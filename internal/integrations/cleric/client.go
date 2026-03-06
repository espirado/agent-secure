package cleric

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

// Client creates Cleric incidents when SENTINEL routes to human
// This is the handoff layer — Cleric handles the human workflow
type Client struct {
	apiKey     string
	baseURL    string
	httpClient *http.Client
}

func NewClient() *Client {
	return &Client{
		apiKey:  os.Getenv("CLERIC_API_KEY"),
		baseURL: os.Getenv("CLERIC_BASE_URL"),
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
}

// CreateIncident fires a Cleric incident with full SENTINEL context
// The billing specialist gets everything they need in one place
func (c *Client) CreateIncident(
	decision *models.Decision,
	verdict *models.Verdict,
) (string, error) {

	severity := incidentSeverity(verdict)
	title := fmt.Sprintf(
		"[SENTINEL] %s: %s decision blocked — %s",
		verdict.AuthorityLabel,
		decision.Payer,
		patternName(verdict),
	)

	description := c.buildDescription(decision, verdict)

	incident := map[string]interface{}{
		"title":       title,
		"severity":    severity,
		"description": description,
		"source":      "sentinel",
		"tags": []string{
			fmt.Sprintf("payer:%s", decision.Payer),
			fmt.Sprintf("pattern:%s", patternID(verdict)),
			fmt.Sprintf("authority:%s", verdict.AuthorityLabel),
			fmt.Sprintf("decision_type:%s", decision.DecisionType),
		},
		"metadata": map[string]interface{}{
			"decision_id":       decision.ID,
			"patient_id":        decision.PatientID,
			"procedure_code":    decision.ProcedureCode,
			"agent_prediction":  decision.Prediction,
			"agent_confidence":  decision.Confidence,
			"sentinel_verdict":  verdict.AuthorityLabel,
			"fidelity_score":    verdict.FidelityReport.OverallScore,
			"reliability_score": verdict.ReliabilityScore,
			"suggested_fix":     verdict.SuggestedFix,
			"pattern_accuracy":  patternAccuracy(verdict),
		},
	}

	body, _ := json.Marshal(incident)
	req, err := http.NewRequest("POST",
		c.baseURL+"/api/v1/incidents", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	c.setHeaders(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("cleric incident creation failed: %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		IncidentID string `json:"incident_id"`
		URL        string `json:"url"`
	}
	json.NewDecoder(resp.Body).Decode(&result)

	log.Warn().
		Str("decision_id", decision.ID).
		Str("cleric_incident_id", result.IncidentID).
		Str("payer", decision.Payer).
		Str("severity", severity).
		Msg("Cleric incident created — routing to human billing specialist")

	return result.IncidentID, nil
}

// buildDescription creates a rich incident description for the billing specialist
// This is the critical UX moment — they get context, not just a ticket
func (c *Client) buildDescription(decision *models.Decision, verdict *models.Verdict) string {
	desc := fmt.Sprintf(`## SENTINEL Incident Report

**Decision Blocked:** MedAgent attempted to autonomously %s a %s prior authorization for procedure %s.

**Why SENTINEL Blocked It:**
%s

**Signal Fidelity Issues Detected:**
`, decision.Prediction, decision.Payer, decision.ProcedureCode, verdict.Rationale)

	for _, audit := range verdict.FidelityReport.Audits {
		desc += fmt.Sprintf("- **%s** [%s]: %s\n", audit.SignalType, audit.Severity, audit.Detail)
	}

	if verdict.PatternDetected != nil {
		p := verdict.PatternDetected
		desc += fmt.Sprintf(`
**Reasoning Pattern: %s** (ID: %s)
- Historical accuracy when this pattern fires: **%.0f%%**
- Failure mode: %s
- What to look for: %s
`, p.Name, p.ID, p.Accuracy*100, p.FailureMode, p.MissingEvidence)
	}

	desc += fmt.Sprintf(`
**Recommended Actions:**
%s

**Agent Stated Confidence:** %.0f%% (reliability on %s: %.0f%%)

**Review Checklist:**
- [ ] Verify step therapy documentation is complete (all pages)
- [ ] Confirm current payer policy version (check %s portal)
- [ ] Review patient prior authorization history
- [ ] Make final determination: APPROVE or DENY

*This incident was created by SENTINEL — the AI reasoning auditor monitoring MedAgent.*
`,
		verdict.SuggestedFix,
		decision.Confidence*100,
		decision.Payer,
		verdict.ReliabilityScore*100,
		decision.Payer,
	)

	return desc
}

// ResolveIncident records the human's decision — feeds back into SENTINEL's learning loop
func (c *Client) ResolveIncident(incidentID string, humanDecision string, wasAgentRight bool) error {
	resolution := map[string]interface{}{
		"incident_id":    incidentID,
		"resolution":     "resolved_by_human",
		"human_decision": humanDecision,
		"was_agent_right": wasAgentRight,
		"resolved_at":    time.Now(),
	}

	body, _ := json.Marshal(resolution)
	req, err := http.NewRequest("PATCH",
		c.baseURL+"/api/v1/incidents/"+incidentID+"/resolve", bytes.NewReader(body))
	if err != nil {
		return err
	}
	c.setHeaders(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	log.Info().
		Str("incident_id", incidentID).
		Str("human_decision", humanDecision).
		Bool("was_agent_right", wasAgentRight).
		Msg("Cleric incident resolved — outcome fed back to SENTINEL")

	return nil
}

func (c *Client) setHeaders(req *http.Request) {
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")
}

func incidentSeverity(v *models.Verdict) string {
	switch v.Authority {
	case models.AuthorityQuarantine:
		return "critical"
	case models.AuthorityHumanRequired:
		if v.FidelityReport.CriticalFlags >= 2 {
			return "high"
		}
		return "medium"
	default:
		return "low"
	}
}

func patternID(v *models.Verdict) string {
	if v.PatternDetected != nil {
		return v.PatternDetected.ID
	}
	return "none"
}

func patternName(v *models.Verdict) string {
	if v.PatternDetected != nil {
		return v.PatternDetected.Name
	}
	return "Unknown Pattern"
}

func patternAccuracy(v *models.Verdict) float64 {
	if v.PatternDetected != nil {
		return v.PatternDetected.Accuracy
	}
	return 0
}
