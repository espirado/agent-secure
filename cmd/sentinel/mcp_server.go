package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/rs/zerolog/log"

	"github.com/sentinel-ai/sentinel/internal/fingerprint"
	"github.com/sentinel-ai/sentinel/internal/gate"
	ddclient "github.com/sentinel-ai/sentinel/internal/integrations/datadog"
	"github.com/sentinel-ai/sentinel/internal/models"
	"github.com/sentinel-ai/sentinel/internal/scoring"
)

// startMCPServer launches SENTINEL as an MCP server.
//
// Supports two transports (set MCP_TRANSPORT env var):
//   - "streamable_http" (default) — Streamable HTTP, compatible with agentgateway
//   - "sse"                       — Server-Sent Events (legacy)
//
// This is the thin Datadog integration layer: any MCP client (Datadog Bits AI,
// Claude Desktop, agentgateway, a custom orchestrator) can pull agent decisions
// from Datadog, evaluate them through SENTINEL's pipeline, and get reliability
// data back.
//
//   Port: defaults to 8081 (set MCP_PORT to override)
//
// When using agentgateway:
//   agentgateway proxies MCP clients → localhost:3000 → SENTINEL MCP:8081
//   This adds RBAC, audit logging, session management, and observability.
//
// Tools exposed:
//   sentinel_evaluate        — run a decision through the full pipeline
//   sentinel_pull_decisions  — pull recent agent spans/events from Datadog
//   sentinel_reliability     — get per-agent, per-payer reliability profile
//   sentinel_patterns        — list known failure signatures
func startMCPServer(
	port string,
	gatekeeper *gate.AuthorityGate,
	scorer *scoring.ReliabilityScorer,
	patterns *fingerprint.PatternLibrary,
	dd *ddclient.Client,
) error {
	transport := os.Getenv("MCP_TRANSPORT")
	if transport == "" {
		transport = "streamable_http"
	}
	log.Info().Str("port", port).Str("transport", transport).Msg("🔌 Starting SENTINEL MCP server")

	s := server.NewMCPServer(
		"sentinel",
		"1.0.0",
		server.WithToolCapabilities(true),
	)

	// ── Tool 1: Evaluate Decision ────────────────────────────
	// The core SENTINEL loop exposed as an MCP tool.
	// Agent decision in → Verdict out (with authority level, pattern, fidelity).
	evaluateTool := mcp.NewTool("sentinel_evaluate",
		mcp.WithDescription(
			"Evaluate an AI agent decision through SENTINEL's full pipeline: "+
				"signal fidelity audit → pattern classification → reliability scoring → authority gate. "+
				"Returns a verdict: FULL_AUTONOMY, ACT_AND_NOTIFY, HUMAN_REQUIRED, or QUARANTINE."),
		mcp.WithString("decision_json",
			mcp.Required(),
			mcp.Description("JSON-encoded Decision object with id, agent_id, payer, confidence, retrieved_signals, etc.")),
	)

	s.AddTool(evaluateTool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		raw := req.GetString("decision_json", "")

		var decision models.Decision
		if err := json.Unmarshal([]byte(raw), &decision); err != nil {
			return mcp.NewToolResultText(fmt.Sprintf("❌ Invalid decision JSON: %v", err)), nil
		}

		verdict := gatekeeper.Evaluate(&decision)

		// Async: emit verdict to Datadog for full audit trail
		go func() {
			if err := dd.EmitVerdict(verdict); err != nil {
				log.Error().Err(err).Msg("MCP → Datadog emit failed")
			}
		}()

		out, _ := json.MarshalIndent(verdict, "", "  ")
		return mcp.NewToolResultText(string(out)), nil
	})

	// ── Tool 2: Pull Decisions from Datadog ──────────────────
	// "Pull a small set of agent spans/events" — the thin DD integration.
	// Retrieves recent MedAgent decisions from Datadog's event stream.
	pullTool := mcp.NewTool("sentinel_pull_decisions",
		mcp.WithDescription(
			"Pull recent MedAgent decision events from Datadog event stream. "+
				"Returns raw decisions that can be piped into sentinel_evaluate for enrichment."),
		mcp.WithString("agent_id",
			mcp.Required(),
			mcp.Description("Agent ID to pull decisions for, e.g. 'medagent-v2'")),
		mcp.WithNumber("hours_back",
			mcp.Description("Hours of history to search. Default: 24")),
	)

	s.AddTool(pullTool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		agentID := req.GetString("agent_id", "")

		hoursBack := req.GetFloat("hours_back", 24.0)

		since := time.Now().Add(-time.Duration(hoursBack) * time.Hour)
		decisions, err := dd.PullDecisions(agentID, since)
		if err != nil {
			return mcp.NewToolResultText(fmt.Sprintf("❌ Datadog pull failed: %v", err)), nil
		}

		out, _ := json.MarshalIndent(decisions, "", "  ")
		return mcp.NewToolResultText(fmt.Sprintf("Pulled %d decisions:\n%s", len(decisions), string(out))), nil
	})

	// ── Tool 3: Reliability Profile ──────────────────────────
	// Dashboard-grade reliability data as an MCP tool.
	// Returns overall accuracy, per-payer breakdown, drift detection, ECE.
	reliabilityTool := mcp.NewTool("sentinel_reliability",
		mcp.WithDescription(
			"Get full reliability profile for an agent: overall accuracy, per-payer breakdown, "+
				"drift detection, ECE calibration error, and confidence-reality gap analysis."),
		mcp.WithString("agent_id",
			mcp.Required(),
			mcp.Description("Agent ID, e.g. 'medagent-v2'")),
	)

	s.AddTool(reliabilityTool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		agentID := req.GetString("agent_id", "")
		profile := scorer.GetProfile(agentID)

		out, _ := json.MarshalIndent(profile, "", "  ")
		return mcp.NewToolResultText(string(out)), nil
	})

	// ── Tool 4: Pattern Library ──────────────────────────────
	// Surfaces SENTINEL's learned failure signatures for investigation.
	patternsTool := mcp.NewTool("sentinel_patterns",
		mcp.WithDescription(
			"List all known reasoning failure patterns in SENTINEL's pattern library. "+
				"Each pattern has historical accuracy, trigger flags, failure mode, and affected payers."),
	)

	s.AddTool(patternsTool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		all := patterns.GetAll()
		out, _ := json.MarshalIndent(all, "", "  ")
		return mcp.NewToolResultText(string(out)), nil
	})

	// ── Launch Transport ─────────────────────────────────────
	switch transport {
	case "streamable_http":
		// Streamable HTTP — compatible with agentgateway
		// agentgateway connects to this endpoint via:
		//   targets:
		//     - name: sentinel
		//       mcp:
		//         host: http://localhost:8081/mcp
		httpServer := server.NewStreamableHTTPServer(s,
			server.WithEndpointPath("/mcp"),
		)

		log.Info().
			Str("port", port).
			Str("transport", "streamable_http").
			Str("endpoint", fmt.Sprintf("http://localhost:%s/mcp", port)).
			Int("tools", 4).
			Msg("SENTINEL MCP server ready — connect via Streamable HTTP or agentgateway")

		return httpServer.Start(":" + port)

	default:
		// SSE (legacy) — direct client connections
		sseServer := server.NewSSEServer(s,
			server.WithBaseURL(fmt.Sprintf("http://localhost:%s", port)),
		)

		log.Info().
			Str("port", port).
			Str("transport", "sse").
			Int("tools", 4).
			Msg("SENTINEL MCP server ready — connect via SSE")

		return sseServer.Start(":" + port)
	}
}
