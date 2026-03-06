package scoring

import (
	"fmt"
	"time"

	"github.com/sentinel-ai/sentinel/internal/models"
	"github.com/rs/zerolog/log"
)

// StalePolicyThresholdDays — Payer policy older than this is flagged
const StalePolicyThresholdDays = 30

// WeightDivergenceThreshold — >30% deviation from baseline triggers a flag
const WeightDivergenceThreshold = 0.30

// RequiredSignals — signals SENTINEL expects for each decision type
var RequiredSignals = map[models.DecisionType][]string{
	models.DecisionPriorAuth: {
		"payer_policy",
		"patient_history",
		"step_therapy_docs",
		"clinical_criteria",
	},
	models.DecisionDenialPredict: {
		"payer_policy",
		"claim_history",
		"denial_patterns",
	},
	models.DecisionEscalation: {
		"patient_history",
		"complexity_score",
	},
}

// SignalFidelityAuditor checks every piece of evidence MedAgent retrieved
type SignalFidelityAuditor struct {
	scorer *ReliabilityScorer
}

func NewSignalFidelityAuditor(scorer *ReliabilityScorer) *SignalFidelityAuditor {
	return &SignalFidelityAuditor{scorer: scorer}
}

// Audit takes a decision and returns a full fidelity report
func (a *SignalFidelityAuditor) Audit(decision *models.Decision) *models.FidelityReport {
	log.Info().
		Str("decision_id", decision.ID).
		Str("payer", decision.Payer).
		Float64("stated_confidence", decision.Confidence).
		Msg("Starting signal fidelity audit")

	var audits []models.SignalAudit
	criticalFlags := 0

	// 1. Check each retrieved signal
	retrievedTypes := map[string]bool{}
	for _, sig := range decision.RetrievedDocs {
		retrievedTypes[sig.SignalType] = true
		audit := a.auditSignal(sig)
		if audit != nil {
			audits = append(audits, *audit)
			if audit.Severity == "CRITICAL" {
				criticalFlags++
			}
		}
	}

	// 2. Check for missing required signals
	required := RequiredSignals[decision.DecisionType]
	for _, req := range required {
		if !retrievedTypes[req] {
			audits = append(audits, models.SignalAudit{
				SignalType:   req,
				Flags:        []models.FidelityFlag{models.FlagMissingSignal},
				Severity:     "CRITICAL",
				Detail:       fmt.Sprintf("Required signal '%s' was not retrieved at all", req),
				SuggestedFix: fmt.Sprintf("Force retrieval of '%s' before reasoning", req),
			})
			criticalFlags++
		}
	}

	// 3. Confidence-vs-history mismatch
	if a.scorer != nil {
		historicalAccuracy := a.scorer.GetPayerScore(decision.AgentID, decision.Payer)
		gap := decision.Confidence - historicalAccuracy
		if gap > 0.20 {
			severity := "HIGH"
			if gap > 0.30 {
				severity = "CRITICAL"
				criticalFlags++
			}
			audits = append(audits, models.SignalAudit{
				SignalType:   "confidence_calibration",
				Flags:        []models.FidelityFlag{models.FlagConfidenceMismatch},
				Severity:     severity,
				Detail:       fmt.Sprintf("Agent confidence %.0f%% exceeds historical accuracy %.0f%% by %.0f points", decision.Confidence*100, historicalAccuracy*100, gap*100),
				SuggestedFix: fmt.Sprintf("Recalibrate confidence model for %s; consider payer-specific fine-tuning", decision.Payer),
			})
		}
	}

	// 4. Compute overall fidelity score (0.0–1.0)
	overallScore := a.computeOverallScore(audits, len(decision.RetrievedDocs))

	// 5. Determine suggested action
	suggestedAction := a.suggestAction(criticalFlags, overallScore)

	report := &models.FidelityReport{
		DecisionID:      decision.ID,
		OverallScore:    overallScore,
		Audits:          audits,
		CriticalFlags:   criticalFlags,
		SuggestedAction: suggestedAction,
		Timestamp:       time.Now(),
	}

	log.Info().
		Str("decision_id", decision.ID).
		Float64("fidelity_score", overallScore).
		Int("critical_flags", criticalFlags).
		Str("suggested_action", suggestedAction).
		Msg("Signal fidelity audit complete")

	return report
}

func (a *SignalFidelityAuditor) auditSignal(sig models.RetrievedSignal) *models.SignalAudit {
	var flags []models.FidelityFlag
	var details []string
	var fixes []string
	severity := "LOW"

	// Check 1: Policy staleness
	if sig.SignalType == "payer_policy" || sig.SignalType == "clinical_criteria" {
		ageInDays := int(time.Since(sig.VersionDate).Hours() / 24)
		if ageInDays > StalePolicyThresholdDays {
			flags = append(flags, models.FlagStalePolicy)
			details = append(details,
				fmt.Sprintf("Policy version is %d days old (threshold: %d days)", ageInDays, StalePolicyThresholdDays))
			fixes = append(fixes, "Fetch latest payer policy from live API, bypass cache")
			severity = "CRITICAL"
		}
	}

	// Check 2: Incomplete document retrieval
	if sig.PagesRequested > 0 && sig.PagesReturned < sig.PagesRequested {
		completeness := float64(sig.PagesReturned) / float64(sig.PagesRequested)
		flags = append(flags, models.FlagIncompleteRetrieval)
		details = append(details,
			fmt.Sprintf("Retrieved %d/%d pages (%.0f%% complete)",
				sig.PagesReturned, sig.PagesRequested, completeness*100))
		fixes = append(fixes, fmt.Sprintf("Retry retrieval for pages %d-%d",
			sig.PagesReturned+1, sig.PagesRequested))
		if completeness < 0.5 {
			severity = "CRITICAL"
		} else {
			severity = maxSeverity(severity, "HIGH")
		}
	}

	// Check 3: Timeout on retrieval
	if sig.TimedOut {
		flags = append(flags, models.FlagTimeoutOnCritical)
		details = append(details, "Retrieval timed out — content may be incomplete or absent")
		fixes = append(fixes, "Retry with extended timeout or fallback to cached version with staleness warning")
		severity = maxSeverity(severity, "HIGH")
	}

	// Check 4: Weight divergence from baseline
	if sig.BaselineWeight > 0 {
		divergence := absFloat(sig.WeightApplied-sig.BaselineWeight) / sig.BaselineWeight
		if divergence > WeightDivergenceThreshold {
			flags = append(flags, models.FlagWeightDivergence)
			details = append(details,
				fmt.Sprintf("Signal weighted at %.2f but baseline is %.2f (%.0f%% divergence)",
					sig.WeightApplied, sig.BaselineWeight, divergence*100))
			fixes = append(fixes, "Investigate why model is under/over-weighting this signal for this payer")
			severity = maxSeverity(severity, "MEDIUM")
		}
	}

	// No issues found
	if len(flags) == 0 {
		return nil
	}

	return &models.SignalAudit{
		SignalType:   sig.SignalType,
		Flags:        flags,
		Severity:     severity,
		Detail:       joinStrings(details, "; "),
		SuggestedFix: joinStrings(fixes, "; "),
	}
}

func (a *SignalFidelityAuditor) computeOverallScore(audits []models.SignalAudit, totalSignals int) float64 {
	if totalSignals == 0 {
		return 0.0
	}

	score := 1.0
	for _, audit := range audits {
		switch audit.Severity {
		case "CRITICAL":
			score -= 0.25
		case "HIGH":
			score -= 0.15
		case "MEDIUM":
			score -= 0.08
		case "LOW":
			score -= 0.03
		}
	}

	if score < 0 {
		score = 0
	}
	return score
}

func (a *SignalFidelityAuditor) suggestAction(criticalFlags int, overallScore float64) string {
	switch {
	case criticalFlags >= 2:
		return "BLOCK — Multiple critical signal failures. Route to human with full fidelity report."
	case criticalFlags == 1:
		return "PAUSE — Attempt signal repair. If unresolvable, escalate to human."
	case overallScore < 0.6:
		return "NOTIFY — Proceed with caution. Human review recommended within 2 hours."
	default:
		return "PASS — Signal fidelity acceptable for autonomous execution."
	}
}

// ─── helpers ──────────────────────────────────

func maxSeverity(a, b string) string {
	order := map[string]int{"LOW": 0, "MEDIUM": 1, "HIGH": 2, "CRITICAL": 3}
	if order[b] > order[a] {
		return b
	}
	return a
}

func absFloat(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

func joinStrings(ss []string, sep string) string {
	result := ""
	for i, s := range ss {
		if i > 0 {
			result += sep
		}
		result += s
	}
	return result
}
