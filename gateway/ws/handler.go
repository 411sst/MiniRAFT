package ws

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"go.uber.org/zap"
	"miniraft/gateway/leader"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

// colourPalette is 16 distinct accessible colours cycled per new connection.
var colourPalette = []string{
	"#E63946", "#F4A261", "#2A9D8F", "#264653",
	"#A8DADC", "#457B9D", "#E9C46A", "#F77F00",
	"#D62828", "#023E8A", "#80B918", "#6A4C93",
	"#FF6B6B", "#4ECDC4", "#45B7D1", "#96CEB4",
}

// WSMessage is the generic envelope for all WebSocket messages.
type WSMessage struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

// Client represents a single connected WebSocket client.
type Client struct {
	UserID string
	Colour string
	Conn   *websocket.Conn
	Send   chan []byte
}

// pendingStroke holds a stroke buffered during a leaderless period.
type pendingStroke struct {
	userID  string
	payload json.RawMessage
	kind    string // "draw" or "undo"
}

// WSHub manages all WebSocket clients.
type WSHub struct {
	mu          sync.RWMutex
	clients     map[string]*Client // userID -> Client
	colourIndex int
	tracker     *leader.LeaderTracker
	logger      *zap.Logger
	metrics     interface {
		IncrConnections()
		DecrConnections()
		IncrStrokes()
	}

	// Stroke buffer: holds strokes queued while there is no leader.
	bufMu   sync.Mutex
	buffer  []pendingStroke
}

// NewWSHub creates a new WSHub.
func NewWSHub(tracker *leader.LeaderTracker, logger *zap.Logger, metrics interface {
	IncrConnections()
	DecrConnections()
	IncrStrokes()
}) *WSHub {
	h := &WSHub{
		clients: make(map[string]*Client),
		tracker: tracker,
		logger:  logger,
		metrics: metrics,
	}
	go h.drainBuffer()
	return h
}

// ServeHTTP upgrades an HTTP connection to WebSocket and registers the client.
func (h *WSHub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	if r.Method == http.MethodOptions {
		w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		w.WriteHeader(http.StatusNoContent)
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		h.logger.Error("websocket upgrade failed", zap.Error(err))
		return
	}

	userID := fmt.Sprintf("user-%d", time.Now().UnixNano())

	h.mu.Lock()
	colour := colourPalette[h.colourIndex%len(colourPalette)]
	h.colourIndex++
	client := &Client{
		UserID: userID,
		Colour: colour,
		Conn:   conn,
		Send:   make(chan []byte, 256),
	}
	h.clients[userID] = client
	h.mu.Unlock()

	h.logger.Info("websocket client connected", zap.String("userID", userID), zap.String("colour", colour))
	if h.metrics != nil {
		h.metrics.IncrConnections()
	}

	// Send USER_COLOR_ASSIGNED.
	h.sendJSON(client, "USER_COLOR_ASSIGNED", map[string]string{
		"userId": userID,
		"colour": colour,
	})

	// Send CANVAS_SYNC: fetch all committed entries from the leader.
	go h.sendCanvasSync(client)

	// Start write goroutine.
	go h.writePump(client)

	// Read pump runs in the current goroutine.
	h.readPump(client)
}

// sendCanvasSync fetches committed log entries from the leader and sends them to the client.
func (h *WSHub) sendCanvasSync(client *Client) {
	cfg, ok := h.tracker.GetLeaderConfig()
	if !ok {
		h.logger.Warn("CANVAS_SYNC: no leader available", zap.String("userID", client.UserID))
		return
	}

	httpClient := &http.Client{Timeout: 3 * time.Second}
	resp, err := httpClient.Get(cfg.EntriesURL)
	if err != nil {
		h.logger.Warn("CANVAS_SYNC: failed to fetch entries",
			zap.String("url", cfg.EntriesURL),
			zap.Error(err),
		)
		return
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		h.logger.Warn("CANVAS_SYNC: failed to read entries body", zap.Error(err))
		return
	}

	// Parse entries to normalise the shape for the frontend.
	var rawEntries []json.RawMessage
	if err := json.Unmarshal(body, &rawEntries); err != nil {
		h.logger.Warn("CANVAS_SYNC: failed to parse entries", zap.Error(err))
		return
	}

	h.sendJSON(client, "CANVAS_SYNC", map[string]interface{}{
		"entries": rawEntries,
	})

	h.logger.Debug("CANVAS_SYNC sent",
		zap.String("userID", client.UserID),
		zap.Int("entries", len(rawEntries)),
	)
}

// readPump reads messages from the client until the connection closes.
func (h *WSHub) readPump(client *Client) {
	defer func() {
		h.mu.Lock()
		delete(h.clients, client.UserID)
		h.mu.Unlock()
		close(client.Send)
		client.Conn.Close()
		if h.metrics != nil {
			h.metrics.DecrConnections()
		}
		h.logger.Info("websocket client disconnected", zap.String("userID", client.UserID))
	}()

	client.Conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	client.Conn.SetPongHandler(func(string) error {
		client.Conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})

	for {
		_, raw, err := client.Conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				h.logger.Warn("websocket unexpected close", zap.String("userID", client.UserID), zap.Error(err))
			}
			return
		}

		var msg WSMessage
		if err := json.Unmarshal(raw, &msg); err != nil {
			h.logger.Warn("failed to unmarshal ws message", zap.String("userID", client.UserID), zap.Error(err))
			continue
		}

		h.handleMessage(client, msg)
	}
}

// writePump writes messages from the Send channel to the WebSocket connection.
func (h *WSHub) writePump(client *Client) {
	ticker := time.NewTicker(30 * time.Second)
	defer func() {
		ticker.Stop()
		client.Conn.Close()
	}()

	for {
		select {
		case msg, ok := <-client.Send:
			client.Conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if !ok {
				client.Conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			if err := client.Conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				h.logger.Warn("websocket write error", zap.String("userID", client.UserID), zap.Error(err))
				return
			}
		case <-ticker.C:
			client.Conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := client.Conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

// handleMessage processes an incoming WebSocket message from a client.
func (h *WSHub) handleMessage(client *Client, msg WSMessage) {
	switch msg.Type {
	case "STROKE_DRAW":
		h.logger.Info("STROKE_DRAW received",
			zap.String("userID", client.UserID),
		)
		go h.forwardStrokeToLeader(client.UserID, msg.Payload)
		if h.metrics != nil {
			h.metrics.IncrStrokes()
		}

	case "STROKE_UNDO":
		h.logger.Info("STROKE_UNDO received", zap.String("userID", client.UserID))
		go h.forwardUndoToLeader(client.UserID, msg.Payload)

	default:
		h.logger.Warn("unknown ws message type",
			zap.String("userID", client.UserID),
			zap.String("type", msg.Type),
		)
	}
}

// drainBuffer periodically retries buffered strokes once a leader is available.
func (h *WSHub) drainBuffer() {
	ticker := time.NewTicker(300 * time.Millisecond)
	defer ticker.Stop()
	for range ticker.C {
		_, ok := h.tracker.GetLeaderConfig()
		if !ok {
			continue
		}

		h.bufMu.Lock()
		if len(h.buffer) == 0 {
			h.bufMu.Unlock()
			continue
		}
		pending := h.buffer
		h.buffer = nil
		h.bufMu.Unlock()

		h.logger.Info("draining stroke buffer", zap.Int("count", len(pending)))
		for _, p := range pending {
			if p.kind == "undo" {
				go h.forwardUndoToLeader(p.userID, p.payload)
			} else {
				go h.forwardStrokeToLeader(p.userID, p.payload)
			}
		}
	}
}

// forwardStrokeToLeader sends a stroke to the leader's POST /stroke endpoint.
// The leader will replicate it and call back /internal/committed; that handler
// broadcasts STROKE_COMMITTED to all clients.
func (h *WSHub) forwardStrokeToLeader(userID string, payload json.RawMessage) {
	cfg, ok := h.tracker.GetLeaderConfig()
	if !ok {
		h.logger.Warn("no leader — buffering stroke until election completes", zap.String("userID", userID))
		h.bufMu.Lock()
		h.buffer = append(h.buffer, pendingStroke{userID: userID, payload: payload, kind: "draw"})
		h.bufMu.Unlock()
		return
	}

	body, err := json.Marshal(map[string]interface{}{
		"userId":  userID,
		"payload": payload,
	})
	if err != nil {
		h.logger.Error("failed to marshal stroke body", zap.Error(err))
		return
	}

	httpClient := &http.Client{Timeout: 5 * time.Second}
	resp, err := httpClient.Post(cfg.StrokeURL, "application/json", bytes.NewReader(body))
	if err != nil {
		h.logger.Warn("failed to forward stroke to leader",
			zap.String("leaderID", cfg.ID),
			zap.Error(err),
		)
		return
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body) //nolint:errcheck

	h.logger.Debug("stroke forwarded to leader",
		zap.String("leaderID", cfg.ID),
		zap.Int("status", resp.StatusCode),
	)
}

// forwardUndoToLeader sends a STROKE_UNDO to the leader's POST /undo endpoint.
func (h *WSHub) forwardUndoToLeader(userID string, payload json.RawMessage) {
	cfg, ok := h.tracker.GetLeaderConfig()
	if !ok {
		h.logger.Warn("no leader — buffering undo until election completes", zap.String("userID", userID))
		h.bufMu.Lock()
		h.buffer = append(h.buffer, pendingStroke{userID: userID, payload: payload, kind: "undo"})
		h.bufMu.Unlock()
		return
	}

	body, err := json.Marshal(map[string]interface{}{
		"userId":  userID,
		"payload": payload,
	})
	if err != nil {
		h.logger.Error("failed to marshal undo body", zap.Error(err))
		return
	}

	undoURL := cfg.StrokeURL[:len(cfg.StrokeURL)-len("stroke")] + "undo"
	httpClient := &http.Client{Timeout: 5 * time.Second}
	resp, err := httpClient.Post(undoURL, "application/json", bytes.NewReader(body))
	if err != nil {
		h.logger.Warn("failed to forward undo to leader",
			zap.String("leaderID", cfg.ID),
			zap.Error(err),
		)
		return
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body) //nolint:errcheck
}

// sendJSON serialises payload as a WSMessage and enqueues it for the client.
func (h *WSHub) sendJSON(client *Client, msgType string, payload interface{}) {
	raw, err := json.Marshal(payload)
	if err != nil {
		h.logger.Error("failed to marshal payload", zap.String("type", msgType), zap.Error(err))
		return
	}
	envelope := WSMessage{Type: msgType, Payload: json.RawMessage(raw)}
	data, err := json.Marshal(envelope)
	if err != nil {
		h.logger.Error("failed to marshal envelope", zap.Error(err))
		return
	}
	select {
	case client.Send <- data:
	default:
		h.logger.Warn("client send buffer full, dropping message",
			zap.String("userID", client.UserID),
			zap.String("type", msgType),
		)
	}
}

// BroadcastMessage serialises a WSMessage and sends it to all connected clients.
func (h *WSHub) BroadcastMessage(msgType string, payload interface{}) {
	raw, err := json.Marshal(payload)
	if err != nil {
		h.logger.Error("failed to marshal broadcast payload", zap.String("type", msgType), zap.Error(err))
		return
	}
	envelope := WSMessage{Type: msgType, Payload: json.RawMessage(raw)}
	data, err := json.Marshal(envelope)
	if err != nil {
		h.logger.Error("failed to marshal broadcast envelope", zap.Error(err))
		return
	}

	h.mu.RLock()
	defer h.mu.RUnlock()

	for _, client := range h.clients {
		select {
		case client.Send <- data:
		default:
			h.logger.Warn("broadcast: client buffer full, skipping",
				zap.String("userID", client.UserID),
			)
		}
	}
}

// SendToClient sends a unicast message to a specific client by userID.
func (h *WSHub) SendToClient(userID string, msgType string, payload interface{}) {
	h.mu.RLock()
	client, ok := h.clients[userID]
	h.mu.RUnlock()
	if !ok {
		h.logger.Warn("SendToClient: client not found", zap.String("userID", userID))
		return
	}
	h.sendJSON(client, msgType, payload)
}
