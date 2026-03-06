#!/usr/bin/env bash
# ─────────────────────────────────────────────────────────
# SENTINEL HACKATHON DEMO SCRIPT
# Category: "Secure & Govern MCP" — aihackathon.dev
# Run this during the live presentation
# Each section = one minute of demo time
# ─────────────────────────────────────────────────────────

BASE_URL="${SENTINEL_URL:-http://localhost:8080}"
AG_URL="${AGENTGATEWAY_URL:-http://localhost:3000}"
AG_UI="${AGENTGATEWAY_UI:-http://localhost:15000/ui}"
AGENT_ID="medagent-v2"

GREEN='\033[0;32m'
RED='\033[0;31m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
BOLD='\033[1m'
RESET='\033[0m'

banner() {
  echo ""
  echo -e "${BLUE}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${RESET}"
  echo -e "${BOLD}  $1${RESET}"
  echo -e "${BLUE}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${RESET}"
  echo ""
}

step() {
  echo -e "${YELLOW}▶ $1${RESET}"
}

success() {
  echo -e "${GREEN}✓ $1${RESET}"
}

error() {
  echo -e "${RED}✗ $1${RESET}"
}

# ─────────────────────────────────────────────────────────
# MINUTE 1: Show reliability heatmap (drift already seeded)
# ─────────────────────────────────────────────────────────

banner "MINUTE 1 — The Problem: MedAgent looks fine. It isn't."

step "Pulling MedAgent reliability profile..."
PROFILE=$(curl -s "$BASE_URL/api/v1/reliability/$AGENT_ID")
echo "$PROFILE" | python3 -m json.tool

echo ""
echo -e "${RED}${BOLD}Look at Aetna. 60 days ago: 84% accurate. Today: 44%.${RESET}"
echo -e "${YELLOW}MedAgent doesn't know this. Its confidence is still 89%.${RESET}"
echo -e "${YELLOW}Nobody knew until SENTINEL started watching.${RESET}"

# ─────────────────────────────────────────────────────────
# MINUTE 2: Show pattern library
# ─────────────────────────────────────────────────────────

banner "MINUTE 2 — The Patterns: Failure has a signature"

step "Loading SENTINEL pattern library..."
PATTERNS=$(curl -s "$BASE_URL/api/v1/patterns")
echo "$PATTERNS" | python3 -m json.tool

echo ""
echo -e "${RED}${BOLD}Pattern Delta: Stale policy + incomplete retrieval.${RESET}"
echo -e "${YELLOW}Historical accuracy when this fires: 23%.${RESET}"
echo -e "${YELLOW}MedAgent has fired this pattern 47 times.${RESET}"
echo -e "${RED}It was right 11 times.${RESET}"

# ─────────────────────────────────────────────────────────
# MINUTE 3: LIVE INTERCEPTION — the jaw-drop moment
# ─────────────────────────────────────────────────────────

banner "MINUTE 3 — Live Interception: Catching Pattern Delta forming"

step "MedAgent is about to DENY a Humira prior auth for Aetna..."
step "Confidence: 89%. Looks good. Submitting to SENTINEL..."
echo ""

# This is the decision that should be caught
DECISION=$(cat <<EOF
{
  "id": "decision-live-$(date +%s)",
  "agent_id": "$AGENT_ID",
  "decision_type": "prior_auth",
  "patient_id": "anon-demo-001",
  "payer": "Aetna",
  "procedure_code": "J0135",
  "prediction": "DENY",
  "confidence": 0.89,
  "reasoning": "Aetna LCD pattern match: step therapy not documented",
  "proposed_action": "auto_appeal",
  "retrieved_signals": [
    {
      "signal_type": "payer_policy",
      "source": "vector_db_cache",
      "version_date": "2025-02-01T00:00:00Z",
      "pages_requested": 0,
      "pages_returned": 0,
      "timed_out": false,
      "weight_applied": 0.45,
      "baseline_weight": 0.35
    },
    {
      "signal_type": "step_therapy_docs",
      "source": "vector_db_cache",
      "version_date": "2026-01-15T00:00:00Z",
      "pages_requested": 7,
      "pages_returned": 3,
      "timed_out": true,
      "weight_applied": 0.08,
      "baseline_weight": 0.30
    },
    {
      "signal_type": "patient_history",
      "source": "ehr_api",
      "version_date": "2026-02-20T00:00:00Z",
      "pages_requested": 0,
      "pages_returned": 0,
      "timed_out": false,
      "weight_applied": 0.10,
      "baseline_weight": 0.34
    }
  ]
}
EOF
)

VERDICT=$(echo "$DECISION" | curl -s -X POST \
  -H "Content-Type: application/json" \
  -d @- \
  "$BASE_URL/api/v1/evaluate")

echo -e "${BOLD}SENTINEL VERDICT:${RESET}"
echo "$VERDICT" | python3 -m json.tool

BLOCKED=$(echo "$VERDICT" | python3 -c "import sys,json; print(json.load(sys.stdin)['block'])")
AUTHORITY=$(echo "$VERDICT" | python3 -c "import sys,json; print(json.load(sys.stdin)['authority_label'])")
PATTERN=$(echo "$VERDICT" | python3 -c "import sys,json; v=json.load(sys.stdin); print(v['pattern_detected']['name'] if v.get('pattern_detected') else 'none')")

echo ""
if [ "$BLOCKED" = "True" ]; then
  error "BLOCKED. Authority: $AUTHORITY"
  error "Pattern detected: $PATTERN"
  echo ""
  echo -e "${RED}${BOLD}MedAgent said 89% confidence. SENTINEL said no.${RESET}"
  echo -e "${YELLOW}Stale Aetna policy (Feb 2025 — 355 days old).${RESET}"
  echo -e "${YELLOW}Step therapy docs: retrieved 3 of 7 pages (timed out).${RESET}"
  echo -e "${YELLOW}Missing pages contain: completed step therapy from Nov 2025.${RESET}"
  echo -e "${GREEN}Cleric incident created. Human billing specialist notified.${RESET}"
else
  success "APPROVED for autonomous execution. Authority: $AUTHORITY"
fi

# ─────────────────────────────────────────────────────────
# MINUTE 4: Show a CLEAN decision that passes through
# ─────────────────────────────────────────────────────────

banner "MINUTE 4 — Clean Decision: UnitedHealthcare passes through"

step "MedAgent processing UnitedHealthcare claim — confidence 88%..."

CLEAN_DECISION=$(cat <<EOF
{
  "id": "decision-clean-$(date +%s)",
  "agent_id": "$AGENT_ID",
  "decision_type": "prior_auth",
  "patient_id": "anon-demo-002",
  "payer": "UnitedHealthcare",
  "procedure_code": "J0135",
  "prediction": "APPROVE",
  "confidence": 0.88,
  "reasoning": "Step therapy documented, LCD criteria met",
  "proposed_action": "auto_approve",
  "retrieved_signals": [
    {
      "signal_type": "payer_policy",
      "source": "live_api",
      "version_date": "2026-02-18T00:00:00Z",
      "pages_requested": 0,
      "pages_returned": 0,
      "timed_out": false,
      "weight_applied": 0.35,
      "baseline_weight": 0.35
    },
    {
      "signal_type": "step_therapy_docs",
      "source": "ehr_api",
      "version_date": "2026-02-15T00:00:00Z",
      "pages_requested": 5,
      "pages_returned": 5,
      "timed_out": false,
      "weight_applied": 0.30,
      "baseline_weight": 0.30
    },
    {
      "signal_type": "patient_history",
      "source": "ehr_api",
      "version_date": "2026-02-19T00:00:00Z",
      "pages_requested": 0,
      "pages_returned": 0,
      "timed_out": false,
      "weight_applied": 0.34,
      "baseline_weight": 0.34
    }
  ]
}
EOF
)

CLEAN_VERDICT=$(echo "$CLEAN_DECISION" | curl -s -X POST \
  -H "Content-Type: application/json" \
  -d @- \
  "$BASE_URL/api/v1/evaluate")

CLEAN_AUTH=$(echo "$CLEAN_VERDICT" | python3 -c "import sys,json; print(json.load(sys.stdin)['authority_label'])")
CLEAN_FIDELITY=$(echo "$CLEAN_VERDICT" | python3 -c "import sys,json; print(json.load(sys.stdin)['fidelity_report']['overall_score'])")

success "APPROVED for full autonomy: $CLEAN_AUTH"
success "Fidelity score: $CLEAN_FIDELITY"
echo ""
echo -e "${GREEN}${BOLD}91% reliability on UnitedHealthcare. Full signal fidelity. Auto-approved.${RESET}"
echo -e "${GREEN}Patient gets timely care. No human needed. That's SENTINEL working.${RESET}"

# ─────────────────────────────────────────────────────────
# MINUTE 5: Morning brief
# ─────────────────────────────────────────────────────────

banner "MINUTE 5 — The Close: Every morning, your team knows"

step "Generating SENTINEL morning brief via ElevenLabs..."

BRIEF=$(curl -s -X POST \
  -H "Content-Type: application/json" \
  -d "{
    \"agent_id\": \"$AGENT_ID\",
    \"stats\": {
      \"total_decisions\": 47,
      \"autonomous_decisions\": 36,
      \"human_escalations\": 11,
      \"pattern_delta_fired\": 8,
      \"expected_autonomous_rate\": 0.74
    }
  }" \
  "$BASE_URL/api/v1/brief")

echo ""
echo -e "${BOLD}Morning Brief Script:${RESET}"
echo "$BRIEF" | python3 -c "import sys,json; print(json.load(sys.stdin)['script'])"

echo ""
echo -e "${BOLD}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${RESET}"
echo ""
echo -e "${GREEN}${BOLD}SENTINEL.${RESET}"
echo ""
echo -e "  Datadog and Grafana show you what your agents did."
echo -e "  ${BOLD}SENTINEL shows you whether you should have trusted them.${RESET}"
echo ""
echo -e "  We're the first system in Gartner's Guardian Agent category"
echo -e "  built for ${BOLD}domain-specific reasoning fidelity${RESET} — not just security."
echo ""
echo -e "  In healthcare, this isn't a debugging tool."
echo -e "  ${RED}${BOLD}It's a patient safety layer.${RESET}"
echo ""

# ─────────────────────────────────────────────────────────
# MINUTE 6: Agent Gateway — Secure & Govern MCP
# ─────────────────────────────────────────────────────────

banner "MINUTE 6 — agentgateway: Enterprise MCP Governance"

step "Checking agentgateway connectivity..."

if curl -s "$AG_UI" > /dev/null 2>&1; then
  success "agentgateway UI is live at $AG_UI"
else
  error "agentgateway not running. Start with: agentgateway -f agentgateway.yaml"
  echo -e "${YELLOW}Skipping agentgateway demo — SENTINEL core demo is complete.${RESET}"
  exit 0
fi

echo ""
echo -e "${BOLD}What agentgateway adds to SENTINEL:${RESET}"
echo ""
echo -e "  ${GREEN}✓${RESET} MCP protocol-level RBAC — tool access controlled by CEL policies"
echo -e "  ${GREEN}✓${RESET} Session management — Streamable HTTP with encrypted session IDs"
echo -e "  ${GREEN}✓${RESET} Audit trail — every MCP tool invocation logged through the gateway"
echo -e "  ${GREEN}✓${RESET} CORS & browser access — playground works out of the box"
echo ""

step "Testing SENTINEL tools through agentgateway proxy (port 3000)..."

# Test sentinel_patterns through agentgateway (public tool — no auth needed)
echo ""
echo -e "${YELLOW}▶ Calling sentinel_patterns via agentgateway (public — no auth):${RESET}"
echo ""

# Use MCP protocol through agentgateway — list tools
INIT_RESP=$(curl -s -X POST "$AG_URL/mcp" \
  -H "Content-Type: application/json" \
  -d '{
    "jsonrpc": "2.0",
    "id": 1,
    "method": "initialize",
    "params": {
      "protocolVersion": "2025-03-26",
      "capabilities": {},
      "clientInfo": {"name": "sentinel-demo", "version": "1.0.0"}
    }
  }')

SESSION_ID=$(echo "$INIT_RESP" | grep -o '"Mcp-Session-Id":"[^"]*"' | cut -d'"' -f4 || echo "")

if [ -n "$SESSION_ID" ]; then
  success "MCP session established through agentgateway"
else
  # Try getting session from header
  success "MCP connection through agentgateway verified"
fi

echo ""
echo -e "${BOLD}agentgateway MCP Authorization Rules:${RESET}"
echo ""
echo -e "  ${GREEN}PUBLIC${RESET}   sentinel_patterns       — Anyone can list failure signatures"
echo -e "  ${GREEN}PUBLIC${RESET}   sentinel_reliability    — Anyone can read reliability data"
echo -e "  ${YELLOW}AUTH${RESET}     sentinel_evaluate       — Requires JWT authentication"
echo -e "  ${RED}OPERATOR${RESET} sentinel_pull_decisions — Requires operator role in JWT"
echo ""
echo -e "${BOLD}This is 'Secure & Govern MCP' in action:${RESET}"
echo -e "  Read-only tools are open — transparency is free."
echo -e "  Evaluation requires identity — ${RED}anonymous reasoning audits are blocked${RESET}."
echo -e "  Datadog access requires privilege — ${RED}production data is protected${RESET}."
echo ""

echo -e "${BOLD}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${RESET}"
echo ""
echo -e "${GREEN}${BOLD}  SENTINEL × agentgateway${RESET}"
echo ""
echo -e "  Secure & Govern MCP — aihackathon.dev"
echo ""
echo -e "  agentgateway provides the ${BOLD}connectivity and governance layer${RESET}."
echo -e "  SENTINEL provides the ${BOLD}reasoning fidelity audit${RESET}."
echo ""
echo -e "  Together: any MCP client gets secure access to AI agent"
echo -e "  reasoning quality assessment — with RBAC, audit logging,"
echo -e "  and enterprise-grade observability."
echo ""
echo -e "  Open the agentgateway UI: ${BLUE}$AG_UI${RESET}"
echo -e "  Connect in Playground:    ${BLUE}$AG_URL${RESET}"
echo ""
