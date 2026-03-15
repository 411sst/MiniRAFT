# MiniRAFT — Distributed Raft Collaborative Canvas

MiniRAFT is a fault-tolerant collaborative whiteboard that uses the Raft consensus
algorithm to replicate every brushstroke across a three-node cluster in real time.
All drawing state is durable — the cluster survives complete restarts and single-node
failures without losing a stroke.

---

## Architecture

```
┌─────────────────────────────────────────────────────────────────┐
│                         Browser(s)                               │
│  ┌──────────┐   ┌──────────┐   ┌──────────┐   ┌─────────────┐  │
│  │  Canvas  │   │ Dashboard│   │ Toolbar  │   │ Chaos Button│  │
└──┼──────────┼───┼──────────┼───┼──────────┼───┼─────────────┼──┘
   │          │   │          │   │          │   │             │
   │ WebSocket│   │   SSE    │   │          │   │  HTTP REST  │
   │  :8080   │   │  :8080   │   │          │   │   :8080     │
   ▼          ▼   ▼          │   │          │   ▼             │
┌──────────────────────────────────────────────────────────────┐
│                        Gateway  :8080                         │
│   /ws   WebSocket hub — receives STROKE_DRAW, STROKE_UNDO    │
│   /events/cluster-status   SSE — pushes node health / leader │
│   /chaos   Docker stop/kill for chaos engineering            │
│   /internal/committed   POST from replicas on log commit     │
│   /health   /metrics   Prometheus                            │
└──────────┬──────────────────┬──────────────────┬─────────────┘
           │                  │                  │
           │  HTTP REST       │  HTTP REST       │  HTTP REST
           │  /stroke /undo   │  /stroke /undo   │  /stroke /undo
           │  /status /entries│  /status /entries│  /status /entries
           ▼                  ▼                  ▼
    ┌─────────────┐   ┌─────────────┐   ┌─────────────┐
    │  Replica 1  │   │  Replica 2  │   │  Replica 3  │
    │  :8081/:9001│   │  :8082/:9002│   │  :8083/:9003│
    │             │◄──┤             ├──►│             │
    │  Raft node  │   │  Raft node  │   │  Raft node  │
    │  WAL on disk│   │  WAL on disk│   │  WAL on disk│
    └─────────────┘   └─────────────┘   └─────────────┘
          gRPC :9001 ◄──────────────────────► gRPC :9001
          (RequestVote, AppendEntries, Heartbeat, SyncLog)
```

**Protocol summary:**

| Path | Protocol | Purpose |
|------|----------|---------|
| Browser ↔ Gateway | WebSocket | Stroke drawing, undo, canvas sync |
| Browser ↔ Gateway | SSE | Cluster status push (leader, node health) |
| Browser → Gateway | HTTP REST | Chaos (stop/kill replica) |
| Gateway ↔ Replicas | HTTP REST | Forward strokes/undos, poll status, fetch log entries |
| Replica → Gateway | HTTP REST | Notify gateway on each log commit (`/internal/committed`) |
| Replica ↔ Replica | gRPC | Raft RPCs: RequestVote, AppendEntries, Heartbeat, SyncLog |

---

## Quick Start

### Prerequisites

- [Docker](https://docs.docker.com/get-docker/) and [Docker Compose v2](https://docs.docker.com/compose/install/)
- Go 1.22+ (for local development only — not needed to run with Docker)

### Run everything

```bash
git clone <repo-url> miniraft
cd miniraft
docker compose up --build
```

Open **http://localhost:3000** in your browser.

### Port reference

| Port | Service | Description |
|------|---------|-------------|
| `3000` | Frontend | React canvas + dashboard UI |
| `8080` | Gateway | WebSocket (`/ws`), SSE (`/events/cluster-status`), REST |
| `8081` | Replica 1 | HTTP status / stroke / entries / health |
| `8082` | Replica 2 | HTTP status / stroke / entries / health |
| `8083` | Replica 3 | HTTP status / stroke / entries / health |
| `9001` | Replica 1 | gRPC (Raft RPCs) |
| `9002` | Replica 2 | gRPC (Raft RPCs) |
| `9003` | Replica 3 | gRPC (Raft RPCs) |

### Hot-reload development mode

```bash
docker compose -f docker-compose.dev.yml up --build
```

Source files in `replica/` are bind-mounted into the containers. [Air](https://github.com/air-verse/air)
watches for `.go` file changes and rebuilds automatically — editing any replica file
triggers a zero-downtime restart and new leader election within ~2 seconds.

---

## Demo Scenarios

### Kill the leader (chaos mode)

**Via UI:** click the **Chaos Mode** button (skull icon in toolbar), select a kill
mode, and click a replica node in the dashboard.

**Via CLI:**
```bash
# Stop a replica gracefully
docker stop miniraft-replica1-1

# Hard kill (simulates crash)
docker kill miniraft-replica1-1
```

A new leader is elected within 500–800 ms. Any strokes sent during the gap are
buffered at the gateway and committed once the new leader is elected (up to 2 s).

### View logs

```bash
docker logs -f miniraft-replica1-1
docker logs -f miniraft-gateway-1
```

### View cluster status

```bash
# All three replicas — returns state, term, logLength, leaderId
curl http://localhost:8081/status | jq .
curl http://localhost:8082/status | jq .
curl http://localhost:8083/status | jq .
```

### View Prometheus metrics

```bash
curl http://localhost:8080/metrics
```

Individual replica metrics:
```bash
curl http://localhost:8081/metrics
```

### View committed log entries

```bash
curl http://localhost:8081/entries | jq .
```

### Automated smoke test

Requires `websocat` (or `wscat` / Python `websockets`) for WebSocket steps:

```bash
bash scripts/smoke-test.sh
```

The script brings up the cluster, sends a stroke, kills the leader, verifies
re-election within 3 s, sends a post-failover stroke, restarts the killed replica,
and confirms it catches up — then tears everything down.

---

## Component Ownership

| Name | Component | Key Files |
|------|-----------|-----------|
| Shrish | Raft Core Engine | `replica/raft/node.go`, `election.go`, `heartbeat.go` |
| Saffiya | Log Replication + WAL | `replica/log/log.go`, `wal.go`, `replication.go` |
| Rushad | Gateway + Docker + Chaos | `gateway/`, `docker-compose.yml` |
| Ayesha | Frontend + Dashboard + Undo | `frontend/src/` |

---

## MiniRAFT Protocol Summary

- **Leader election** — each node starts an election timer (500–800 ms random). If
  no heartbeat arrives before it fires, the node becomes a Candidate, increments its
  term, and broadcasts `RequestVote` RPCs. The first Candidate to collect a majority
  (≥ 2/3) wins and becomes Leader.

- **Heartbeats** — the Leader sends `AppendEntries` (empty or with new entries) to
  all peers every 150 ms. If a follower misses enough heartbeats its timer fires and a
  new election begins. Heartbeats run in a dedicated goroutine that is cancelled
  immediately when the node steps down.

- **Log replication** — all mutations (stroke draws, undo compensations) are
  forwarded to the Leader's HTTP API. The Leader appends the entry to its log and
  replicates it to followers via `AppendEntries`. Once a majority acknowledges the
  entry it is committed and the Leader calls `POST /internal/committed` on the gateway,
  which broadcasts `STROKE_COMMITTED` / `UNDO_COMPENSATION` to all WebSocket clients.

- **Log catch-up** — a follower that falls behind (e.g. after restart) receives the
  missing entries in bulk via the `SyncLog` gRPC call, which the Leader initiates via
  `catchUpPeer` whenever an `AppendEntries` rejection signals a log gap.

- **Durability** — every log entry is written to a WAL (Write-Ahead Log) on disk and
  `fsync`-ed before the node acknowledges it. On restart, the WAL is replayed to
  restore the full log and commit index before the node joins the cluster.

- **Gateway buffering** — while the cluster has no leader (during elections) the
  gateway buffers incoming strokes in memory for up to 2 s. Once a leader is elected
  the buffer is drained and all pending strokes are committed. Strokes older than 2 s
  are dropped with an error returned to the originating client.

- **Optimistic UI** — strokes appear immediately on the drawing client at 70 %
  opacity (pending state) and switch to full opacity when `STROKE_COMMITTED` is
  received. On `CANVAS_SYNC` (reconnect / new tab) any locally pending strokes are
  re-sent so they are not lost during reconnects.

---

## Known Limitations

The following items were reviewed during the pre-submission audit and intentionally
deferred as low-risk or by-design deviations:

- **`TruncateFrom` committed-index guard is defensive only** — the guard in
  `replica/log/log.go` refuses to truncate at or below the commit index and returns
  an error, which `AppendEntries` propagates as `Success: false`. In a correct Raft
  implementation the leader never sends a conflicting entry below the commit index, so
  this path is unreachable in normal operation. It exists as a safety net.

- **Optimistic pending layer uses `globalAlpha` instead of a true separate canvas** —
  pending strokes are drawn at 70 % opacity by setting `ctx.globalAlpha = 0.7` in
  `drawing.js`. A production implementation would render pending strokes on an
  off-screen canvas and composite them, which would be more efficient for dense scenes.

- **No strokeId deduplication at the replica level** — deduplication of duplicate
  `STROKE_DRAW` messages (e.g. client retry) happens only at the gateway
  (`gateway/ws/handler.go`). If a stroke somehow reaches a replica twice it would
  be written as two log entries with the same `strokeId`. The gateway's 60-second
  dedup window makes this effectively impossible in practice.
