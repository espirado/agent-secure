package scoring

import (
	"math"
	"sync"
	"time"

	"github.com/sentinel-ai/sentinel/internal/models"
	"github.com/rs/zerolog/log"
)

// DriftThreshold — accuracy drop of this magnitude in 7 days triggers quarantine flag
const DriftThreshold = 0.20

// MinSampleForDrift — need at least this many decisions before we can declare drift
const MinSampleForDrift = 10

// ReliabilityScorer maintains a rolling accuracy record per agent × payer
type ReliabilityScorer struct {
	mu       sync.RWMutex
	outcomes map[string][]scoredOutcome // key: "agentID:payer"
}

type scoredOutcome struct {
	Correct    bool
	Confidence float64
	Timestamp  time.Time
}

func NewReliabilityScorer() *ReliabilityScorer {
	return &ReliabilityScorer{
		outcomes: make(map[string][]scoredOutcome),
	}
}

// RecordOutcome ingests a resolved claim outcome
func (rs *ReliabilityScorer) RecordOutcome(outcome *models.Outcome) {
	rs.mu.Lock()
	defer rs.mu.Unlock()

	key := outcome.AgentID + ":" + outcome.Payer
	rs.outcomes[key] = append(rs.outcomes[key], scoredOutcome{
		Correct:    outcome.WasCorrect,
		Confidence: outcome.AgentConfidence,
		Timestamp:  outcome.ResolvedAt,
	})

	log.Debug().
		Str("key", key).
		Bool("correct", outcome.WasCorrect).
		Float64("confidence", outcome.AgentConfidence).
		Msg("Outcome recorded in reliability scorer")
}

// GetProfile builds the full reliability profile for an agent
func (rs *ReliabilityScorer) GetProfile(agentID string) *models.ReliabilityProfile {
	rs.mu.RLock()
	defer rs.mu.RUnlock()

	profile := &models.ReliabilityProfile{
		AgentID:              agentID,
		ConfidenceRealityGap: make(map[string]models.ConfidenceBand),
		LastUpdated:          time.Now(),
	}

	// Aggregate across all payers for this agent
	var allOutcomes []scoredOutcome
	payerSet := map[string]bool{}

	for key, outcomes := range rs.outcomes {
		agentKey := agentID + ":"
		if len(key) > len(agentKey) && key[:len(agentKey)] == agentKey {
			payer := key[len(agentKey):]
			payerSet[payer] = true
			allOutcomes = append(allOutcomes, outcomes...)
		}
	}

	// Overall accuracy
	profile.OverallAccuracy = accuracy(allOutcomes)

	// ECE (Expected Calibration Error)
	profile.CalibrationError = rs.computeECE(allOutcomes)

	// Per-payer breakdown
	for payer := range payerSet {
		key := agentID + ":" + payer
		payerOutcomes := rs.outcomes[key]

		weeklyAcc := rs.weeklyAccuracy(payerOutcomes, 8)
		drift := rs.detectDrift(weeklyAcc)
		trend := rs.trend(weeklyAcc)

		if drift {
			profile.DriftDetected = append(profile.DriftDetected, payer)
		}

		profile.ByPayer = append(profile.ByPayer, models.PayerProfile{
			Payer:          payer,
			Accuracy:       accuracy(payerOutcomes),
			SampleSize:     len(payerOutcomes),
			Trend:          trend,
			DriftFlag:      drift,
			WeeklyAccuracy: weeklyAcc,
		})
	}

	// Confidence reality gap — the key calibration insight
	profile.ConfidenceRealityGap = rs.computeConfidenceGap(allOutcomes)

	log.Info().
		Str("agent_id", agentID).
		Float64("overall_accuracy", profile.OverallAccuracy).
		Float64("ece", profile.CalibrationError).
		Int("drift_payers", len(profile.DriftDetected)).
		Msg("Reliability profile computed")

	return profile
}

// GetPayerScore returns the reliability score for a specific agent × payer combo
func (rs *ReliabilityScorer) GetPayerScore(agentID, payer string) float64 {
	rs.mu.RLock()
	defer rs.mu.RUnlock()

	key := agentID + ":" + payer
	outcomes, ok := rs.outcomes[key]
	if !ok || len(outcomes) < MinSampleForDrift {
		return 0.70 // default when insufficient data — conservative
	}
	return accuracy(outcomes)
}

// IsDrifting returns true if a payer has significant accuracy decline
func (rs *ReliabilityScorer) IsDrifting(agentID, payer string) bool {
	rs.mu.RLock()
	defer rs.mu.RUnlock()

	key := agentID + ":" + payer
	outcomes := rs.outcomes[key]
	if len(outcomes) < MinSampleForDrift {
		return false
	}

	weekly := rs.weeklyAccuracy(outcomes, 4)
	return rs.detectDrift(weekly)
}

// ─── internal helpers ────────────────────────

func accuracy(outcomes []scoredOutcome) float64 {
	if len(outcomes) == 0 {
		return 0
	}
	correct := 0
	for _, o := range outcomes {
		if o.Correct {
			correct++
		}
	}
	return float64(correct) / float64(len(outcomes))
}

// weeklyAccuracy returns accuracy per week for the last n weeks
func (rs *ReliabilityScorer) weeklyAccuracy(outcomes []scoredOutcome, weeks int) []float64 {
	result := make([]float64, weeks)
	now := time.Now()

	for w := 0; w < weeks; w++ {
		start := now.AddDate(0, 0, -(w+1)*7)
		end := now.AddDate(0, 0, -w*7)

		var weekOutcomes []scoredOutcome
		for _, o := range outcomes {
			if o.Timestamp.After(start) && o.Timestamp.Before(end) {
				weekOutcomes = append(weekOutcomes, o)
			}
		}

		if len(weekOutcomes) > 0 {
			result[weeks-1-w] = accuracy(weekOutcomes)
		} else {
			result[weeks-1-w] = -1 // no data marker
		}
	}
	return result
}

// detectDrift compares latest week vs 4-week average
func (rs *ReliabilityScorer) detectDrift(weekly []float64) bool {
	if len(weekly) < 2 {
		return false
	}

	// Get most recent valid week
	latest := -1.0
	for i := len(weekly) - 1; i >= 0; i-- {
		if weekly[i] >= 0 {
			latest = weekly[i]
			break
		}
	}

	if latest < 0 {
		return false
	}

	// Get average of earlier weeks
	var validWeeks []float64
	for _, w := range weekly[:len(weekly)-1] {
		if w >= 0 {
			validWeeks = append(validWeeks, w)
		}
	}

	if len(validWeeks) == 0 {
		return false
	}

	avg := 0.0
	for _, w := range validWeeks {
		avg += w
	}
	avg /= float64(len(validWeeks))

	drop := avg - latest
	return drop >= DriftThreshold
}

func (rs *ReliabilityScorer) trend(weekly []float64) string {
	if len(weekly) < 2 {
		return "stable"
	}

	var valid []float64
	for _, w := range weekly {
		if w >= 0 {
			valid = append(valid, w)
		}
	}
	if len(valid) < 2 {
		return "stable"
	}

	recent := valid[len(valid)-1]
	earlier := valid[0]
	diff := recent - earlier

	switch {
	case diff > 0.05:
		return "improving"
	case diff < -0.05:
		return "declining"
	default:
		return "stable"
	}
}

// computeECE — Expected Calibration Error
// Groups predictions by confidence band and measures how far stated confidence
// deviates from actual accuracy. This is the core of Andrew's calibration research.
func (rs *ReliabilityScorer) computeECE(outcomes []scoredOutcome) float64 {
	if len(outcomes) == 0 {
		return 0
	}

	bands := []struct{ low, high float64 }{
		{0.50, 0.65},
		{0.65, 0.75},
		{0.75, 0.85},
		{0.85, 1.00},
	}

	ece := 0.0
	for _, band := range bands {
		var bandOutcomes []scoredOutcome
		for _, o := range outcomes {
			if o.Confidence >= band.low && o.Confidence < band.high {
				bandOutcomes = append(bandOutcomes, o)
			}
		}
		if len(bandOutcomes) == 0 {
			continue
		}

		avgConf := 0.0
		for _, o := range bandOutcomes {
			avgConf += o.Confidence
		}
		avgConf /= float64(len(bandOutcomes))

		acc := accuracy(bandOutcomes)
		weight := float64(len(bandOutcomes)) / float64(len(outcomes))
		ece += weight * math.Abs(avgConf-acc)
	}

	return ece
}

func (rs *ReliabilityScorer) computeConfidenceGap(outcomes []scoredOutcome) map[string]models.ConfidenceBand {
	bands := map[string]struct{ low, high float64 }{
		"0.50-0.65": {0.50, 0.65},
		"0.65-0.75": {0.65, 0.75},
		"0.75-0.85": {0.75, 0.85},
		"0.85-1.00": {0.85, 1.00},
	}

	result := map[string]models.ConfidenceBand{}

	for label, band := range bands {
		var bandOutcomes []scoredOutcome
		for _, o := range outcomes {
			if o.Confidence >= band.low && o.Confidence < band.high {
				bandOutcomes = append(bandOutcomes, o)
			}
		}
		if len(bandOutcomes) == 0 {
			continue
		}

		avgConf := 0.0
		for _, o := range bandOutcomes {
			avgConf += o.Confidence
		}

		result[label] = models.ConfidenceBand{
			Band:           label,
			StatedMean:     avgConf / float64(len(bandOutcomes)),
			ActualAccuracy: accuracy(bandOutcomes),
		}
	}

	return result
}
