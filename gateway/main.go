package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"miniraft/gateway/chaos"
	"miniraft/gateway/leader"
	"miniraft/gateway/metrics"
	"miniraft/gateway/sse"
	"miniraft/gateway/ws"
)

func getEnv(key, defaultVal string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultVal
}

func buildLogger(level string) (*zap.Logger, error) {
	var zapLevel zapcore.Level
	if err := zapLevel.UnmarshalText([]byte(level)); err != nil {
		zapLevel = zapcore.InfoLevel
	}

	cfg := zap.NewProductionEncoderConfig()
	cfg.TimeKey = "ts"
	cfg.EncodeTime = zapcore.ISO8601TimeEncoder

	core := zapcore.NewCore(
		zapcore.NewJSONEncoder(cfg),
		zapcore.AddSync(os.Stdout),
		zapLevel,
	)
	return zap.New(core), nil
}

func main() {
	port := getEnv("GATEWAY_PORT", "8080")
	replicaAddrs := getEnv("REPLICA_ADDRS", "replica1:9001,replica2:9001,replica3:9001")
	replicaHTTP := getEnv("REPLICA_HTTP", "http://replica1:8081,http://replica2:8081,http://replica3:8081")
	logLevel := getEnv("LOG_LEVEL", "info")

	logger, err := buildLogger(logLevel)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to build logger: %v\n", err)
		os.Exit(1)
	}
	defer logger.Sync() //nolint:errcheck

	// Build replica configs from env vars.
	grpcAddrs := strings.Split(replicaAddrs, ",")
	httpAddrs := strings.Split(replicaHTTP, ",")

	replicas := make([]leader.ReplicaConfig, 0, len(grpcAddrs))
	for i, addr := range grpcAddrs {
		id := fmt.Sprintf("replica%d", i+1)
		httpBase := ""
		if i < len(httpAddrs) {
			httpBase = strings.TrimSpace(httpAddrs[i])
		}
		replicas = append(replicas, leader.ReplicaConfig{
			ID:         id,
			StatusURL:  httpBase + "/status",
			StrokeURL:  httpBase + "/stroke",
			EntriesURL: httpBase + "/entries",
		})
		logger.Info("registered replica",
			zap.String("id", id),
			zap.String("grpc", strings.TrimSpace(addr)),
			zap.String("http", httpBase),
		)
	}

	// Create metrics.
	m := metrics.NewGatewayMetrics()

	// sseHub and wsHub are created below; use pointers so onChange can reference them.
	var sseHub *sse.SSEHub
	var wsHub *ws.WSHub

	// Create leader tracker.
	tracker := leader.NewLeaderTracker(replicas, logger, func(newLeaderID string, term int64) {
		m.IncrLeaderChanges()
		logger.Info("leader changed", zap.String("leader", newLeaderID), zap.Int64("term", term))
		data, _ := json.Marshal(map[string]interface{}{
			"leaderId": newLeaderID,
			"term":     term,
		})
		if sseHub != nil {
			sseHub.Broadcast("leader_elected", string(data))
		}
		// Push LEADER_CHANGED to all WebSocket clients so the toolbar updates immediately.
		if wsHub != nil {
			wsHub.BroadcastMessage("LEADER_CHANGED", map[string]interface{}{
				"leaderId": newLeaderID,
				"term":     term,
			})
		}
	})

	// Create SSE hub.
	sseHub = sse.NewSSEHub(tracker, logger)

	// Create WebSocket hub.
	wsHub = ws.NewWSHub(tracker, logger, m)

	// Create chaos handler (best-effort; falls back to stub if Docker unavailable).
	var chaosHTTP http.Handler
	chaosHandler, err := chaos.NewChaosHandler(tracker, logger, m)
	if err != nil {
		logger.Warn("chaos handler unavailable (Docker socket not accessible), using stub", zap.Error(err))
		chaosHTTP = http.HandlerFunc(chaos.ServeHTTPStub)
	} else {
		chaosHTTP = chaosHandler
	}

	// Build HTTP mux.
	mux := http.NewServeMux()
	mux.Handle("/ws", wsHub)
	mux.Handle("/events/cluster-status", sseHub)
	mux.Handle("/chaos", chaosHTTP)
	mux.Handle("/metrics", promhttp.Handler())
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"ok":true}`)) //nolint:errcheck
	})
	mux.HandleFunc("/internal/committed", internalCommittedHandler(wsHub, logger))

	// Start background goroutines.
	ctx := context.Background()
	tracker.Start(ctx)
	sseHub.StartBroadcasting(ctx)

	logger.Info("gateway listening", zap.String("port", port))
	if err := http.ListenAndServe(":"+port, corsMiddleware(mux)); err != nil {
		logger.Fatal("gateway server error", zap.Error(err))
	}
}

// internalCommittedHandler handles POST /internal/committed from replicas.
// The leader calls this whenever an entry is committed, and the gateway
// broadcasts STROKE_COMMITTED to all WebSocket clients.
func internalCommittedHandler(hub *ws.WSHub, logger *zap.Logger) http.HandlerFunc {
	type committedStrokeData struct {
		Points []map[string]float64 `json:"points"`
		Colour string               `json:"colour"`
		Width  float64              `json:"width"`
		Tool   string               `json:"tool"`
	}
	type committedEntry struct {
		Index     int64               `json:"index"`
		Term      int64               `json:"term"`
		Type      string              `json:"type"`
		StrokeID  string              `json:"strokeId"`
		UserID    string              `json:"userId"`
		Timestamp int64               `json:"ts"`
		Data      committedStrokeData `json:"data"`
	}

	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var entry committedEntry
		if err := json.NewDecoder(r.Body).Decode(&entry); err != nil {
			http.Error(w, "invalid body", http.StatusBadRequest)
			return
		}
		defer r.Body.Close()

		logger.Debug("internal/committed received",
			zap.String("strokeId", entry.StrokeID),
			zap.String("userId", entry.UserID),
		)

		if entry.Type == "UNDO_COMPENSATION" {
			// Broadcast undo removal to all clients.
			hub.BroadcastMessage("UNDO_COMPENSATION", map[string]interface{}{
				"strokeId": entry.StrokeID,
				"userId":   entry.UserID,
				"index":    entry.Index,
				"term":     entry.Term,
			})
		} else {
			// Build the payload that the frontend's STROKE_COMMITTED handler expects.
			hub.BroadcastMessage("STROKE_COMMITTED", map[string]interface{}{
				"strokeId":   entry.StrokeID,
				"userId":     entry.UserID,
				"points":     entry.Data.Points,
				"colour":     entry.Data.Colour,
				"width":      entry.Data.Width,
				"strokeTool": entry.Data.Tool,
				"index":      entry.Index,
				"term":       entry.Term,
			})
		}

		w.WriteHeader(http.StatusNoContent)
	}
}

// corsMiddleware adds permissive CORS headers for development.
func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
