#!/usr/bin/env bash
# smoke-test.sh — MiniRAFT end-to-end smoke test
# Requires: docker, docker compose (v2), curl, jq
# Optional (for WS steps): websocat or wscat (npm -g wscat)
# Usage: bash scripts/smoke-test.sh
# Exit 0 only if every step passes.

set -euo pipefail

# ─── Colour helpers ──────────────────────────────────────────────────────────
RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; NC='\033[0m'
PASS() { printf "${GREEN}[PASS]${NC} %s\n" "$1"; }
FAIL() { printf "${RED}[FAIL]${NC} %s\n" "$1"; FAILURES=$((FAILURES+1)); }
INFO() { printf "${YELLOW}[INFO]${NC} %s\n" "$1"; }

FAILURES=0
COMPOSE="docker compose"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(dirname "$SCRIPT_DIR")"
REPLICA_HTTP_PORTS=(8081 8082 8083)  # localhost ports mapped to replica1/2/3
GATEWAY_WS="ws://localhost:8080/ws"
GATEWAY_HTTP="http://localhost:8080"

# ─── Step 1: Bring up the stack ──────────────────────────────────────────────
step1() {
  INFO "Step 1: docker compose up --build -d"
  cd "$REPO_ROOT"
  $COMPOSE up --build -d 2>&1 | tail -5

  INFO "Waiting for all replicas and gateway to pass healthchecks (up to 90s)..."
  local deadline=$(( $(date +%s) + 90 ))
  local healthy=0
  while [ $(date +%s) -lt $deadline ]; do
    healthy=0
    for port in "${REPLICA_HTTP_PORTS[@]}"; do
      if curl -sf "http://localhost:${port}/health" -o /dev/null 2>/dev/null; then
        healthy=$((healthy+1))
      fi
    done
    # gateway health
    local gw_ok=0
    if curl -sf "${GATEWAY_HTTP}/health" -o /dev/null 2>/dev/null; then gw_ok=1; fi

    if [ $healthy -eq 3 ] && [ $gw_ok -eq 1 ]; then
      PASS "Step 1: all 4 services healthy"
      return 0
    fi
    sleep 2
  done
  FAIL "Step 1: timed out waiting for healthchecks (replicas_healthy=$healthy, gw=$gw_ok)"
  return 1
}

# ─── WS helper ───────────────────────────────────────────────────────────────
# Sends one message to the WebSocket and captures output for N seconds.
# Prints received frames to stdout.  Tries websocat, then wscat, then python.
ws_send_recv() {
  local url="$1" msg="$2" timeout_sec="$3"
  if command -v websocat &>/dev/null; then
    # websocat: send one message, receive for timeout_sec, then exit
    echo "$msg" | timeout "$timeout_sec" websocat --no-close -n1 "$url" 2>/dev/null || true
  elif command -v wscat &>/dev/null; then
    # wscat: --connect, --execute (send+wait), --wait for responses
    wscat --connect "$url" --execute "$msg" --wait "$((timeout_sec*1000))" 2>/dev/null || true
  elif python3 -c "import websockets" 2>/dev/null; then
    python3 - <<PYEOF
import asyncio, json, sys
import websockets

async def run():
    async with websockets.connect("$url") as ws:
        await ws.send('''$msg''')
        try:
            async for msg in asyncio.wait_for(ws.__aiter__().__anext__(), timeout=$timeout_sec):
                print(msg)
        except (asyncio.TimeoutError, StopAsyncIteration):
            pass
asyncio.run(run())
PYEOF
  else
    echo "NO_WS_CLIENT"
  fi
}

# ─── Step 2: STROKE_DRAW → STROKE_COMMITTED ──────────────────────────────────
SMOKE_STROKE_ID="smoke-$(date +%s%N | md5sum | head -c8)"
STROKE_DRAW_MSG='{"type":"STROKE_DRAW","payload":{"strokeId":"'"$SMOKE_STROKE_ID"'","points":[{"x":10,"y":10},{"x":20,"y":20}],"colour":"#ff0000","width":3,"strokeTool":"pen"}}'

step2() {
  INFO "Step 2: STROKE_DRAW → STROKE_COMMITTED (strokeId=$SMOKE_STROKE_ID)"
  if ! command -v websocat &>/dev/null && ! command -v wscat &>/dev/null; then
    if ! python3 -c "import websockets" 2>/dev/null; then
      INFO "Step 2: SKIPPED — no WebSocket client available (install websocat or wscat)"
      return 0
    fi
  fi

  local output
  output=$(ws_send_recv "$GATEWAY_WS" "$STROKE_DRAW_MSG" 4)
  if echo "$output" | grep -q "STROKE_COMMITTED"; then
    PASS "Step 2: STROKE_COMMITTED received within 3s"
  else
    FAIL "Step 2: STROKE_COMMITTED not received (output: $(echo "$output" | head -3))"
  fi
}

# ─── Step 3: Find leader and kill it ─────────────────────────────────────────
KILLED_CONTAINER=""
KILLED_REPLICA_IDX=""

step3() {
  INFO "Step 3: Identify current leader and kill its container"
  local leader_id="" leader_port=""
  for i in 1 2 3; do
    local port="${REPLICA_HTTP_PORTS[$((i-1))]}"
    local state
    state=$(curl -sf "http://localhost:${port}/status" 2>/dev/null | jq -r '.state // empty' 2>/dev/null || true)
    if [ "$state" = "LEADER" ]; then
      leader_id="replica${i}"
      leader_port="$port"
      break
    fi
  done

  if [ -z "$leader_id" ]; then
    # Wait up to 5s for a leader to be elected before failing
    INFO "No leader yet, waiting up to 5s..."
    local deadline=$(( $(date +%s) + 5 ))
    while [ $(date +%s) -lt $deadline ]; do
      for i in 1 2 3; do
        local port="${REPLICA_HTTP_PORTS[$((i-1))]}"
        local state
        state=$(curl -sf "http://localhost:${port}/status" 2>/dev/null | jq -r '.state // empty' 2>/dev/null || true)
        if [ "$state" = "LEADER" ]; then
          leader_id="replica${i}"
          leader_port="$port"
          break 2
        fi
      done
      sleep 0.5
    done
  fi

  if [ -z "$leader_id" ]; then
    FAIL "Step 3: could not identify leader within 5s"
    return 1
  fi

  # Determine docker container name (docker compose v2 uses <project>-<service>-<N>)
  local project
  project=$(cd "$REPO_ROOT" && $COMPOSE config --format json 2>/dev/null | jq -r '.name // empty' 2>/dev/null || basename "$REPO_ROOT" | tr '[:upper:]' '[:lower:]')
  KILLED_CONTAINER="${project}-${leader_id}-1"
  KILLED_REPLICA_IDX="${leader_id: -1}"  # last char: 1, 2, or 3

  INFO "Leader is $leader_id (port $leader_port), killing container $KILLED_CONTAINER"
  docker stop "$KILLED_CONTAINER" >/dev/null
  PASS "Step 3: killed container $KILLED_CONTAINER (was $leader_id)"
}

# ─── Step 4: New leader within 3s ────────────────────────────────────────────
NEW_LEADER_PORT=""

step4() {
  INFO "Step 4: Polling for new leader (up to 3s)"
  local deadline=$(( $(date +%s) + 3 ))
  while [ $(date +%s) -lt $deadline ]; do
    for i in 1 2 3; do
      # skip the killed replica
      [ "$i" = "$KILLED_REPLICA_IDX" ] && continue
      local port="${REPLICA_HTTP_PORTS[$((i-1))]}"
      local state
      state=$(curl -sf "http://localhost:${port}/status" 2>/dev/null | jq -r '.state // empty' 2>/dev/null || true)
      if [ "$state" = "LEADER" ]; then
        NEW_LEADER_PORT="$port"
        PASS "Step 4: new leader elected — replica${i} (port $port) within 3s"
        return 0
      fi
    done
    sleep 0.1
  done
  FAIL "Step 4: no new leader elected within 3s"
  return 1
}

# ─── Step 5: STROKE_DRAW after failover ──────────────────────────────────────
step5() {
  INFO "Step 5: STROKE_DRAW after failover → STROKE_COMMITTED within 3s"
  if ! command -v websocat &>/dev/null && ! command -v wscat &>/dev/null; then
    if ! python3 -c "import websockets" 2>/dev/null; then
      INFO "Step 5: SKIPPED — no WebSocket client available"
      return 0
    fi
  fi

  local stroke_id="smoke-post-failover-$(date +%s%N | md5sum | head -c8)"
  local msg='{"type":"STROKE_DRAW","payload":{"strokeId":"'"$stroke_id"'","points":[{"x":30,"y":30}],"colour":"#0000ff","width":2,"strokeTool":"pen"}}'
  local output
  output=$(ws_send_recv "$GATEWAY_WS" "$msg" 4)
  if echo "$output" | grep -q "STROKE_COMMITTED"; then
    PASS "Step 5: STROKE_COMMITTED received after failover"
  else
    FAIL "Step 5: STROKE_COMMITTED not received after failover (output: $(echo "$output" | head -3))"
  fi
}

# ─── Step 6: Restart the stopped container ───────────────────────────────────
step6() {
  INFO "Step 6: Restarting $KILLED_CONTAINER"
  if [ -z "$KILLED_CONTAINER" ]; then
    FAIL "Step 6: no container was killed — skipping"
    return 1
  fi
  docker start "$KILLED_CONTAINER" >/dev/null
  PASS "Step 6: restarted $KILLED_CONTAINER"
}

# ─── Step 7: Catch-up verification ───────────────────────────────────────────
step7() {
  INFO "Step 7: Waiting for restarted replica to reach FOLLOWER and catch up log (up to 10s)"
  if [ -z "$KILLED_REPLICA_IDX" ]; then
    FAIL "Step 7: unknown killed replica index"
    return 1
  fi

  local restarted_port="${REPLICA_HTTP_PORTS[$((KILLED_REPLICA_IDX-1))]}"

  # Get leader's logLength from the new-leader port
  local leader_log_len=0
  if [ -n "$NEW_LEADER_PORT" ]; then
    leader_log_len=$(curl -sf "http://localhost:${NEW_LEADER_PORT}/status" 2>/dev/null | jq '.logLength // 0' 2>/dev/null || echo 0)
  fi
  INFO "Leader logLength=$leader_log_len; waiting for replica$KILLED_REPLICA_IDX (port $restarted_port) to catch up"

  local deadline=$(( $(date +%s) + 10 ))
  while [ $(date +%s) -lt $deadline ]; do
    local status_json
    status_json=$(curl -sf "http://localhost:${restarted_port}/status" 2>/dev/null || true)
    local state log_len
    state=$(echo "$status_json" | jq -r '.state // empty' 2>/dev/null || true)
    log_len=$(echo "$status_json" | jq '.logLength // -1' 2>/dev/null || echo -1)

    if [ "$state" = "FOLLOWER" ] && [ "$log_len" -ge "$leader_log_len" ]; then
      PASS "Step 7: replica$KILLED_REPLICA_IDX is FOLLOWER with logLength=$log_len (leader=$leader_log_len)"
      return 0
    fi
    sleep 0.2
  done
  FAIL "Step 7: replica$KILLED_REPLICA_IDX did not catch up in 10s (state=$state, logLength=$log_len, leader=$leader_log_len)"
  return 1
}

# ─── Step 8: Tear down ───────────────────────────────────────────────────────
step8() {
  INFO "Step 8: docker compose down"
  cd "$REPO_ROOT"
  $COMPOSE down -v 2>&1 | tail -3
  PASS "Step 8: cluster torn down"
}

# ─── Main ─────────────────────────────────────────────────────────────────────
main() {
  echo "========================================"
  echo "  MiniRAFT Smoke Test"
  echo "========================================"

  # Always run teardown at end, even on error
  trap 'step8 2>/dev/null || true' EXIT

  step1 || { FAIL "Step 1 fatal — aborting"; exit 1; }
  step2
  step3 || { FAIL "Step 3 fatal — aborting failover steps"; FAILURES=$((FAILURES+4)); step8; exit 1; }
  step4 || { FAIL "Step 4 fatal — aborting post-failover steps"; FAILURES=$((FAILURES+3)); step8; exit 1; }
  step5
  step6 || true
  step7
  # step8 runs via trap

  echo "========================================"
  if [ $FAILURES -eq 0 ]; then
    printf "${GREEN}ALL STEPS PASSED${NC}\n"
    exit 0
  else
    printf "${RED}$FAILURES STEP(S) FAILED${NC}\n"
    exit 1
  fi
}

main "$@"
