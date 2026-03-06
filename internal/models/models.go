package models

import "time"

// ─────────────────────────────────────────────
// AGENT DECISION — what MedAgent emits
// ─────────────────────────────────────────────

type DecisionType string

const (
	DecisionPriorAuth     DecisionType = "prior_auth"
	DecisionDenialPredict DecisionType = "denial_prediction"
	DecisionEscalation    DecisionType = "escalation_routing"
)

type Decision struct {
	ID             string            `json:"id"`
	AgentID        string            `json:"agent_id"`
	DecisionType   DecisionType      `json:"decision_type"`
	PatientID      string            `json:"patient_id"`   // anonymised
	Payer          string            `json:"payer"`
	ProcedureCode  string            `json:"procedure_code"`
	Prediction     string            `json:"prediction"`  // DENY | APPROVE | ESCALATE
	Confidence     float64           `json:"confidence"`  // 0.0–1.0 as stated by MedAgent
	Reasoning      string            `json:"reasoning"`
	ProposedAction string            `json:"proposed_action"`
	RetrievedDocs  []RetrievedSignal `json:"retrieved_signals"`
	Timestamp      time.Time         `json:"timestamp"`
}

// ─────────────────────────────────────────────
// RETRIEVED SIGNAL — evidence MedAgent fetched
// ─────────────────────────────────────────────

type RetrievedSignal struct {
	SignalType      string    `json:"signal_type"`      // payer_policy | patient_history | step_therapy_docs
	Source          string    `json:"source"`           // vector_db | live_api | cache
	VersionDate     time.Time `json:"version_date"`     // when was this data last updated
	PagesRequested  int       `json:"pages_requested"`  // 0 if N/A
	PagesReturned   int       `json:"pages_returned"`   // 0 if N/A
	TimedOut        bool      `json:"timed_out"`
	WeightApplied   float64   `json:"weight_applied"`   // what weight did MedAgent use
	BaselineWeight  float64   `json:"baseline_weight"`  // expected weight for this context
	RawContent      string    `json:"raw_content,omitempty"`
}

// ─────────────────────────────────────────────
// SIGNAL FIDELITY AUDIT — SENTINEL's X-ray
// ─────────────────────────────────────────────

type FidelityFlag string

const (
	FlagStalePolicy         FidelityFlag = "STALE_POLICY"         // policy older than 30 days
	FlagIncompleteRetrieval FidelityFlag = "INCOMPLETE_RETRIEVAL"  // pages_returned < pages_requested
	FlagWeightDivergence    FidelityFlag = "WEIGHT_DIVERGENCE"     // weight deviated >30% from baseline
	FlagTimeoutOnCritical   FidelityFlag = "TIMEOUT_ON_CRITICAL"   // timed out on a required signal
	FlagMissingSignal       FidelityFlag = "MISSING_SIGNAL"        // expected signal not retrieved at all
	FlagConfidenceMismatch  FidelityFlag = "CONFIDENCE_MISMATCH"   // stated confidence >> historical accuracy
)

type SignalAudit struct {
	SignalType   string         `json:"signal_type"`
	Flags        []FidelityFlag `json:"flags"`
	Severity     string         `json:"severity"` // LOW | MEDIUM | HIGH | CRITICAL
	Detail       string         `json:"detail"`
	SuggestedFix string         `json:"suggested_fix"`
}

type FidelityReport struct {
	DecisionID      string        `json:"decision_id"`
	OverallScore    float64       `json:"overall_score"` // 0.0 (broken) – 1.0 (perfect)
	Audits          []SignalAudit `json:"audits"`
	CriticalFlags   int           `json:"critical_flags"`
	SuggestedAction string        `json:"suggested_action"`
	Timestamp       time.Time     `json:"timestamp"`
}

// ─────────────────────────────────────────────
// REASONING PATTERN — learned failure signatures
// ─────────────────────────────────────────────

type ReasoningPattern struct {
	ID              string    `json:"id"`              // e.g. "pattern_delta"
	Name            string    `json:"name"`
	Description     string    `json:"description"`
	Flags           []FidelityFlag `json:"trigger_flags"` // which flag combo defines this pattern
	Accuracy        float64   `json:"accuracy"`        // historical accuracy when this pattern fires
	Occurrences     int       `json:"occurrences"`
	FailureMode     string    `json:"failure_mode"`    // human readable root cause
	MissingEvidence string    `json:"missing_evidence"`
	Payers          []string  `json:"payers"`          // which payers this pattern is most common on
	LastSeen        time.Time `json:"last_seen"`
	Severity        string    `json:"severity"`        // LOW | MEDIUM | HIGH | CRITICAL
}

// ─────────────────────────────────────────────
// AUTHORITY LEVEL — what SENTINEL grants
// ─────────────────────────────────────────────

type AuthorityLevel int

const (
	AuthorityQuarantine    AuthorityLevel = 0 // all decisions → human, drift detected
	AuthorityHumanRequired AuthorityLevel = 1 // reliability < 0.65
	AuthorityActAndNotify  AuthorityLevel = 2 // reliability 0.65–0.85
	AuthorityFullAuto      AuthorityLevel = 3 // reliability > 0.85
)

func (a AuthorityLevel) String() string {
	switch a {
	case AuthorityQuarantine:
		return "QUARANTINE"
	case AuthorityHumanRequired:
		return "HUMAN_REQUIRED"
	case AuthorityActAndNotify:
		return "ACT_AND_NOTIFY"
	case AuthorityFullAuto:
		return "FULL_AUTONOMY"
	default:
		return "UNKNOWN"
	}
}

// ─────────────────────────────────────────────
// SENTINEL VERDICT — the gate decision
// ─────────────────────────────────────────────

type Verdict struct {
	DecisionID       string         `json:"decision_id"`
	Authority        AuthorityLevel `json:"authority"`
	AuthorityLabel   string         `json:"authority_label"`
	Block            bool           `json:"block"`           // true = do not execute autonomously
	PatternDetected  *ReasoningPattern `json:"pattern_detected,omitempty"`
	FidelityReport   *FidelityReport   `json:"fidelity_report,omitempty"`
	ReliabilityScore float64        `json:"reliability_score"`
	Rationale        string         `json:"rationale"`
	SuggestedFix     string         `json:"suggested_fix"`
	EscalateTo       string         `json:"escalate_to,omitempty"` // "cleric" | "human" | "none"
	Timestamp        time.Time      `json:"timestamp"`
}

// ─────────────────────────────────────────────
// OUTCOME — ground truth when claim resolves
// ─────────────────────────────────────────────

type OutcomeResult string

const (
	OutcomeApproved OutcomeResult = "APPROVED"
	OutcomeDenied   OutcomeResult = "DENIED"
	OutcomePending  OutcomeResult = "PENDING"
)

type Outcome struct {
	DecisionID     string        `json:"decision_id"`
	AgentID        string        `json:"agent_id"`
	Payer          string        `json:"payer"`
	DecisionType   DecisionType  `json:"decision_type"`
	AgentPredicted string        `json:"agent_predicted"`
	AgentConfidence float64      `json:"agent_confidence"`
	ActualOutcome  OutcomeResult `json:"actual_outcome"`
	WasCorrect     bool          `json:"was_correct"`
	SentinelBlocked bool         `json:"sentinel_blocked"`
	SentinelCorrect bool         `json:"sentinel_correct"` // if blocked, was blocking right?
	ResolvedAt     time.Time     `json:"resolved_at"`
}

// ─────────────────────────────────────────────
// RELIABILITY PROFILE — per agent, per context
// ─────────────────────────────────────────────

type PayerProfile struct {
	Payer        string    `json:"payer"`
	Accuracy     float64   `json:"accuracy"`
	SampleSize   int       `json:"sample_size"`
	Trend        string    `json:"trend"`   // "stable" | "improving" | "declining"
	DriftFlag    bool      `json:"drift_flag"`
	WeeklyAccuracy []float64 `json:"weekly_accuracy"` // last 8 weeks
}

type ReliabilityProfile struct {
	AgentID              string         `json:"agent_id"`
	OverallAccuracy      float64        `json:"overall_accuracy"`
	CalibrationError     float64        `json:"calibration_error"` // ECE
	ByPayer              []PayerProfile `json:"by_payer"`
	DriftDetected        []string       `json:"drift_detected"`    // payers with drift
	ConfidenceRealityGap map[string]ConfidenceBand `json:"confidence_reality_gap"`
	LastUpdated          time.Time      `json:"last_updated"`
}

type ConfidenceBand struct {
	Band         string  `json:"band"`           // e.g. "0.85-1.0"
	StatedMean   float64 `json:"stated_mean"`
	ActualAccuracy float64 `json:"actual_accuracy"`
}
