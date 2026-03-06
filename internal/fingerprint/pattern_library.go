package fingerprint

import (
	"encoding/json"
	"os"
	"sync"
	"time"

	"github.com/sentinel-ai/sentinel/internal/models"
	"github.com/rs/zerolog/log"
)

// PatternLibrary holds all known failure patterns, loaded from history
type PatternLibrary struct {
	mu               sync.RWMutex
	patterns         []models.ReasoningPattern
	decisionPatterns map[string]string // decision_id → pattern_id
}

func NewPatternLibrary() *PatternLibrary {
	lib := &PatternLibrary{
		decisionPatterns: make(map[string]string),
	}
	lib.loadDefaults()
	return lib
}

// loadDefaults seeds the library with known patterns from prior Kustode/VGAC data
func (pl *PatternLibrary) loadDefaults() {
	pl.patterns = []models.ReasoningPattern{
		{
			ID:          "pattern_alpha",
			Name:        "Clean Retrieval",
			Description: "Full signal retrieval, current policy, balanced weighting",
			Flags:       []models.FidelityFlag{},
			Accuracy:    0.91,
			Occurrences: 0,
			FailureMode: "N/A — reliable pattern",
			Severity:    "LOW",
		},
		{
			ID:          "pattern_delta",
			Name:        "Stale + Incomplete",
			Description: "Stale policy version combined with incomplete document retrieval",
			Flags: []models.FidelityFlag{
				models.FlagStalePolicy,
				models.FlagIncompleteRetrieval,
			},
			Accuracy:        0.23,
			Occurrences:     0,
			FailureMode:     "Agent reasons from outdated policy + partial evidence. Overconfidence systematic.",
			MissingEvidence: "Current payer policy (< 30 days) and complete step therapy documentation",
			Payers:          []string{"Aetna", "Cigna"},
			Severity:        "CRITICAL",
		},
		{
			ID:          "pattern_echo",
			Name:        "Timeout Cascade",
			Description: "Multiple retrieval timeouts causing reasoning on empty or stale cache",
			Flags: []models.FidelityFlag{
				models.FlagTimeoutOnCritical,
				models.FlagMissingSignal,
			},
			Accuracy:        0.31,
			Occurrences:     0,
			FailureMode:     "Vector DB cache staleness causes timeouts. Agent proceeds with defaults.",
			MissingEvidence: "Live payer API data",
			Severity:        "CRITICAL",
		},
		{
			ID:          "pattern_ghost",
			Name:        "Weight Inversion",
			Description: "Agent dramatically under-weights patient history in favour of policy matching",
			Flags: []models.FidelityFlag{
				models.FlagWeightDivergence,
			},
			Accuracy:        0.54,
			Occurrences:     0,
			FailureMode:     "Patient-specific context deprioritised. Policy-only reasoning misses individual exceptions.",
			MissingEvidence: "Patient history with sufficient weight (baseline: 0.34)",
			Severity:        "HIGH",
		},
		{
			ID:              "pattern_sigma",
			Name:            "Confidence Mismatch",
			Description:     "Agent confidence significantly exceeds historical accuracy for this payer",
			Flags: []models.FidelityFlag{
				models.FlagConfidenceMismatch,
			},
			Accuracy:        0.42,
			Occurrences:     0,
			FailureMode:     "Confidence model not recalibrated after payer policy changes. Agent believes it is right when data says otherwise.",
			MissingEvidence: "Updated calibration data from recent payer outcomes",
			Payers:          []string{"Aetna"},
			Severity:        "HIGH",
		},
	}

	log.Info().Int("patterns_loaded", len(pl.patterns)).Msg("Pattern library initialised")
}

// Classify takes a fidelity report and returns the best matching pattern
func (pl *PatternLibrary) Classify(report *models.FidelityReport) *models.ReasoningPattern {
	pl.mu.Lock()
	defer pl.mu.Unlock()

	if len(report.Audits) == 0 {
		p := pl.patterns[0] // pattern_alpha — clean
		pl.decisionPatterns[report.DecisionID] = p.ID
		return &p
	}

	// Collect all flags from the report
	activeFlags := map[models.FidelityFlag]bool{}
	for _, audit := range report.Audits {
		for _, flag := range audit.Flags {
			activeFlags[flag] = true
		}
	}

	bestMatch := -1
	bestScore := 0

	for i, pattern := range pl.patterns {
		if len(pattern.Flags) == 0 {
			continue // skip pattern_alpha in matching
		}

		matchScore := 0
		for _, pFlag := range pattern.Flags {
			if activeFlags[pFlag] {
				matchScore++
			}
		}

		if matchScore > bestScore {
			bestScore = matchScore
			bestMatch = i
		}
	}

	if bestMatch == -1 || bestScore == 0 {
		// No known pattern — return a generic unknown pattern
		pl.decisionPatterns[report.DecisionID] = "pattern_unknown"
		return &models.ReasoningPattern{
			ID:          "pattern_unknown",
			Name:        "Unknown Failure Signature",
			Description: "Signal flags present but no matching pattern in library",
			Accuracy:    0.50,
			FailureMode: "Novel failure mode — recommend investigation and pattern addition",
			Severity:    "HIGH",
		}
	}

	matched := pl.patterns[bestMatch]
	pl.decisionPatterns[report.DecisionID] = matched.ID
	log.Info().
		Str("pattern_id", matched.ID).
		Str("pattern_name", matched.Name).
		Float64("historical_accuracy", matched.Accuracy).
		Int("flag_matches", bestScore).
		Msg("Reasoning pattern classified")

	return &matched
}

// RecordOutcome updates pattern accuracy from a resolved outcome
func (pl *PatternLibrary) RecordOutcome(patternID string, wasCorrect bool) {
	pl.mu.Lock()
	defer pl.mu.Unlock()

	for i, p := range pl.patterns {
		if p.ID == patternID {
			// Exponential moving average update
			alpha := 0.1
			if wasCorrect {
				pl.patterns[i].Accuracy = (1-alpha)*p.Accuracy + alpha*1.0
			} else {
				pl.patterns[i].Accuracy = (1-alpha)*p.Accuracy + alpha*0.0
			}
			pl.patterns[i].Occurrences++
			pl.patterns[i].LastSeen = time.Now()

			log.Info().
				Str("pattern_id", patternID).
				Float64("updated_accuracy", pl.patterns[i].Accuracy).
				Int("occurrences", pl.patterns[i].Occurrences).
				Bool("was_correct", wasCorrect).
				Msg("Pattern accuracy updated from outcome")
			return
		}
	}
}

// RecordOutcomeForDecision looks up the pattern classified for a decision and updates its accuracy
func (pl *PatternLibrary) RecordOutcomeForDecision(decisionID string, wasCorrect bool) {
	pl.mu.Lock()
	defer pl.mu.Unlock()

	patternID, ok := pl.decisionPatterns[decisionID]
	if !ok {
		log.Warn().Str("decision_id", decisionID).Msg("No pattern mapping found for decision")
		return
	}
	delete(pl.decisionPatterns, decisionID)

	for i, p := range pl.patterns {
		if p.ID == patternID {
			alpha := 0.1
			if wasCorrect {
				pl.patterns[i].Accuracy = (1-alpha)*p.Accuracy + alpha*1.0
			} else {
				pl.patterns[i].Accuracy = (1-alpha)*p.Accuracy + alpha*0.0
			}
			pl.patterns[i].Occurrences++
			pl.patterns[i].LastSeen = time.Now()

			log.Info().
				Str("decision_id", decisionID).
				Str("pattern_id", patternID).
				Float64("updated_accuracy", pl.patterns[i].Accuracy).
				Bool("was_correct", wasCorrect).
				Msg("Pattern accuracy updated from decision outcome")
			return
		}
	}
}

// Export saves pattern library to disk (for persistence between hackathon restarts)
func (pl *PatternLibrary) Export(path string) error {
	pl.mu.RLock()
	defer pl.mu.RUnlock()

	data, err := json.MarshalIndent(pl.patterns, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

// GetAll returns all patterns for the Lightdash dashboard
func (pl *PatternLibrary) GetAll() []models.ReasoningPattern {
	pl.mu.RLock()
	defer pl.mu.RUnlock()
	result := make([]models.ReasoningPattern, len(pl.patterns))
	copy(result, pl.patterns)
	return result
}
