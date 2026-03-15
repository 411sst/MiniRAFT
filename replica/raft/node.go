package raft

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	rafflog "miniraft/replica/log"
	"miniraft/replica/metrics"
	proto "miniraft/replica/proto"

	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// protoToLogEntry converts a proto.LogEntry to a rafflog.LogEntry.
// Defined here so it is accessible from both node.go and rpc_server.go.
func protoToLogEntry(pe *proto.LogEntry) rafflog.LogEntry {
	var data rafflog.StrokeData
	json.Unmarshal(pe.Data, &data) //nolint:errcheck
	return rafflog.LogEntry{
		Index:     pe.Index,
		Term:      pe.Term,
		Type:      rafflog.EntryType(pe.Type),
		StrokeID:  pe.StrokeId,
		UserID:    pe.UserId,
		Data:      data,
		Timestamp: pe.Timestamp,
	}
}

// NodeState represents the RAFT role of a node.
type NodeState int

const (
	Follower  NodeState = 0
	Candidate NodeState = 1
	Leader    NodeState = 2
)

// String returns a human-readable label for the node state.
func (s NodeState) String() string {
	switch s {
	case Follower:
		return "FOLLOWER"
	case Candidate:
		return "CANDIDATE"
	case Leader:
		return "LEADER"
	default:
		return "UNKNOWN"
	}
}

// NodeStatus is a snapshot of this node's current status, safe to JSON-encode.
type NodeStatus struct {
	ReplicaID       string `json:"replicaId"`
	State           string `json:"state"`
	Term            int64  `json:"term"`
	LogLength       int    `json:"logLength"`
	CommitIndex     int64  `json:"commitIndex"`
	LeaderID        string `json:"leaderId"`
	LastHeartbeatMs int64  `json:"lastHeartbeatMs"`
	Healthy         bool   `json:"healthy"`
}

// RaftNode is the central struct that drives the RAFT state machine.
type RaftNode struct {
	mu              sync.Mutex
	id              string
	peers           []string
	state           NodeState
	currentTerm     int64
	votedFor        string
	log             *rafflog.RaftLog
	commitIndex     int64
	lastApplied     int64
	nextIndex       map[string]int64
	matchIndex      map[string]int64
	electionTimer   *time.Timer
	heartbeatStop   chan struct{}
	leaderID        string
	lastHeartbeatMs int64
	logger          *zap.Logger
	metrics         *metrics.ReplicaMetrics
	wal             *rafflog.WAL

	// Phase 2: peer gRPC connections (populated once at Start, then read-only)
	peerClients map[string]proto.RaftServiceClient

	// Phase 2: per-index commit notification channels
	commitNotifiers map[int64]chan struct{}

	// Phase 2: callback fired (in a goroutine) when an entry is committed
	onCommit func(entry rafflog.LogEntry)
}

// NewRaftNode constructs a RaftNode with the given dependencies.
func NewRaftNode(
	id string,
	peers []string,
	raftLog *rafflog.RaftLog,
	wal *rafflog.WAL,
	logger *zap.Logger,
	m *metrics.ReplicaMetrics,
) *RaftNode {
	return &RaftNode{
		id:              id,
		peers:           peers,
		state:           Follower,
		log:             raftLog,
		nextIndex:       make(map[string]int64),
		matchIndex:      make(map[string]int64),
		heartbeatStop:   make(chan struct{}),
		logger:          logger,
		metrics:         m,
		wal:             wal,
		peerClients:     make(map[string]proto.RaftServiceClient),
		commitNotifiers: make(map[int64]chan struct{}),
	}
}

// RestoreState applies persisted term and votedFor from WAL replay.
func (n *RaftNode) RestoreState(term int64, votedFor string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if term > 0 {
		n.currentTerm = term
		n.votedFor = votedFor
		if n.metrics != nil {
			n.metrics.RaftTerm.Set(float64(term))
		}
	}
}

// Dial connects to all peer gRPC addresses. Must be called before Start or SyncFromPeers.
func (n *RaftNode) Dial() {
	n.dialPeers()
}

// Start starts the election timer (begin participating in RAFT).
// Call Dial() first so peer connections are ready.
func (n *RaftNode) Start() {
	n.logger.Info("starting RAFT node", zap.String("id", n.id), zap.Strings("peers", n.peers))
	n.resetElectionTimer()
}

// SyncFromPeers tries to fetch missing log entries from any available peer via SyncLog RPC.
// This is called at startup so a restarting node can catch up before participating.
func (n *RaftNode) SyncFromPeers() {
	n.mu.Lock()
	fromIndex := n.log.LastIndex() + 1
	peers := make([]string, len(n.peers))
	copy(peers, n.peers)
	n.mu.Unlock()

	if len(peers) == 0 {
		return
	}

	n.logger.Info("attempting catch-up sync", zap.Int64("fromIndex", fromIndex))

	for _, peer := range peers {
		client, ok := n.getPeerClient(peer)
		if !ok {
			continue
		}

		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		resp, err := client.SyncLog(ctx, &proto.SyncLogRequest{
			ReplicaId: n.id,
			FromIndex: fromIndex,
		})
		cancel()

		if err != nil {
			n.logger.Debug("SyncLog RPC failed", zap.String("peer", peer), zap.Error(err))
			continue
		}

		if len(resp.Entries) == 0 {
			n.logger.Info("catch-up sync: already up-to-date", zap.String("peer", peer))
		} else {
			for _, pe := range resp.Entries {
				entry := protoToLogEntry(pe)
				if err := n.log.AppendEntry(entry); err != nil {
					n.logger.Error("catch-up AppendEntry failed", zap.Error(err))
				}
			}
			n.logger.Info("catch-up sync complete",
				zap.String("peer", peer),
				zap.Int("entries", len(resp.Entries)),
				zap.Int64("commitIndex", resp.CommitIndex),
			)
		}

		// Advance commit index.
		if resp.CommitIndex > n.log.GetCommitIndex() {
			newCommit := resp.CommitIndex
			if lastIdx := n.log.LastIndex(); lastIdx < newCommit {
				newCommit = lastIdx
			}
			n.log.Commit(newCommit) //nolint:errcheck
			n.mu.Lock()
			n.commitIndex = n.log.GetCommitIndex()
			n.mu.Unlock()
		}

		return // Success — no need to try other peers.
	}
}

// dialPeers creates gRPC client connections to all peer addresses.
// Called once from Start(); after this peerClients is read-only.
func (n *RaftNode) dialPeers() {
	for _, peer := range n.peers {
		conn, err := grpc.NewClient(peer, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			n.logger.Warn("failed to dial peer", zap.String("peer", peer), zap.Error(err))
			continue
		}
		n.peerClients[peer] = proto.NewRaftServiceClient(conn)
		n.logger.Info("dialed peer", zap.String("peer", peer))
	}
}

// getPeerClient returns the gRPC client for a peer (read-only after Start).
func (n *RaftNode) getPeerClient(peer string) (proto.RaftServiceClient, bool) {
	client, ok := n.peerClients[peer]
	return client, ok
}

// SetOnCommit registers a callback invoked (in a new goroutine) when any entry is committed.
func (n *RaftNode) SetOnCommit(fn func(entry rafflog.LogEntry)) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.onCommit = fn
}

// GetStatus returns a read-only snapshot of the node's current status.
func (n *RaftNode) GetStatus() NodeStatus {
	n.mu.Lock()
	defer n.mu.Unlock()

	entries := n.log.AllEntries()
	return NodeStatus{
		ReplicaID:       n.id,
		State:           n.state.String(),
		Term:            n.currentTerm,
		LogLength:       len(entries),
		CommitIndex:     n.commitIndex,
		LeaderID:        n.leaderID,
		LastHeartbeatMs: n.lastHeartbeatMs,
		Healthy:         true,
	}
}

// GetState returns the current NodeState (safe under lock).
func (n *RaftNode) GetState() NodeState {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.state
}

// GetTerm returns the current term (safe under lock).
func (n *RaftNode) GetTerm() int64 {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.currentTerm
}

// GetLeaderID returns the known leader ID.
func (n *RaftNode) GetLeaderID() string {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.leaderID
}

// SetLeaderID stores the known leader ID.
func (n *RaftNode) SetLeaderID(id string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.leaderID = id
}

// ResetElectionTimer is the exported wrapper used by the RPC layer.
func (n *RaftNode) ResetElectionTimer() {
	n.resetElectionTimer()
}

// BecomeFollower transitions the node to Follower state (acquires lock).
func (n *RaftNode) BecomeFollower(term int64, leaderID string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.becomeFollowerLocked(term, leaderID)
}

// becomeFollowerLocked transitions to Follower; must be called with n.mu held.
func (n *RaftNode) becomeFollowerLocked(term int64, leaderID string) {
	n.logger.Info("becoming follower", zap.Int64("term", term), zap.String("leaderID", leaderID))

	if term > n.currentTerm {
		n.currentTerm = term
		n.votedFor = ""
		if n.wal != nil {
			n.wal.WriteTerm(term)   //nolint:errcheck
			n.wal.WriteVote("")     //nolint:errcheck
		}
	}

	n.state = Follower
	n.leaderID = leaderID

	if n.metrics != nil {
		n.metrics.RaftTerm.Set(float64(n.currentTerm))
		n.metrics.RaftState.Set(float64(Follower))
	}
}

// BecomeLeader transitions the node to Leader state (acquires lock, then starts heartbeat).
func (n *RaftNode) BecomeLeader() {
	n.mu.Lock()

	n.logger.Info("becoming leader", zap.Int64("term", n.currentTerm))
	n.state = Leader
	n.leaderID = n.id

	// Initialise replication indices.
	lastIdx := n.log.LastIndex()
	for _, peer := range n.peers {
		n.nextIndex[peer] = lastIdx + 1
		n.matchIndex[peer] = 0
	}

	if n.metrics != nil {
		n.metrics.RaftState.Set(float64(Leader))
	}

	n.mu.Unlock()

	go sendHeartbeats(n)
}

// Replicate appends an entry as leader and blocks until committed (or timeout).
// Returns the committed entry or an error.
func (n *RaftNode) Replicate(entry rafflog.LogEntry, timeout time.Duration) (rafflog.LogEntry, error) {
	n.mu.Lock()

	if n.state != Leader {
		n.mu.Unlock()
		return rafflog.LogEntry{}, fmt.Errorf("not leader (current leader: %s)", n.leaderID)
	}

	entry.Index = n.log.LastIndex() + 1
	entry.Term = n.currentTerm

	if err := n.log.AppendEntry(entry); err != nil {
		n.mu.Unlock()
		return rafflog.LogEntry{}, fmt.Errorf("append failed: %w", err)
	}

	if n.metrics != nil {
		n.metrics.RaftEntriesAppended.Inc()
		n.metrics.RaftLogLength.Set(float64(entry.Index))
	}

	notifier := make(chan struct{}, 1)
	n.commitNotifiers[entry.Index] = notifier

	// Single-node cluster: commit immediately.
	if len(n.peers) == 0 {
		n.advanceCommitTo(entry.Index)
	}

	n.mu.Unlock()

	select {
	case <-notifier:
		return entry, nil
	case <-time.After(timeout):
		n.mu.Lock()
		delete(n.commitNotifiers, entry.Index)
		n.mu.Unlock()
		return rafflog.LogEntry{}, fmt.Errorf("replication timeout after %v", timeout)
	}
}

// tryAdvanceCommitIndex checks if any uncommitted entries now have majority replication.
// Must be called with n.mu held.
func (n *RaftNode) tryAdvanceCommitIndex() {
	if n.state != Leader {
		return
	}

	lastIdx := n.log.LastIndex()
	majority := len(n.peers)/2 + 1 // includes self; total cluster = len(peers)+1

	for idx := n.commitIndex + 1; idx <= lastIdx; idx++ {
		// Count replicas that have this entry (self = 1)
		count := 1
		for _, peer := range n.peers {
			if n.matchIndex[peer] >= idx {
				count++
			}
		}

		if count >= majority {
			// Only commit entries from the current term (Raft §5.4.2)
			entry, ok := n.log.GetEntry(idx)
			if ok && entry.Term == n.currentTerm {
				n.advanceCommitTo(idx)
			}
		}
	}
}

// advanceCommitTo commits all entries from commitIndex+1 up to and including idx.
// Must be called with n.mu held.
func (n *RaftNode) advanceCommitTo(idx int64) {
	for i := n.commitIndex + 1; i <= idx; i++ {
		entry, ok := n.log.GetEntry(i)
		if !ok {
			break
		}
		if err := n.log.Commit(i); err != nil {
			n.logger.Error("WAL commit failed", zap.Int64("index", i), zap.Error(err))
			break
		}

		if n.metrics != nil {
			n.metrics.RaftCommitIndex.Set(float64(i))
			n.metrics.RaftEntriesCommitted.Inc()
		}

		if ch, ok := n.commitNotifiers[i]; ok {
			select {
			case ch <- struct{}{}:
			default:
			}
			delete(n.commitNotifiers, i)
		}

		if n.onCommit != nil {
			captured := entry
			go n.onCommit(captured)
		}

		n.commitIndex = i
	}
}
