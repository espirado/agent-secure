#!/usr/bin/env bash
cd "$(dirname "$0")"
trap 'echo "Stopping..."; kill 0' EXIT

# Clean up old processes
lsof -ti :8080 :8081 :9090 :3000 :15000 2>/dev/null | xargs kill -9 2>/dev/null || true
sleep 1

# Build if needed
if [ ! -f ./sentinel ]; then
  echo "Building SENTINEL..."
  go build -o sentinel ./cmd/sentinel
fi

# Start SENTINEL
SEED_DEMO_DATA=true MCP_TRANSPORT=streamable_http ./sentinel &
sleep 2

# Start agentgateway
agentgateway -f agentgateway.yaml &
sleep 2

# Start dashboard server
python3 -m http.server 9090 --directory . &
sleep 1

# Open SENTINEL dashboard
open "http://localhost:9090/dashboard.html"

echo ""
echo "SENTINEL dashboard is open."
echo "Press ENTER to switch to agentgateway UI."
read -r

# Open agentgateway UI
open "http://localhost:15000/ui"

echo "agentgateway UI is open."
echo "Press Ctrl+C to stop everything."
wait
