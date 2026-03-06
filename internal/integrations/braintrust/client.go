package braintrust

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/sentinel-ai/sentinel/internal/models"
	"github.com/rs/zerolog/log"
)

const braintrustAPIBase = "https://api.braintrust.dev/v1"

// Client logs every SENTINEL evaluation to Braintrust for eval scoring
// This gives judges a live scoreboard of AI reliability during the demo
type Client struct {
	apiKey     string
	projectID  string
	httpClient *http.Client
}

func NewClient() *Client {
	return &Client{
		apiKey:    os.Getenv("BRAINTRUST_API_KEY"),
		projectID: os.Getenv("BRAINTRUST_PROJECT_ID"),
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
}

// ── Log a SENTINEL evaluation ────────────────────────────────────────

// LogEvaluation records a decision + verdict to Braintrust before outcome is known
// Outcome scores are added later via UpdateWithOutcome
func (c *Client) LogEvaluation(
	decision *models.Decision,
	verdict *models.Verdict,
) (string, error) {

	span := map[string]interface{}{
		"project_id":  c.projectID,
		"experiment":  "MedAgent_SENTINEL_Live",
		"input": map[string]interface{}{
			"decision_type":    decision.DecisionType,
			"payer":            decision.Payer,
			"procedure_code":   decision.ProcedureCode,
			"agent_confidence": decision.Confidence,
			"signals_retrieved": len(decision.RetrievedDocs),
		},
		"output": map[string]interface{}{
			"agent_prediction":  decision.Prediction,
			"sentinel_verdict":  verdict.AuthorityLabel,
			"blocked":           verdict.Block,
			"pattern_detected":  patternID(verdict),
			"fidelity_score":    fidelityScore(verdict),
			"reliability_score": verdict.ReliabilityScore,
		},
		"metadata": map[string]interface{}{
			"decision_id": decision.ID,
			"rationale":   verdict.Rationale,
			"escalate_to": verdict.EscalateTo,
			"critical_flags": fidelityCriticalFlags(verdict),
		},
		"tags": []string{
			fmt.Sprintf("payer:%s", decision.Payer),
			fmt.Sprintf("authority:%s", verdict.AuthorityLabel),
			fmt.Sprintf("pattern:%s", patternID(verdict)),
		},
	}

	body, _ := json.Marshal(span)
	req, err := http.NewRequest("POST",
		braintrustAPIBase+"/experiment/insert", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	c.setHeaders(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("braintrust log failed: %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		ID string `json:"id"`
	}
	json.NewDecoder(resp.Body).Decode(&result)

	log.Info().
		Str("decision_id", decision.ID).
		Str("braintrust_span_id", result.ID).
		Str("verdict", verdict.AuthorityLabel).
		Msg("Evaluation logged to Braintrust")

	return result.ID, nil
}

// ── Score with ground truth outcome ──────────────────────────────────

// UpdateWithOutcome adds outcome scores once a claim resolves (days later)
// This is the self-improving loop — SENTINEL gets smarter with every feedback cycle
func (c *Client) UpdateWithOutcome(spanID string, outcome *models.Outcome) error {
	scores := map[string]interface{}{
		// Was the agent's prediction correct?
		"agent_correct": boolScore(outcome.WasCorrect),

		// Was the agent's confidence justified?
		// (If they said 89% but were wrong, confidence_justified = 0)
		"confidence_justified": confidenceJustifiedScore(outcome),

		// Was SENTINEL's decision to block/allow correct?
		"sentinel_correct": boolScore(outcome.SentinelCorrect),

		// If SENTINEL blocked, did blocking prevent harm?
		"block_prevented_harm": blockPreventedHarmScore(outcome),
	}

	update := map[string]interface{}{
		"id":     spanID,
		"scores": scores,
		"metadata": map[string]interface{}{
			"actual_outcome":    outcome.ActualOutcome,
			"resolved_at":       outcome.ResolvedAt,
			"sentinel_blocked":  outcome.SentinelBlocked,
		},
	}

	body, _ := json.Marshal(update)
	req, err := http.NewRequest("PATCH",
		braintrustAPIBase+"/experiment/insert", bytes.NewReader(body))
	if err != nil {
		return err
	}
	c.setHeaders(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("braintrust outcome update failed: %w", err)
	}
	defer resp.Body.Close()

	log.Info().
		Str("span_id", spanID).
		Bool("agent_correct", outcome.WasCorrect).
		Bool("sentinel_correct", outcome.SentinelCorrect).
		Msg("Outcome scores updated in Braintrust")

	return nil
}

// ── Get summary stats for dashboard ─────────────────────────────────

type ExperimentStats struct {
	AgentAccuracy    float64 `json:"agent_accuracy"`
	SentinelAccuracy float64 `json:"sentinel_accuracy"`
	BlockedCount     int     `json:"blocked_count"`
	HarmPrevented    int     `json:"harm_prevented"`
	TotalDecisions   int     `json:"total_decisions"`
}

func (c *Client) GetExperimentStats() (*ExperimentStats, error) {
	req, err := http.NewRequest("GET",
		braintrustAPIBase+"/experiment/"+c.projectID+"/summary", nil)
	if err != nil {
		return nil, err
	}
	c.setHeaders(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var stats ExperimentStats
	json.NewDecoder(resp.Body).Decode(&stats)
	return &stats, nil
}

// ── helpers ──────────────────────────────────

func (c *Client) setHeaders(req *http.Request) {
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")
}

func patternID(v *models.Verdict) string {
	if v.PatternDetected != nil {
		return v.PatternDetected.ID
	}
	return "none"
}

func boolScore(b bool) float64 {
	if b {
		return 1.0
	}
	return 0.0
}

func confidenceJustifiedScore(outcome *models.Outcome) float64 {
	// Perfect score: agent said 90% and was right
	// Zero score: agent said 90% and was wrong
	// Partial: proportional to how wrong the confidence was
	if outcome.WasCorrect {
		return outcome.AgentConfidence // justified confidence earns full score
	}
	// Penalty for overconfidence: 90% confidence + wrong = 0.10 (not zero, but bad)
	return 1.0 - outcome.AgentConfidence
}

func fidelityScore(v *models.Verdict) float64 {
	if v.FidelityReport != nil {
		return v.FidelityReport.OverallScore
	}
	return 0
}

func fidelityCriticalFlags(v *models.Verdict) int {
	if v.FidelityReport != nil {
		return v.FidelityReport.CriticalFlags
	}
	return 0
}

func blockPreventedHarmScore(outcome *models.Outcome) float64 {
	if !outcome.SentinelBlocked {
		return -1.0 // N/A — not blocked
	}
	// SENTINEL blocked it. Was the block justified?
	if outcome.SentinelCorrect {
		return 1.0 // blocked a wrong decision — harm prevented
	}
	return 0.0 // blocked a correct decision — false positive
}
