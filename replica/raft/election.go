package raft

import (
	"math/rand"
	"time"

	"go.uber.org/zap"
)

// randomTimeout returns a random election timeout between 500 and 800 ms.
func randomTimeout() time.Duration {
	return time.Duration(500+rand.Intn(300)) * time.Millisecond
}

// resetElectionTimer stops any existing election timer and resets it with a new random timeout.
func (n *RaftNode) resetElectionTimer() {
	if n.electionTimer != nil {
		if !n.electionTimer.Stop() {
			// Drain the channel to avoid spurious fires.
			select {
			case <-n.electionTimer.C:
			default:
			}
		}
		n.electionTimer.Reset(randomTimeout())
		return
	}

	n.electionTimer = time.AfterFunc(randomTimeout(), func() {
		n.logger.Warn("election timeout fired")
		n.startElection()
	})
}

// startElection is a Phase 1 stub: logs the intent and resets the timer.
func (n *RaftNode) startElection() {
	n.mu.Lock()
	n.state = Candidate
	n.currentTerm++
	term := n.currentTerm
	if n.metrics != nil {
		n.metrics.RaftElectionsTotal.Inc()
		n.metrics.RaftTerm.Set(float64(term))
		n.metrics.RaftState.Set(float64(Candidate))
	}
	n.mu.Unlock()

	n.logger.Info("starting election", zap.Int64("term", term), zap.String("id", n.id))
	n.resetElectionTimer()
}
