# 🛡 SENTINEL — AI Reasoning Observatory

> *"Every tool shows you what your AI agent decided. SENTINEL shows you whether you should have trusted it."*

**Hackathon Category: [Secure & Govern MCP](https://aihackathon.dev/)** — Security and governance of MCP/AI agents with agentgateway.

Gartner's Guardian Agent category. Built for domain-specific reasoning fidelity, not just security compliance.

---

## 🏆 Hackathon: MCP & AI Agents (aihackathon.dev)

SENTINEL is a submission to the **"Secure & Govern MCP"** track of the [AI Agent MCP Hackathon](https://aihackathon.dev/). It demonstrates how **agentgateway** can be used to secure, govern, and observe AI agent reasoning decisions at the MCP protocol layer.

### What SENTINEL Adds to the Ecosystem

| Layer | Technology | What It Does |
|-------|-----------|--------------|
| **Connectivity** | agentgateway | MCP proxy with RBAC, session management, audit logging |
| **Governance** | SENTINEL Authority Gate | Pre-decision interception: FULL_AUTO → QUARANTINE |
| **Security** | agentgateway + CEL policies | Tool-level authorization (evaluate requires JWT auth) |
| **Observability** | agentgateway + Datadog | Full MCP tool invocation audit trail |
| **Reasoning Audit** | SENTINEL Fidelity Auditor | Signal-level evidence quality scoring |
| **Pattern Detection** | SENTINEL Pattern Library | Learned failure signatures with historical accuracy |

---

## The Problem Gap

| Tool | What It Shows |
|------|--------------|
| Grafana Assistant | Execution trace — what the agent looked at |
| Datadog Bits AI | Root cause of incidents — what went wrong |
| Cleric | How to resolve incidents — what to do |
| **SENTINEL** | **Whether the reasoning was structurally sound — before the decision committed** |

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

## Sponsor Integration Map

| Sponsor / Project | Role in SENTINEL |
|-------------------|-----------------|
| **agentgateway** | MCP proxy — RBAC, session management, audit logging, CORS for SENTINEL tools |
| **Datadog** | MCP Server — decision event ingestion + reliability metrics + drift monitors |
| **Braintrust** | Eval store — every decision scored against ground truth outcomes |
| **Cleric** | Incident creation — human billing specialist review with full context |
| **ElevenLabs** | Morning voice brief — daily reliability report for ops standup |
| **SENTINEL Dashboard** | Built-in dashboard — reliability heatmap, drift charts, pattern library, live feed |

---

## Local Setup

```bash
# 1. Clone and configure
git clone https://github.com/andrewespira/sentinel.git
cd sentinel
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

## Hackathon Build Timeline (5.5 hours)

| Hour | Task |
|------|------|
| 0-0.5 | Install agentgateway, configure `agentgateway.yaml`, verify proxy |
| 0.5-1 | Get API keys, run SENTINEL, verify agentgateway → SENTINEL MCP |
| 1-2 | Wire Braintrust logging, verify eval spans appear |
| 2-3 | Create Cleric incident manually, verify webhook |
| 3-4 | ElevenLabs voice brief end-to-end |
| 4-4.5 | Configure agentgateway RBAC policies, test tool authorization |
| 4.5-5 | SENTINEL dashboard (reliability heatmap, drift charts, live feed) |
| 5-5.5 | Demo polish, run full demo script with agentgateway |

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

## The Pitch (30 seconds)

"Datadog just launched Bits AI SRE. Grafana just shipped Assistant Investigations. Cleric raised $9.8M building AI incident management. They all solve the same problem: react faster after something breaks.

Nobody is asking: is the AI agent that's fixing your incidents actually reliable? SENTINEL is the first system to audit AI reasoning at the signal level — checking whether evidence was complete and current before the decision committed — and fingerprint the failure patterns that systematically precede wrong outcomes.

In healthcare, that's not a developer tool. It's a patient safety layer. And Gartner just named this the fastest-growing category in AI for 2030."

---

## Key Differentiators vs Competition

- **vs Wayfound**: Business metrics monitoring vs reasoning-level fidelity auditing
- **vs NeuralTrust**: Security/compliance guardrails vs calibration quality gates  
- **vs Datadog Bits AI**: Incident responder vs reasoning auditor of the responder
- **vs Grafana Investigations**: Shows you the trace vs shows you why the trace was wrong
- **vs Cleric**: Human handoff layer vs the layer that decides *whether* to hand off
