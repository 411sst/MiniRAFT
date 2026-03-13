package raft

import (
	"time"

	"go.uber.org/zap"
)

const heartbeatInterval = 150 * time.Millisecond

// sendHeartbeats runs in its own goroutine while the node is Leader.
// Phase 1 stub: logs "sending heartbeat" every 150 ms until stopped.
func sendHeartbeats(n *RaftNode) {
	ticker := time.NewTicker(heartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			n.mu.Lock()
			isLeader := n.state == Leader
			term := n.currentTerm
			n.mu.Unlock()

			if !isLeader {
				return
			}

			n.logger.Debug("sending heartbeat", zap.Int64("term", term))
			if n.metrics != nil {
				n.metrics.RaftHeartbeatsSent.Inc()
			}

		case <-n.heartbeatStop:
			n.logger.Debug("heartbeat sender stopped")
			return
		}
	}
}

// stopHeartbeat signals the heartbeat goroutine to exit.
func (n *RaftNode) stopHeartbeat() {
	select {
	case n.heartbeatStop <- struct{}{}:
	default:
	}
}
