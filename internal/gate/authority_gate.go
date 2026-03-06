package gate

import (
	"fmt"
	"time"

	"github.com/sentinel-ai/sentinel/internal/fingerprint"
	"github.com/sentinel-ai/sentinel/internal/models"
	"github.com/sentinel-ai/sentinel/internal/scoring"
	"github.com/rs/zerolog/log"
)

// Thresholds for authority levels
const (
	ThresholdFullAuto      = 0.85
	ThresholdActAndNotify  = 0.65
	ThresholdHumanRequired = 0.0 // everything below ActAndNotify
)

// AuthorityGate is the core SENTINEL decision engine
// It intercepts MedAgent decisions BEFORE they are executed
type AuthorityGate struct {
	scorer   *scoring.ReliabilityScorer
	auditor  *scoring.SignalFidelityAuditor
	patterns *fingerprint.PatternLibrary
}

func NewAuthorityGate(
	scorer *scoring.ReliabilityScorer,
	auditor *scoring.SignalFidelityAuditor,
	patterns *fingerprint.PatternLibrary,
) *AuthorityGate {
	return &AuthorityGate{
		scorer:   scorer,
		auditor:  auditor,
		patterns: patterns,
	}
}

// Evaluate is called for every MedAgent decision before execution
// This is the jaw-drop moment of the demo — interception mid-reasoning
func (g *AuthorityGate) Evaluate(decision *models.Decision) *models.Verdict {
	log.Info().
		Str("decision_id", decision.ID).
		Str("agent", decision.AgentID).
		Str("payer", decision.Payer).
		Str("prediction", decision.Prediction).
		Float64("agent_confidence", decision.Confidence).
		Msg("SENTINEL intercepting decision for evaluation")

	// ── Step 1: Signal Fidelity Audit ──────────────────────
	fidelityReport := g.auditor.Audit(decision)

	// ── Step 2: Reasoning Pattern Classification ────────────
	pattern := g.patterns.Classify(fidelityReport)

	// ── Step 3: Reliability Score (historical) ───────────────
	reliabilityScore := g.scorer.GetPayerScore(decision.AgentID, decision.Payer)
	isDrifting := g.scorer.IsDrifting(decision.AgentID, decision.Payer)

	// ── Step 4: Determine Authority Level ───────────────────
	authority := g.computeAuthority(reliabilityScore, isDrifting, fidelityReport)

	// ── Step 5: Build the Verdict ────────────────────────────
	verdict := &models.Verdict{
		DecisionID:       decision.ID,
		Authority:        authority,
		AuthorityLabel:   authority.String(),
		Block:            authority < models.AuthorityActAndNotify,
		PatternDetected:  pattern,
		FidelityReport:   fidelityReport,
		ReliabilityScore: reliabilityScore,
		Rationale:        g.buildRationale(decision, authority, pattern, fidelityReport, reliabilityScore, isDrifting),
		SuggestedFix:     g.buildSuggestedFix(fidelityReport, pattern),
		EscalateTo:       g.escalationTarget(authority),
		Timestamp:        time.Now(),
	}

	log.Warn().
		Str("decision_id", decision.ID).
		Str("authority", authority.String()).
		Bool("blocked", verdict.Block).
		Str("pattern", pattern.ID).
		Float64("fidelity_score", fidelityReport.OverallScore).
		Float64("reliability_score", reliabilityScore).
		Bool("drift_detected", isDrifting).
		Msg("SENTINEL verdict issued")

	return verdict
}

func (g *AuthorityGate) computeAuthority(
	reliability float64,
	isDrifting bool,
	fidelity *models.FidelityReport,
) models.AuthorityLevel {
	// Quarantine overrides everything — drift means something external changed
	if isDrifting {
		return models.AuthorityQuarantine
	}

	// Critical fidelity failures override reliability score
	// Even a historically reliable agent should not act autonomously on broken evidence
	if fidelity.CriticalFlags >= 2 {
		return models.AuthorityHumanRequired
	}

	// Standard reliability thresholds
	switch {
	case reliability >= ThresholdFullAuto && fidelity.OverallScore >= 0.8:
		return models.AuthorityFullAuto
	case reliability >= ThresholdActAndNotify:
		return models.AuthorityActAndNotify
	default:
		return models.AuthorityHumanRequired
	}
}

func (g *AuthorityGate) buildRationale(
	decision *models.Decision,
	authority models.AuthorityLevel,
	pattern *models.ReasoningPattern,
	fidelity *models.FidelityReport,
	reliability float64,
	isDrifting bool,
) string {
	switch authority {
	case models.AuthorityQuarantine:
		return fmt.Sprintf(
			"QUARANTINE: Accuracy on %s has declined significantly in the past 7 days "+
				"(current: %.0f%%). Likely cause: payer policy update not reflected in training data. "+
				"All %s decisions require human review until drift is resolved.",
			decision.Payer, reliability*100, decision.Payer,
		)

	case models.AuthorityHumanRequired:
		if fidelity.CriticalFlags > 0 {
			return fmt.Sprintf(
				"HUMAN REQUIRED: %d critical signal fidelity failures detected. "+
					"Pattern '%s' identified (historical accuracy: %.0f%%). "+
					"Agent stated %.0f%% confidence but reasoning is structurally compromised.",
				fidelity.CriticalFlags, pattern.Name, pattern.Accuracy*100, decision.Confidence*100,
			)
		}
		return fmt.Sprintf(
			"HUMAN REQUIRED: Reliability on %s is %.0f%% — below autonomous execution threshold (65%%). "+
				"Agent confidence of %.0f%% overstates actual reliability by %.0f points.",
			decision.Payer, reliability*100, decision.Confidence*100,
			(decision.Confidence-reliability)*100,
		)

	case models.AuthorityActAndNotify:
		return fmt.Sprintf(
			"ACT AND NOTIFY: Reliability on %s is %.0f%%. Proceeding autonomously but "+
				"requiring human verification within 2 hours. "+
				"Fidelity score: %.0f%%.",
			decision.Payer, reliability*100, fidelity.OverallScore*100,
		)

	case models.AuthorityFullAuto:
		return fmt.Sprintf(
			"FULL AUTONOMY: Reliability on %s is %.0f%%. Fidelity score: %.0f%%. "+
				"Pattern '%s' — no blocking concerns. Proceeding autonomously.",
			decision.Payer, reliability*100, fidelity.OverallScore*100, pattern.Name,
		)
	}
	return "Unknown authority state"
}

func (g *AuthorityGate) buildSuggestedFix(
	fidelity *models.FidelityReport,
	pattern *models.ReasoningPattern,
) string {
	if len(fidelity.Audits) == 0 {
		return "No action required."
	}

	fix := "Recommended actions: "
	for i, audit := range fidelity.Audits {
		if i > 0 {
			fix += " | "
		}
		fix += audit.SuggestedFix
	}

	if pattern.MissingEvidence != "" {
		fix += fmt.Sprintf(" | Ensure retrieval of: %s", pattern.MissingEvidence)
	}

	return fix
}

func (g *AuthorityGate) escalationTarget(authority models.AuthorityLevel) string {
	switch authority {
	case models.AuthorityQuarantine, models.AuthorityHumanRequired:
		return "cleric" // Cleric creates the incident, human billing specialist reviews
	case models.AuthorityActAndNotify:
		return "slack_notify" // notify only, no escalation
	default:
		return "none"
	}
}
