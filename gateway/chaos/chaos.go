package chaos

import (
	"context"
	"encoding/json"
	"math/rand"
	"net/http"
	"time"

	dockerclient "github.com/docker/docker/client"
	"go.uber.org/zap"
	"miniraft/gateway/leader"
)

// ChaosRequest is the JSON body for POST /chaos.
type ChaosRequest struct {
	Target string `json:"target"` // "random"|"replica1"|"replica2"|"replica3"
	Mode   string `json:"mode"`   // "graceful"|"hard"|"random"
}

// ChaosResponse is returned after a chaos action.
type ChaosResponse struct {
	Killed    string `json:"killed"`
	Mode      string `json:"mode"`
	Timestamp int64  `json:"timestamp"`
}

// ChaosHandler executes chaos actions against Docker containers.
type ChaosHandler struct {
	dockerClient *dockerclient.Client
	tracker      *leader.LeaderTracker
	logger       *zap.Logger
	metrics      interface{ IncrChaos(mode string) }
}

// NewChaosHandler creates a new ChaosHandler, connecting to the Docker daemon.
func NewChaosHandler(tracker *leader.LeaderTracker, logger *zap.Logger, metrics interface{ IncrChaos(mode string) }) (*ChaosHandler, error) {
	cli, err := dockerclient.NewClientWithOpts(
		dockerclient.FromEnv,
		dockerclient.WithAPIVersionNegotiation(),
	)
	if err != nil {
		return nil, err
	}

	return &ChaosHandler{
		dockerClient: cli,
		tracker:      tracker,
		logger:       logger,
		metrics:      metrics,
	}, nil
}

// ServeHTTP handles POST /chaos requests.
func (h *ChaosHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// CORS headers
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req ChaosRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	// Resolve target
	target := req.Target
	if target == "random" || target == "" {
		statuses := h.tracker.GetAllStatuses()
		var healthy []string
		for _, s := range statuses {
			if s.Healthy {
				healthy = append(healthy, s.ReplicaID)
			}
		}
		if len(healthy) == 0 {
			// Fall back to all replicas
			for _, s := range statuses {
				healthy = append(healthy, s.ReplicaID)
			}
		}
		if len(healthy) == 0 {
			// Last resort: pick from tracker replicas
			allStatuses := h.tracker.GetAllStatuses()
			for _, s := range allStatuses {
				healthy = append(healthy, s.ReplicaID)
			}
		}
		if len(healthy) > 0 {
			target = healthy[rand.Intn(len(healthy))]
		} else {
			http.Error(w, "no replicas available", http.StatusServiceUnavailable)
			return
		}
	}

	// Resolve mode
	mode := req.Mode
	if mode == "random" || mode == "" {
		if rand.Intn(2) == 0 {
			mode = "graceful"
		} else {
			mode = "hard"
		}
	}

	ctx := r.Context()
	var actionErr error

	switch mode {
	case "graceful":
		stopTimeout := 5 // seconds
		actionErr = h.dockerClient.ContainerStop(ctx, target, dockerclient.StopOptions{
			Timeout: &stopTimeout,
		})
	case "hard":
		actionErr = h.dockerClient.ContainerKill(ctx, target, "SIGKILL")
	default:
		http.Error(w, "unknown mode: "+mode, http.StatusBadRequest)
		return
	}

	if actionErr != nil {
		h.logger.Error("chaos action failed",
			zap.String("target", target),
			zap.String("mode", mode),
			zap.Error(actionErr),
		)
		http.Error(w, "chaos action failed: "+actionErr.Error(), http.StatusInternalServerError)
		return
	}

	h.logger.Info("chaos action executed",
		zap.String("target", target),
		zap.String("mode", mode),
	)

	if h.metrics != nil {
		h.metrics.IncrChaos(mode)
	}

	resp := ChaosResponse{
		Killed:    target,
		Mode:      mode,
		Timestamp: time.Now().UnixMilli(),
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}

// ServeHTTPStub is a no-op handler used when Docker is unavailable.
func ServeHTTPStub(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Content-Type", "application/json")

	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	w.WriteHeader(http.StatusServiceUnavailable)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"error": "chaos endpoint unavailable: Docker socket not accessible",
	})
}

// contextKey is used to avoid context key collisions.
type contextKey string

// ensure context import is used
var _ = context.Background
