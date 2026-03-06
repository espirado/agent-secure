#!/usr/bin/env bash
# ─────────────────────────────────────────────────────────────
# SENTINEL × Agent Gateway — Setup Script
# Hackathon Category: "Secure & Govern MCP" — aihackathon.dev
#
# This script:
#   1. Installs agentgateway binary
#   2. Starts SENTINEL (REST API + MCP server)
#   3. Starts agentgateway proxying to SENTINEL
#   4. Opens the agentgateway UI for tool exploration
# ─────────────────────────────────────────────────────────────

set -euo pipefail

GREEN='\033[0;32m'
RED='\033[0;31m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
BOLD='\033[1m'
RESET='\033[0m'

banner() {
  echo ""
  echo -e "${BLUE}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${RESET}"
  echo -e "${BOLD}  🛡 SENTINEL × Agent Gateway${RESET}"
  echo -e "${BOLD}  $1${RESET}"
  echo -e "${BLUE}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${RESET}"
  echo ""
}

step() { echo -e "${YELLOW}▶ $1${RESET}"; }
success() { echo -e "${GREEN}✓ $1${RESET}"; }
error() { echo -e "${RED}✗ $1${RESET}"; }

# ── Step 1: Check / Install agentgateway ──────────────────

banner "Step 1: Installing agentgateway"

if command -v agentgateway &> /dev/null; then
  AG_VERSION=$(agentgateway --version 2>/dev/null || echo "unknown")
  success "agentgateway already installed: $AG_VERSION"
else
  step "Installing agentgateway binary..."
  curl -sL https://agentgateway.dev/install | bash
  success "agentgateway installed"
fi

# ── Step 2: Start SENTINEL ────────────────────────────────

banner "Step 2: Starting SENTINEL"

# Check if SENTINEL is already running
if curl -s http://localhost:8080/health > /dev/null 2>&1; then
  success "SENTINEL already running on :8080"
else
  step "Starting SENTINEL with demo data..."

  # Build if needed
  if [ ! -f ./sentinel ]; then
    step "Building SENTINEL..."
    go build -o sentinel ./cmd/sentinel
    success "SENTINEL built"
  fi

  SEED_DEMO_DATA=true MCP_TRANSPORT=streamable_http ./sentinel &
  SENTINEL_PID=$!
  echo "$SENTINEL_PID" > .sentinel.pid
  sleep 2

  if curl -s http://localhost:8080/health > /dev/null 2>&1; then
    success "SENTINEL running on :8080 (REST) and :8081 (MCP)"
  else
    error "SENTINEL failed to start"
    exit 1
  fi
fi

# ── Step 3: Start agentgateway ────────────────────────────

banner "Step 3: Starting agentgateway"

step "Launching agentgateway with SENTINEL config..."

# Check if agentgateway is already running
if curl -s http://localhost:15000/ui/ > /dev/null 2>&1; then
  success "agentgateway already running"
else
  agentgateway -f agentgateway.yaml &
  AG_PID=$!
  echo "$AG_PID" > .agentgateway.pid
  sleep 2

  if curl -s http://localhost:15000/ui/ > /dev/null 2>&1; then
    success "agentgateway running on :3000 (MCP proxy) and :15000 (UI)"
  else
    error "agentgateway failed to start"
    exit 1
  fi
fi

# ── Step 4: Verify connectivity ──────────────────────────

banner "Step 4: Verifying integration"

step "Testing SENTINEL health through agentgateway..."

# Test direct SENTINEL access
if curl -s http://localhost:8080/health | grep -q "ok"; then
  success "Direct SENTINEL access: OK"
else
  error "Direct SENTINEL access: FAILED"
fi

# Test agentgateway UI
if curl -s http://localhost:15000/ui/ > /dev/null 2>&1; then
  success "agentgateway UI: OK"
else
  error "agentgateway UI: FAILED"
fi

echo ""
echo -e "${BOLD}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${RESET}"
echo ""
echo -e "${GREEN}${BOLD}  🎉 SENTINEL × Agent Gateway is ready!${RESET}"
echo ""
echo -e "  ${BOLD}Endpoints:${RESET}"
echo -e "    SENTINEL REST API:    ${BLUE}http://localhost:8080${RESET}"
echo -e "    SENTINEL MCP (direct):${BLUE}http://localhost:8081/mcp${RESET}"
echo -e "    agentgateway MCP:     ${BLUE}http://localhost:3000${RESET}  ← Clients connect here"
echo -e "    agentgateway UI:      ${BLUE}http://localhost:15000/ui${RESET}"
echo ""
echo -e "  ${BOLD}What to try:${RESET}"
echo -e "    1. Open the agentgateway UI → Playground"
echo -e "    2. Connect to ${BLUE}http://localhost:3000${RESET}"
echo -e "    3. Try the ${GREEN}sentinel_patterns${RESET} tool (public access)"
echo -e "    4. Try the ${GREEN}sentinel_evaluate${RESET} tool (requires auth)"
echo -e "    5. Check the ${GREEN}sentinel_reliability${RESET} profile"
echo ""
echo -e "  ${BOLD}MCP Tools available through agentgateway:${RESET}"
echo -e "    • ${GREEN}sentinel_evaluate${RESET}        — Pre-decision reasoning audit"
echo -e "    • ${GREEN}sentinel_pull_decisions${RESET}   — Pull agent events from Datadog"
echo -e "    • ${GREEN}sentinel_reliability${RESET}      — Agent reliability profile"
echo -e "    • ${GREEN}sentinel_patterns${RESET}         — Known failure signatures"
echo ""
echo -e "  ${BOLD}Security (via agentgateway):${RESET}"
echo -e "    • Tool-level RBAC with CEL policies"
echo -e "    • sentinel_evaluate requires JWT authentication"
echo -e "    • sentinel_pull_decisions requires operator role"
echo -e "    • Full audit trail of all MCP tool invocations"
echo ""
echo -e "${BOLD}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${RESET}"

# ── Cleanup handler ──────────────────────────────────────

cleanup() {
  echo ""
  step "Shutting down..."
  [ -f .sentinel.pid ] && kill "$(cat .sentinel.pid)" 2>/dev/null && rm .sentinel.pid
  [ -f .agentgateway.pid ] && kill "$(cat .agentgateway.pid)" 2>/dev/null && rm .agentgateway.pid
  success "All processes stopped"
}

trap cleanup EXIT

# Keep running until Ctrl+C
echo -e "${YELLOW}Press Ctrl+C to stop all services${RESET}"
wait
