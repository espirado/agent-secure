package main

import (
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"github.com/sentinel-ai/sentinel/internal/fingerprint"
	"github.com/sentinel-ai/sentinel/internal/gate"
	ddclient "github.com/sentinel-ai/sentinel/internal/integrations/datadog"
	btclient "github.com/sentinel-ai/sentinel/internal/integrations/braintrust"
	clericclient "github.com/sentinel-ai/sentinel/internal/integrations/cleric"
	elclient "github.com/sentinel-ai/sentinel/internal/integrations/elevenlabs"
	"github.com/sentinel-ai/sentinel/internal/models"
	"github.com/sentinel-ai/sentinel/internal/scoring"
)

func main() {
	// ── Setup ─────────────────────────────────
	godotenv.Load()

	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stdout, TimeFormat: time.RFC3339}).
		With().Str("service", "sentinel").Logger()

	log.Info().Msg("🛡  SENTINEL starting — AI Reasoning Observatory")

	// ── Initialise SENTINEL core components ───
	scorer   := scoring.NewReliabilityScorer()
	auditor  := scoring.NewSignalFidelityAuditor(scorer)
	patterns := fingerprint.NewPatternLibrary()
	gatekeeper := gate.NewAuthorityGate(scorer, auditor, patterns)

	// ── Initialise sponsor integrations ───────
	dd      := ddclient.NewClient()
	bt      := btclient.NewClient()
	cleric  := clericclient.NewClient()
	el      := elclient.NewClient()

	// ── Seed demo data if requested ──────────
	if os.Getenv("SEED_DEMO_DATA") == "true" {
		seedDemoOutcomes(scorer)
	}

	// ── HTTP API ──────────────────────────────
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(logMiddleware())
	// CORS — allow dashboard and other local origins
	r.Use(func(c *gin.Context) {
		c.Header("Access-Control-Allow-Origin", "*")
		c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Content-Type, Authorization")
		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	})

	// Health
	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok", "service": "sentinel"})
	})

	// ── Core SENTINEL Endpoint ─────────────────
	// MedAgent calls this BEFORE executing any decision
	// This is the pre-decision interceptor
	r.POST("/api/v1/evaluate", func(c *gin.Context) {
		var decision models.Decision
		if err := c.ShouldBindJSON(&decision); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		// THE CORE LOOP
		verdict := gatekeeper.Evaluate(&decision)

		// Async: log to Datadog and Braintrust
		go func() {
			if err := dd.EmitVerdict(verdict); err != nil {
				log.Error().Err(err).Msg("Failed to emit verdict to Datadog")
			}
			if _, err := bt.LogEvaluation(&decision, verdict); err != nil {
				log.Error().Err(err).Msg("Failed to log to Braintrust")
			}
		}()

		// If blocked — create Cleric incident immediately
		if verdict.Block && verdict.EscalateTo == "cleric" {
			go func() {
				incidentID, err := cleric.CreateIncident(&decision, verdict)
				if err != nil {
					log.Error().Err(err).Msg("Failed to create Cleric incident")
					return
				}
				log.Info().Str("incident_id", incidentID).Msg("Cleric incident created")
			}()
		}

		c.JSON(http.StatusOK, verdict)
	})

	// ── Outcome Ingestion ──────────────────────
	// Called when a claim resolves (days later) — closes the learning loop
	r.POST("/api/v1/outcome", func(c *gin.Context) {
		var outcome models.Outcome
		if err := c.ShouldBindJSON(&outcome); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		scorer.RecordOutcome(&outcome)
		patterns.RecordOutcomeForDecision(outcome.DecisionID, outcome.WasCorrect) // update pattern accuracy

		// Emit updated reliability metrics to Datadog
		go func() {
			profile := scorer.GetProfile(outcome.AgentID)
			if err := dd.EmitReliabilityMetrics(profile); err != nil {
				log.Error().Err(err).Msg("Failed to emit reliability metrics")
			}
		}()

		c.JSON(http.StatusOK, gin.H{"status": "outcome_recorded"})
	})

	// ── Reliability Profile ────────────────────
	// Dashboard endpoint — Lightdash hits this for visualisation
	r.GET("/api/v1/reliability/:agent_id", func(c *gin.Context) {
		agentID := c.Param("agent_id")
		profile := scorer.GetProfile(agentID)
		c.JSON(http.StatusOK, profile)
	})

	// ── Pattern Library ────────────────────────
	r.GET("/api/v1/patterns", func(c *gin.Context) {
		c.JSON(http.StatusOK, patterns.GetAll())
	})

	// ── Lightdash Dashboard ────────────────────
	// Structured endpoints for Lightdash: reliability heatmap, drift chart, blocked counter
	r.GET("/api/v1/dashboard/overview", func(c *gin.Context) {
		agentID := c.DefaultQuery("agent_id", "medagent-v2")
		profile := scorer.GetProfile(agentID)
		allPatterns := patterns.GetAll()

		c.JSON(http.StatusOK, gin.H{
			"agent_id":          agentID,
			"overall_accuracy":  profile.OverallAccuracy,
			"calibration_error": profile.CalibrationError,
			"drift_payers":      profile.DriftDetected,
			"payer_count":       len(profile.ByPayer),
			"blocked_payers":    len(profile.DriftDetected),
			"pattern_count":     len(allPatterns),
			"payers":            profile.ByPayer,
			"confidence_gap":    profile.ConfidenceRealityGap,
			"last_updated":      profile.LastUpdated,
		})
	})

	// Time-series data for Lightdash heatmap: payer × week × accuracy
	r.GET("/api/v1/dashboard/timeseries", func(c *gin.Context) {
		agentID := c.DefaultQuery("agent_id", "medagent-v2")
		profile := scorer.GetProfile(agentID)

		var series []gin.H
		now := time.Now()
		for _, payer := range profile.ByPayer {
			for i, acc := range payer.WeeklyAccuracy {
				if acc < 0 {
					continue
				}
				weeksBack := len(payer.WeeklyAccuracy) - 1 - i
				weekStart := now.AddDate(0, 0, -weeksBack*7)
				year, week := weekStart.ISOWeek()
				series = append(series, gin.H{
					"payer":      payer.Payer,
					"week":       fmt.Sprintf("%d-W%02d", year, week),
					"accuracy":   acc,
					"drift_flag": payer.DriftFlag,
					"trend":      payer.Trend,
				})
			}
		}

		c.JSON(http.StatusOK, gin.H{
			"agent_id": agentID,
			"series":   series,
		})
	})

	// ── Morning Brief ──────────────────────────
	r.POST("/api/v1/brief", func(c *gin.Context) {
		var req struct {
			AgentID string                    `json:"agent_id"`
			Stats   elclient.TodayStats       `json:"stats"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		profile := scorer.GetProfile(req.AgentID)
		audio, script, err := el.GenerateMorningBrief(profile, req.Stats)
		if err != nil {
			log.Error().Err(err).Msg("Failed to generate morning brief")
			// Return script even if audio fails — demo fallback
			c.JSON(http.StatusOK, gin.H{"script": script, "audio_error": err.Error()})
			return
		}

		_ = audio
		// In prod: save audio to GCS and return URL
		c.JSON(http.StatusOK, gin.H{
			"script":      script,
			"audio_bytes": len(audio),
			"status":      "generated",
		})
	})

	// ── Setup Datadog Drift Monitor ────────────
	// Call once during hackathon setup to create the monitor in Datadog
	r.POST("/api/v1/setup/drift-monitor", func(c *gin.Context) {
		var req struct {
			AgentID string `json:"agent_id"`
			Payer   string `json:"payer"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		if err := dd.CreateDriftMonitor(req.AgentID, req.Payer); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		c.JSON(http.StatusOK, gin.H{"status": "drift_monitor_created"})
	})

	// ── Start MCP Server (Streamable HTTP or SSE transport) ──
	// When using agentgateway, clients connect to agentgateway:3000
	// which proxies to SENTINEL MCP on this port with RBAC + audit.
	mcpPort := os.Getenv("MCP_PORT")
	if mcpPort == "" {
		mcpPort = "8081"
	}
	mcpTransport := os.Getenv("MCP_TRANSPORT")
	if mcpTransport == "" {
		mcpTransport = "streamable_http"
	}
	go func() {
		if err := startMCPServer(mcpPort, gatekeeper, scorer, patterns, dd); err != nil {
			log.Error().Err(err).Msg("MCP server stopped")
		}
	}()

	// Log agentgateway integration hint
	log.Info().
		Str("mcp_port", mcpPort).
		Str("mcp_transport", mcpTransport).
		Msg("💡 To run with agentgateway: agentgateway -f agentgateway.yaml")

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	log.Info().Str("port", port).Msg("SENTINEL API ready")
	r.Run(":" + port)
}

// ── Demo data seeder ───────────────────────────────────────────────────
// Loads 60 days of synthetic outcomes so the dashboard has data at demo time

func seedDemoOutcomes(scorer *scoring.ReliabilityScorer) {
	log.Info().Msg("Seeding demo outcomes for dashboard population")

	// Aetna — declining accuracy (the drift story)
	aetnaOutcomes := []struct {
		correct    bool
		confidence float64
		daysAgo    int
	}{
		// Month 1 — good accuracy (84%)
		{true, 0.87, 60}, {true, 0.82, 58}, {true, 0.91, 56}, {false, 0.78, 54},
		{true, 0.85, 52}, {true, 0.88, 50}, {false, 0.79, 48}, {true, 0.83, 46},
		{true, 0.86, 44}, {true, 0.81, 42}, {true, 0.89, 40}, {false, 0.76, 38},
		// Month 2 — declining (policy update happened around day 30)
		{false, 0.88, 35}, {true, 0.82, 33}, {false, 0.91, 31}, {false, 0.87, 29},
		{false, 0.84, 27}, {true, 0.79, 25}, {false, 0.89, 23}, {false, 0.86, 21},
		{false, 0.91, 18}, {false, 0.88, 15}, {true, 0.82, 12}, {false, 0.89, 10},
		{false, 0.87, 7}, {false, 0.91, 5}, {false, 0.85, 3}, {false, 0.89, 1},
	}

	for _, o := range aetnaOutcomes {
		scorer.RecordOutcome(&models.Outcome{
			AgentID:         "medagent-v2",
			Payer:           "Aetna",
			DecisionType:    models.DecisionPriorAuth,
			WasCorrect:      o.correct,
			AgentConfidence: o.confidence,
			ResolvedAt:      time.Now().AddDate(0, 0, -o.daysAgo),
		})
	}

	// UnitedHealthcare — stable high accuracy (91%)
	for i := 0; i < 40; i++ {
		correct := i%10 != 0 // 90% accuracy
		scorer.RecordOutcome(&models.Outcome{
			AgentID:         "medagent-v2",
			Payer:           "UnitedHealthcare",
			DecisionType:    models.DecisionPriorAuth,
			WasCorrect:      correct,
			AgentConfidence: 0.85 + float64(i%10)*0.01,
			ResolvedAt:      time.Now().AddDate(0, 0, -(60 - i)),
		})
	}

	log.Info().Msg("Demo data seeded — dashboard ready")
}

func logMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		log.Info().
			Str("method", c.Request.Method).
			Str("path", c.Request.URL.Path).
			Int("status", c.Writer.Status()).
			Dur("latency", time.Since(start)).
			Msg("request")
	}
}
