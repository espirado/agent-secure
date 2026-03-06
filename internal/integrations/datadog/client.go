package datadog

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/sentinel-ai/sentinel/internal/models"
	"github.com/rs/zerolog/log"
)

const (
	datadogAPIBase    = "https://api.datadoghq.com"
	datadogEventsPath = "/api/v1/events"
	datadogMetricPath = "/api/v2/series"
)

// Client wraps the Datadog API for SENTINEL's ingestion layer
type Client struct {
	apiKey     string
	appKey     string
	httpClient *http.Client
}

func NewClient() *Client {
	return &Client{
		apiKey: os.Getenv("DD_API_KEY"),
		appKey: os.Getenv("DD_APP_KEY"),
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// ── Ingest MedAgent Decision from Datadog Event stream ──────────────

// PullDecisions queries Datadog for recent MedAgent decision events
func (c *Client) PullDecisions(agentID string, since time.Time) ([]models.Decision, error) {
	log.Info().
		Str("agent_id", agentID).
		Time("since", since).
		Msg("Pulling MedAgent decisions from Datadog")

	url := fmt.Sprintf("%s%s?start=%d&end=%d&tags=source:medagent,agent_id:%s",
		datadogAPIBase, datadogEventsPath,
		since.Unix(), time.Now().Unix(), agentID)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("building datadog request: %w", err)
	}
	c.setHeaders(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("datadog request failed: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	var ddResp struct {
		Events []struct {
			Title string `json:"title"`
			Text  string `json:"text"`
			Tags  []string `json:"tags"`
			DateHappened int64 `json:"date_happened"`
		} `json:"events"`
	}

	if err := json.Unmarshal(body, &ddResp); err != nil {
		return nil, fmt.Errorf("parsing datadog response: %w", err)
	}

	var decisions []models.Decision
	for _, event := range ddResp.Events {
		var d models.Decision
		if err := json.Unmarshal([]byte(event.Text), &d); err != nil {
			log.Warn().Str("event_title", event.Title).Msg("Could not parse decision event")
			continue
		}
		d.Timestamp = time.Unix(event.DateHappened, 0)
		decisions = append(decisions, d)
	}

	log.Info().Int("decisions_pulled", len(decisions)).Msg("Decisions pulled from Datadog")
	return decisions, nil
}

// ── Emit SENTINEL Verdict to Datadog ────────────────────────────────

// EmitVerdict sends SENTINEL's verdict as a Datadog event for full audit trail
func (c *Client) EmitVerdict(verdict *models.Verdict) error {
	payload := map[string]interface{}{
		"title": fmt.Sprintf("SENTINEL Verdict: %s [%s]",
			verdict.AuthorityLabel, verdict.DecisionID),
		"text": mustMarshal(verdict),
		"tags": []string{
			"source:sentinel",
			fmt.Sprintf("authority:%s", verdict.AuthorityLabel),
			fmt.Sprintf("blocked:%v", verdict.Block),
			fmt.Sprintf("pattern:%s", verdictPatternID(verdict)),
		},
		"alert_type": verdictAlertType(verdict),
		"date_happened": verdict.Timestamp.Unix(),
	}

	return c.postEvent(payload)
}

// ── Emit Reliability Metrics to Datadog ─────────────────────────────

// EmitReliabilityMetrics sends per-payer accuracy as Datadog metrics
// This allows Datadog monitors to alert when SENTINEL detects drift
func (c *Client) EmitReliabilityMetrics(profile *models.ReliabilityProfile) error {
	now := time.Now().Unix()

	var series []map[string]interface{}

	// Overall accuracy metric
	series = append(series, map[string]interface{}{
		"metric": "sentinel.agent.reliability.overall",
		"type":   "gauge",
		"points": [][]interface{}{{now, profile.OverallAccuracy}},
		"tags":   []string{fmt.Sprintf("agent_id:%s", profile.AgentID)},
	})

	// Calibration error metric
	series = append(series, map[string]interface{}{
		"metric": "sentinel.agent.reliability.calibration_error",
		"type":   "gauge",
		"points": [][]interface{}{{now, profile.CalibrationError}},
		"tags":   []string{fmt.Sprintf("agent_id:%s", profile.AgentID)},
	})

	// Per-payer accuracy metrics — this is what triggers Datadog drift alerts
	for _, payerProfile := range profile.ByPayer {
		tags := []string{
			fmt.Sprintf("agent_id:%s", profile.AgentID),
			fmt.Sprintf("payer:%s", payerProfile.Payer),
			fmt.Sprintf("drift:%v", payerProfile.DriftFlag),
			fmt.Sprintf("trend:%s", payerProfile.Trend),
		}

		series = append(series, map[string]interface{}{
			"metric": "sentinel.agent.reliability.by_payer",
			"type":   "gauge",
			"points": [][]interface{}{{now, payerProfile.Accuracy}},
			"tags":   tags,
		})

		// Emit drift as a binary metric for easy alerting
		driftVal := 0.0
		if payerProfile.DriftFlag {
			driftVal = 1.0
		}
		series = append(series, map[string]interface{}{
			"metric": "sentinel.agent.drift_detected",
			"type":   "gauge",
			"points": [][]interface{}{{now, driftVal}},
			"tags":   tags,
		})
	}

	payload := map[string]interface{}{"series": series}
	body, _ := json.Marshal(payload)

	req, err := http.NewRequest("POST", datadogAPIBase+datadogMetricPath, bytes.NewReader(body))
	if err != nil {
		return err
	}
	c.setHeaders(req)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("emitting reliability metrics: %w", err)
	}
	defer resp.Body.Close()

	log.Info().
		Str("agent_id", profile.AgentID).
		Int("payers", len(profile.ByPayer)).
		Int("drift_payers", len(profile.DriftDetected)).
		Msg("Reliability metrics emitted to Datadog")

	return nil
}

// ── Datadog Monitor Setup ────────────────────────────────────────────

// CreateDriftMonitor creates a Datadog monitor that fires when SENTINEL detects drift
// This is the "Datadog watching SENTINEL watching MedAgent" demo moment
func (c *Client) CreateDriftMonitor(agentID, payer string) error {
	monitor := map[string]interface{}{
		"name": fmt.Sprintf("SENTINEL: %s accuracy drift on %s", agentID, payer),
		"type": "metric alert",
		"query": fmt.Sprintf(
			`avg(last_1h):avg:sentinel.agent.reliability.by_payer{agent_id:%s,payer:%s} < 0.65`,
			agentID, payer,
		),
		"message": fmt.Sprintf(
			"@slack-sentinel-alerts SENTINEL has detected accuracy drift for %s on %s. "+
				"Current accuracy has fallen below 65%%. "+
				"All %s decisions are being routed to human review. "+
				"Investigate payer policy changes.",
			agentID, payer, payer,
		),
		"tags": []string{
			"sentinel:drift_monitor",
			fmt.Sprintf("agent_id:%s", agentID),
			fmt.Sprintf("payer:%s", payer),
		},
		"options": map[string]interface{}{
			"thresholds": map[string]float64{
				"critical": 0.65,
				"warning":  0.75,
			},
			"notify_no_data": false,
			"evaluation_delay": 300,
		},
	}

	body, _ := json.Marshal(monitor)
	req, err := http.NewRequest("POST",
		datadogAPIBase+"/api/v1/monitor", bytes.NewReader(body))
	if err != nil {
		return err
	}
	c.setHeaders(req)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	log.Info().
		Str("agent_id", agentID).
		Str("payer", payer).
		Msg("Drift monitor created in Datadog")

	return nil
}

// ── helpers ──────────────────────────────────

func (c *Client) setHeaders(req *http.Request) {
	req.Header.Set("DD-API-KEY", c.apiKey)
	req.Header.Set("DD-APPLICATION-KEY", c.appKey)
}

func (c *Client) postEvent(payload map[string]interface{}) error {
	body, _ := json.Marshal(payload)
	req, err := http.NewRequest("POST",
		datadogAPIBase+datadogEventsPath, bytes.NewReader(body))
	if err != nil {
		return err
	}
	c.setHeaders(req)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("datadog event API returned %d: %s", resp.StatusCode, string(respBody))
	}
	return nil
}

func mustMarshal(v interface{}) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func verdictPatternID(v *models.Verdict) string {
	if v.PatternDetected != nil {
		return v.PatternDetected.ID
	}
	return "none"
}

func verdictAlertType(v *models.Verdict) string {
	if v.Block {
		return "error"
	}
	if v.Authority == models.AuthorityActAndNotify {
		return "warning"
	}
	return "info"
}
