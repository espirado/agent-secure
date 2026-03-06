package elevenlabs

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/sentinel-ai/sentinel/internal/models"
	"github.com/rs/zerolog/log"
)

const elevenLabsAPIBase = "https://api.elevenlabs.io/v1"

// Client generates the SENTINEL morning voice briefing
// Every morning the ops team hears what SENTINEL caught overnight
type Client struct {
	apiKey  string
	voiceID string // pre-selected professional voice
	httpClient *http.Client
}

func NewClient() *Client {
	voiceID := os.Getenv("ELEVENLABS_VOICE_ID")
	if voiceID == "" {
		voiceID = "21m00Tcm4TlvDq8ikWAM" // Rachel — professional, clear
	}
	return &Client{
		apiKey:  os.Getenv("ELEVENLABS_API_KEY"),
		voiceID: voiceID,
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
}

// GenerateMorningBrief creates the daily ops briefing audio
// This is the ElevenLabs demo moment — the AI that watches AI speaks
func (c *Client) GenerateMorningBrief(
	profile *models.ReliabilityProfile,
	todayStats TodayStats,
) ([]byte, string, error) {

	script := c.writeBriefScript(profile, todayStats)

	log.Info().
		Str("agent_id", profile.AgentID).
		Int("script_length", len(script)).
		Msg("Generating SENTINEL morning brief via ElevenLabs")

	payload := map[string]interface{}{
		"text":     script,
		"model_id": "eleven_monolingual_v1",
		"voice_settings": map[string]interface{}{
			"stability":        0.75,
			"similarity_boost": 0.85,
		},
	}

	body, _ := json.Marshal(payload)
	req, err := http.NewRequest("POST",
		elevenLabsAPIBase+"/text-to-speech/"+c.voiceID,
		bytes.NewReader(body))
	if err != nil {
		return nil, script, err
	}
	req.Header.Set("xi-api-key", c.apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "audio/mpeg")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, script, fmt.Errorf("elevenlabs TTS failed: %w", err)
	}
	defer resp.Body.Close()

	audio, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, script, err
	}

	log.Info().
		Int("audio_bytes", len(audio)).
		Msg("Morning brief audio generated")

	return audio, script, nil
}

// writeBriefScript composes the morning briefing text
func (c *Client) writeBriefScript(
	profile *models.ReliabilityProfile,
	stats TodayStats,
) string {
	now := time.Now()
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf(
		"Good morning. This is your SENTINEL reliability report for %s. ",
		now.Format("January 2nd"),
	))

	// Autonomous rate
	sb.WriteString(fmt.Sprintf(
		"MedAgent processed %d prior authorization decisions yesterday. "+
			"%d were handled fully autonomously. "+
			"%d were escalated to human review. ",
		stats.TotalDecisions,
		stats.AutonomousDecisions,
		stats.HumanEscalations,
	))

	// Drift warnings — most important part
	if len(profile.DriftDetected) > 0 {
		sb.WriteString(fmt.Sprintf(
			"Current concern: accuracy has declined significantly on %s. ",
			strings.Join(profile.DriftDetected, " and "),
		))

		for _, payer := range profile.DriftDetected {
			payerProfile := getPayerProfile(profile, payer)
			if payerProfile != nil {
				sb.WriteString(fmt.Sprintf(
					"MedAgent is currently at %.0f%% accuracy on %s, "+
						"down from a recent high of %.0f%%. "+
						"SENTINEL has quarantined all %s decisions pending investigation. ",
					payerProfile.Accuracy*100,
					payer,
					peakAccuracy(payerProfile.WeeklyAccuracy)*100,
					payer,
				))
			}
		}

		sb.WriteString(
			"Probable cause: a recent payer policy update not yet reflected in MedAgent's knowledge. " +
				"Recommended action: contact your payer representative to request updated coverage guidelines. ",
		)
	} else {
		sb.WriteString("No accuracy drift detected across monitored payers. All systems nominal. ")
	}

	// Top performing payers
	bestPayer := bestPayerProfile(profile)
	if bestPayer != nil {
		sb.WriteString(fmt.Sprintf(
			"%s remains in full autonomy mode at %.0f%% accuracy. ",
			bestPayer.Payer,
			bestPayer.Accuracy*100,
		))
	}

	// Pattern summary
	if stats.PatternDeltaFired > 0 {
		sb.WriteString(fmt.Sprintf(
			"Pattern Delta — our stale policy plus incomplete retrieval signature — "+
				"fired %d times overnight. "+
				"SENTINEL blocked autonomous execution in all %d cases. ",
			stats.PatternDeltaFired, stats.PatternDeltaFired,
		))
	}

	// Closing
	sb.WriteString(fmt.Sprintf(
		"Today's expected autonomous rate: %.0f%%. "+
			"End of SENTINEL reliability report.",
		stats.ExpectedAutonomousRate*100,
	))

	return sb.String()
}

// TodayStats captures daily summary metrics for the brief
type TodayStats struct {
	TotalDecisions         int     `json:"total_decisions"`
	AutonomousDecisions    int     `json:"autonomous_decisions"`
	HumanEscalations       int     `json:"human_escalations"`
	PatternDeltaFired      int     `json:"pattern_delta_fired"`
	ExpectedAutonomousRate float64 `json:"expected_autonomous_rate"`
}

// ── helpers ──────────────────────────────────

func getPayerProfile(profile *models.ReliabilityProfile, payer string) *models.PayerProfile {
	for i, p := range profile.ByPayer {
		if p.Payer == payer {
			return &profile.ByPayer[i]
		}
	}
	return nil
}

func bestPayerProfile(profile *models.ReliabilityProfile) *models.PayerProfile {
	var best *models.PayerProfile
	for i, p := range profile.ByPayer {
		if !p.DriftFlag && (best == nil || p.Accuracy > best.Accuracy) {
			best = &profile.ByPayer[i]
		}
	}
	return best
}

func peakAccuracy(weekly []float64) float64 {
	peak := 0.0
	for _, w := range weekly {
		if w > peak {
			peak = w
		}
	}
	return peak
}
