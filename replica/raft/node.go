package raft

import (
	"sync"
	"time"

	"miniraft/replica/log"
	"miniraft/replica/metrics"

	"go.uber.org/zap"
)

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
	log             *log.RaftLog
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
	wal             *log.WAL
}

// NewRaftNode constructs a RaftNode with the given dependencies.
func NewRaftNode(
	id string,
	peers []string,
	raftLog *log.RaftLog,
	wal *log.WAL,
	logger *zap.Logger,
	m *metrics.ReplicaMetrics,
) *RaftNode {
	return &RaftNode{
		id:            id,
		peers:         peers,
		state:         Follower,
		log:           raftLog,
		nextIndex:     make(map[string]int64),
		matchIndex:    make(map[string]int64),
		heartbeatStop: make(chan struct{}),
		logger:        logger,
		metrics:       m,
		wal:           wal,
	}
}

// Start initialises the election timer and begins the RAFT event loop.
func (n *RaftNode) Start() {
	n.logger.Info("starting RAFT node", zap.String("id", n.id), zap.Strings("peers", n.peers))
	n.resetElectionTimer()
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

// BecomeFollower transitions the node to Follower state.
// Phase 1 stub: updates state and term, resets election timer.
func (n *RaftNode) BecomeFollower(term int64, leaderID string) {
	n.mu.Lock()
	defer n.mu.Unlock()

	n.logger.Info("becoming follower", zap.Int64("term", term), zap.String("leaderID", leaderID))
	n.state = Follower
	n.currentTerm = term
	n.leaderID = leaderID
	n.votedFor = ""

	if n.metrics != nil {
		n.metrics.RaftTerm.Set(float64(term))
		n.metrics.RaftState.Set(float64(Follower))
	}
}

// BecomeLeader transitions the node to Leader state.
// Phase 1 stub: updates state and starts heartbeat goroutine.
func (n *RaftNode) BecomeLeader() {
	n.mu.Lock()
	defer n.mu.Unlock()

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

	go sendHeartbeats(n)
}
