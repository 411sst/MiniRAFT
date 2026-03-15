package chaos

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"net"
	"net/http"
	"time"

	"go.uber.org/zap"
	"miniraft/gateway/leader"
)

// dockerHTTP is a minimal Docker API client that talks to the Docker daemon
// over the Unix socket using only stdlib net/http. It replaces
// github.com/docker/docker/client, which pulls in otelhttp (requires Go 1.25).
type dockerHTTP struct {
	client *http.Client
}

func newDockerHTTP() *dockerHTTP {
	return &dockerHTTP{
		client: &http.Client{
			Transport: &http.Transport{
				DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
					return (&net.Dialer{}).DialContext(ctx, "unix", "/var/run/docker.sock")
				},
			},
		},
	}
}

// ContainerStop sends POST /containers/{id}/stop?t={timeout} to the Docker daemon.
func (d *dockerHTTP) ContainerStop(ctx context.Context, containerID string, timeoutSec int) error {
	url := fmt.Sprintf("http://localhost/containers/%s/stop?t=%d", containerID, timeoutSec)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return err
	}
	resp, err := d.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 && resp.StatusCode != 304 {
		return fmt.Errorf("docker ContainerStop: HTTP %d", resp.StatusCode)
	}
	return nil
}

// ContainerKill sends POST /containers/{id}/kill?signal={sig} to the Docker daemon.
func (d *dockerHTTP) ContainerKill(ctx context.Context, containerID string, signal string) error {
	url := fmt.Sprintf("http://localhost/containers/%s/kill?signal=%s", containerID, signal)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return err
	}
	resp, err := d.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("docker ContainerKill: HTTP %d", resp.StatusCode)
	}
	return nil
}

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
	docker  *dockerHTTP
	tracker *leader.LeaderTracker
	logger  *zap.Logger
	metrics interface{ IncrChaos(mode string) }
}

// NewChaosHandler creates a new ChaosHandler, connecting to the Docker daemon.
func NewChaosHandler(tracker *leader.LeaderTracker, logger *zap.Logger, metrics interface{ IncrChaos(mode string) }) (*ChaosHandler, error) {
	return &ChaosHandler{
		docker:  newDockerHTTP(),
		tracker: tracker,
		logger:  logger,
		metrics: metrics,
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
			for _, s := range statuses {
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
		actionErr = h.docker.ContainerStop(ctx, target, 5)
	case "hard":
		actionErr = h.docker.ContainerKill(ctx, target, "SIGKILL")
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
