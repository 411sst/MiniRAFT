package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"google.golang.org/grpc"

	rafflog "miniraft/replica/log"
	"miniraft/replica/metrics"
	proto "miniraft/replica/proto"
	"miniraft/replica/raft"
	"miniraft/replica/status"
)

func getEnv(key, defaultVal string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultVal
}

func buildLogger(dataDir string, level string) (*zap.Logger, error) {
	var zapLevel zapcore.Level
	if err := zapLevel.UnmarshalText([]byte(level)); err != nil {
		zapLevel = zapcore.InfoLevel
	}

	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return nil, fmt.Errorf("mkdir dataDir: %w", err)
	}

	logFile, err := os.OpenFile(
		filepath.Join(dataDir, "app.log"),
		os.O_APPEND|os.O_CREATE|os.O_WRONLY,
		0644,
	)
	if err != nil {
		return nil, fmt.Errorf("open log file: %w", err)
	}

	encoderCfg := zap.NewProductionEncoderConfig()
	encoderCfg.TimeKey = "ts"
	encoderCfg.EncodeTime = zapcore.ISO8601TimeEncoder

	fileCore := zapcore.NewCore(
		zapcore.NewJSONEncoder(encoderCfg),
		zapcore.AddSync(logFile),
		zapLevel,
	)
	stdoutCore := zapcore.NewCore(
		zapcore.NewJSONEncoder(encoderCfg),
		zapcore.AddSync(os.Stdout),
		zapLevel,
	)

	return zap.New(zapcore.NewTee(fileCore, stdoutCore)), nil
}

func main() {
	replicaID := getEnv("REPLICA_ID", "replica1")
	peersRaw := getEnv("PEERS", "")
	grpcPort := getEnv("GRPC_PORT", "9001")
	httpPort := getEnv("HTTP_PORT", "8081")
	dataDir := getEnv("DATA_DIR", "/data")
	logLevel := getEnv("LOG_LEVEL", "info")

	var peers []string
	if peersRaw != "" {
		for _, p := range strings.Split(peersRaw, ",") {
			p = strings.TrimSpace(p)
			if p != "" {
				peers = append(peers, p)
			}
		}
	}

	// 1. Init logger.
	logger, err := buildLogger(dataDir, logLevel)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to create logger: %v\n", err)
		os.Exit(1)
	}
	defer logger.Sync() //nolint:errcheck

	// 2. Create WAL.
	wal, err := rafflog.NewWAL(dataDir, logger)
	if err != nil {
		logger.Fatal("failed to create WAL", zap.Error(err))
	}
	defer wal.Close() //nolint:errcheck

	// 3. Replay WAL.
	walState, err := wal.Replay()
	if err != nil {
		logger.Fatal("failed to replay WAL", zap.Error(err))
	}
	logger.Info("WAL replayed",
		zap.Int64("term", walState.Term),
		zap.String("votedFor", walState.VotedFor),
		zap.Int("entries", len(walState.Entries)),
		zap.Int64("commitIndex", walState.CommitIndex),
	)

	// 4. Create RaftLog.
	raftLog := rafflog.NewRaftLog(wal, logger)
	if err := raftLog.LoadFromWAL(); err != nil {
		logger.Fatal("failed to load log from WAL", zap.Error(err))
	}

	// 5. Create metrics.
	m := metrics.NewReplicaMetrics(replicaID)

	// 6. Create RaftNode (inject WAL state).
	node := raft.NewRaftNode(replicaID, peers, raftLog, wal, logger, m)
	if walState.Term > 0 {
		node.BecomeFollower(walState.Term, "")
	}

	// 7. Create gRPC server.
	rpcServer := raft.NewRaftRPCServer(node, logger)

	grpcServer := grpc.NewServer()
	proto.RegisterRaftServiceServer(grpcServer, rpcServer)

	// 8. Start gRPC server.
	grpcLis, err := net.Listen("tcp", ":"+grpcPort)
	if err != nil {
		logger.Fatal("failed to listen on gRPC port", zap.String("port", grpcPort), zap.Error(err))
	}
	go func() {
		logger.Info("gRPC server listening", zap.String("port", grpcPort))
		if err := grpcServer.Serve(grpcLis); err != nil {
			logger.Fatal("gRPC server error", zap.Error(err))
		}
	}()

	// 9. Build HTTP mux.
	statusHandler := status.NewStatusHandler(node)
	mux := http.NewServeMux()
	mux.HandleFunc("/health", statusHandler.ServeHealth)
	mux.HandleFunc("/status", statusHandler.ServeStatus)
	mux.Handle("/metrics", promhttp.Handler())
	mux.HandleFunc("/stroke", strokeHandler(node, logger))
	mux.HandleFunc("/entries", entriesHandler(raftLog, logger))

	httpServer := &http.Server{
		Addr:    ":" + httpPort,
		Handler: mux,
	}

	// 10. Start RaftNode.
	node.Start()

	// 11. Start HTTP server (blocking).
	logger.Info("replica started",
		zap.String("replicaId", replicaID),
		zap.String("grpcPort", grpcPort),
		zap.String("httpPort", httpPort),
		zap.String("dataDir", dataDir),
	)
	if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		logger.Fatal("HTTP server error", zap.Error(err))
	}
}

// strokeHandler handles POST /stroke.
// Phase 1 stub: if not leader returns 503; if leader returns 202.
func strokeHandler(node *raft.RaftNode, logger *zap.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		// Discard body.
		io.Copy(io.Discard, r.Body) //nolint:errcheck
		r.Body.Close()              //nolint:errcheck

		w.Header().Set("Content-Type", "application/json")

		if node.GetState() != raft.Leader {
			leaderID := node.GetLeaderID()
			w.WriteHeader(http.StatusServiceUnavailable)
			json.NewEncoder(w).Encode(map[string]interface{}{ //nolint:errcheck
				"error":    "not leader",
				"leaderId": leaderID,
			})
			return
		}

		w.WriteHeader(http.StatusAccepted)
		json.NewEncoder(w).Encode(map[string]interface{}{ //nolint:errcheck
			"accepted": true,
		})
	}
}

// entriesHandler handles GET /entries — returns all committed log entries as JSON.
func entriesHandler(raftLog *rafflog.RaftLog, logger *zap.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		commitIndex := raftLog.GetCommitIndex()
		allEntries := raftLog.AllEntries()

		var committed []rafflog.LogEntry
		for _, e := range allEntries {
			if e.Index <= commitIndex {
				committed = append(committed, e)
			}
		}
		if committed == nil {
			committed = []rafflog.LogEntry{}
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(committed) //nolint:errcheck
	}
}
