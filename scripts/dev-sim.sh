#!/usr/bin/env bash
# Run the whole system on your Mac/Linux machine: one server and N simulated
# gaming PCs. Open http://localhost:8080 afterwards. Ctrl+C stops everything.
#   ./scripts/dev-sim.sh 6
set -euo pipefail
cd "$(dirname "$0")/.."
N="${1:-4}"
BIN=.dev/bin
mkdir -p "$BIN" .dev
go build -o "$BIN/" ./cmd/nextgaming-server ./cmd/nextgaming-agent
pids=()
cleanup() { kill "${pids[@]}" 2>/dev/null || true; }
trap cleanup EXIT INT TERM

"$BIN/nextgaming-server" run -data .dev/server > .dev/server.log 2>&1 &
pids+=($!)
for _ in $(seq 1 50); do [ -f .dev/server/server.json ] && break; sleep 0.1; done
KEY=$(sed -n 's/.*"clubKey": *"\([^"]*\)".*/\1/p' .dev/server/server.json)

for i in $(seq 1 "$N"); do
  mkdir -p ".dev/pc$i"
  cat > ".dev/pc$i/agent.json" <<JSON
{"server":"127.0.0.1:47810","clubKey":"$KEY","localPort":$((47820 + i)),"machineId":"sim-pc-$i","clientPath":"none"}
JSON
  "$BIN/nextgaming-agent" run -data ".dev/pc$i" > ".dev/pc$i/agent.log" 2>&1 &
  pids+=($!)
  sleep 0.3
done
echo "Server + $N simulated PCs running. Admin panel: http://localhost:8080"
echo "Logs in .dev/. A simulated PC's lock state: curl localhost:4782X/state (X = simulated agent 1..N; names in the panel follow connect order)."
wait
