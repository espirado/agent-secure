# SENTINEL — AI Reasoning Observatory

A governance layer for AI agents operating over MCP. SENTINEL audits agent reasoning quality — checking whether evidence was complete and current before a decision commits — and surfaces known failure patterns so operators can intervene early.

Built for the **[Secure & Govern MCP](https://aihackathon.dev/)** track of the AI Agent & MCP Hackathon.

---

## What It Does

SENTINEL sits behind **agentgateway** and exposes four MCP tools that any MCP client can call:

| MCP Tool | Purpose |
|----------|---------|
| `sentinel_evaluate` | Audit a pending agent decision — score signal fidelity, match failure patterns, assign authority level |
| `sentinel_reliability` | Return the historical reliability profile for a given agent |
| `sentinel_patterns` | List known failure signatures with accuracy stats |
| `sentinel_pull_decisions` | Pull agent decision events from Datadog for analysis |

When `sentinel_evaluate` runs, it passes the decision through four stages:

1. **Signal Fidelity Auditor** — Was the evidence complete and current? (stale policy, incomplete docs, timed-out lookups)
2. **Pattern Fingerprinter** — Does this match a known failure signature? (e.g. stale+incomplete → historically 23% accuracy)
3. **Reliability Scorer** — What's the agent's historical accuracy for this context? Is calibration drifting?
4. **Authority Gate** — Based on all of the above, assign: `FULL_AUTO` | `ACT_AND_NOTIFY` | `HUMAN_REQUIRED` | `QUARANTINE`

---

## Architecture

```
                        ┌─────────────────────────┐
                        │  agentgateway (:3000)    │
                        │  ┌───────────────────┐   │
  MCP Clients           │  │ RBAC (CEL rules)  │   │
  (Claude, GPT,    ────►│  │ Session Mgmt      │   │
   Custom Agents)       │  │ Audit Logging     │   │
                        │  │ CORS              │   │
                        │  └────────┬──────────┘   │
                        └───────────┼──────────────┘
                                    │ Streamable HTTP
                                    ▼
                        ┌─────────────────────────┐
                        │  SENTINEL MCP (:8081)    │
                        │  4 MCP Tools             │
                        └───────────┬──────────────┘
                                    │
                  MedAgent Decision  │
                         │          │
                         ▼          ▼
                ┌─────────────────────┐
                │  Signal Fidelity    │  ← Was evidence complete and current?
                │  Auditor            │     Stale policy? Incomplete docs? Timed out?
                └─────────┬───────────┘
                          │
                          ▼
                ┌─────────────────────┐
                │  Pattern            │  ← Which failure signature is this?
                │  Fingerprinter      │     Pattern Delta: stale+incomplete → 23% accuracy
                └─────────┬───────────┘
                          │
                          ▼
                ┌─────────────────────┐
                │  Reliability        │  ← What's the historical accuracy for this payer?
                │  Scorer             │     ECE calibration error? Drift detected?
                └─────────┬───────────┘
                          │
                          ▼
                ┌─────────────────────┐
                │  Authority Gate     │  ← FULL_AUTO | ACT_AND_NOTIFY | HUMAN_REQUIRED | QUARANTINE
                └─────────┬───────────┘
                          │
                    ┌─────┴──────┐
                    ▼            ▼
                 Datadog      Cleric
                 Braintrust   ElevenLabs
```

### agentgateway Integration

```
┌──────────────────────────────────────────────────────────────┐
│  agentgateway — MCP Security & Governance Layer              │
│                                                              │
│  ┌──────────┐   ┌──────────────────┐   ┌────────────────┐  │
│  │ CORS     │   │ MCP Authorization│   │ Audit Logging  │  │
│  │ Policy   │   │ (CEL Rules)      │   │ (all tool      │  │
│  │          │   │                  │   │  invocations)  │  │
│  │ Allow *  │   │ patterns: public │   │                │  │
│  │          │   │ evaluate: auth   │   │ Who called     │  │
│  └──────────┘   │ pull: operator   │   │ what, when     │  │
│                 └──────────────────┘   └────────────────┘  │
│                                                              │
│  MCP Tools (via SENTINEL):                                   │
│    sentinel_evaluate       — Pre-decision reasoning audit    │
│    sentinel_pull_decisions — Pull agent events from Datadog  │
│    sentinel_reliability    — Agent reliability profile       │
│    sentinel_patterns       — Known failure signatures        │
└──────────────────────────────────────────────────────────────┘
```

---

## Integrations

| Service | How SENTINEL Uses It |
|---------|---------------------|
| **agentgateway** | MCP proxy — routes MCP clients to SENTINEL tools with RBAC, session management, and audit logging |
| **Datadog** | Decision event ingestion, reliability metrics, drift monitoring |
| **Braintrust** | Eval store — scores each decision against ground truth outcomes |
| **Cleric** | Creates incidents for human review when authority gate escalates |
| **ElevenLabs** | Generates a daily voice brief summarizing overnight reliability |
| **Dashboard** | Built-in observatory UI — reliability heatmap, drift charts, pattern library, live decision feed |

---

## Local Setup

```bash
# 1. Clone and configure
git clone https://github.com/espirado/agent-secure.git
cd agent-secure
cp .env.example .env
# Fill in API keys (Datadog, Braintrust, Cleric, ElevenLabs)

# 2. Install agentgateway
curl -sL https://agentgateway.dev/install | bash

# 3. One command to start everything
chmod +x start_demo.sh
./start_demo.sh
```

This starts SENTINEL (REST :8080 + MCP :8081), agentgateway (MCP proxy :3000 + UI :15000), and the dashboard server (:9090). It opens the SENTINEL dashboard in your browser, then press ENTER to switch to the agentgateway UI.

### Manual Setup

```bash
# Build and run SENTINEL with demo data
go build -o sentinel ./cmd/sentinel
SEED_DEMO_DATA=true MCP_TRANSPORT=streamable_http ./sentinel

# In another terminal — start agentgateway
agentgateway -f agentgateway.yaml

# In another terminal — serve the dashboard
python3 -m http.server 9090
open http://localhost:9090/dashboard.html
open http://localhost:15000/ui
```

---

## agentgateway Security Policies

SENTINEL uses agentgateway's CEL-based MCP authorization to enforce tool-level access control:

```yaml
mcpAuthorization:
  rules:
    # Public tools — anyone can read
    - 'mcp.tool.name == "sentinel_patterns"'
    - 'mcp.tool.name == "sentinel_reliability"'

    # Authenticated tools — requires JWT
    - 'mcp.tool.name == "sentinel_evaluate" && has(jwt.sub)'

    # Privileged tools — requires operator role
    - 'mcp.tool.name == "sentinel_pull_decisions" && has(jwt.sub) && "operator" in jwt.roles'
```

This means:
- **Read-only tools** (patterns, reliability) are accessible to any MCP client
- **Evaluation** requires the caller to be authenticated (prevents anonymous reasoning audits)
- **Datadog pull** requires operator privileges (sensitive production data)

---

## Why This Matters

As AI agents take on higher-stakes decisions — in healthcare billing, infrastructure management, financial operations — knowing *what* an agent did isn't enough. You need to know whether the reasoning behind the decision was sound *before* it commits. SENTINEL provides that governance layer, built on agentgateway's MCP security primitives.
