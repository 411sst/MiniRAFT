package raft

import (
	"context"
	"time"

	proto "miniraft/replica/proto"

	"go.uber.org/zap"
)

// RaftRPCServer implements proto.RaftServiceServer.
type RaftRPCServer struct {
	proto.UnimplementedRaftServiceServer
	node   *RaftNode
	logger *zap.Logger
}

// NewRaftRPCServer constructs a RaftRPCServer.
func NewRaftRPCServer(node *RaftNode, logger *zap.Logger) *RaftRPCServer {
	return &RaftRPCServer{
		node:   node,
		logger: logger,
	}
}

// RequestVote handles a vote request from a candidate.
// Phase 1 stub: always denies the vote.
func (s *RaftRPCServer) RequestVote(_ context.Context, req *proto.VoteRequest) (*proto.VoteResponse, error) {
	s.logger.Info("RequestVote received",
		zap.String("candidateId", req.CandidateId),
		zap.Int64("term", req.Term),
		zap.Int64("lastLogIndex", req.LastLogIndex),
		zap.Int64("lastLogTerm", req.LastLogTerm),
	)

	currentTerm := s.node.GetTerm()
	return &proto.VoteResponse{
		Term:        currentTerm,
		VoteGranted: false,
	}, nil
}

// AppendEntries handles an AppendEntries RPC from the leader.
// Phase 1 stub: resets election timer and returns success.
func (s *RaftRPCServer) AppendEntries(_ context.Context, req *proto.AppendEntriesRequest) (*proto.AppendEntriesResponse, error) {
	s.logger.Debug("AppendEntries received",
		zap.String("leaderId", req.LeaderId),
		zap.Int64("term", req.Term),
		zap.Int("numEntries", len(req.Entries)),
	)

	s.node.ResetElectionTimer()
	s.node.SetLeaderID(req.LeaderId)

	currentTerm := s.node.GetTerm()
	return &proto.AppendEntriesResponse{
		Term:    currentTerm,
		Success: true,
	}, nil
}

// Heartbeat handles a heartbeat from the leader.
// Phase 1 stub: resets election timer, updates lastHeartbeat, returns success.
func (s *RaftRPCServer) Heartbeat(_ context.Context, req *proto.HeartbeatRequest) (*proto.HeartbeatResponse, error) {
	s.logger.Debug("Heartbeat received",
		zap.String("leaderId", req.LeaderId),
		zap.Int64("term", req.Term),
	)

	s.node.ResetElectionTimer()
	s.node.SetLeaderID(req.LeaderId)

	s.node.mu.Lock()
	s.node.lastHeartbeatMs = time.Now().UnixMilli()
	currentTerm := s.node.currentTerm
	s.node.mu.Unlock()

	return &proto.HeartbeatResponse{
		Term:    currentTerm,
		Success: true,
	}, nil
}

// SyncLog handles a log-sync request from a follower.
// Phase 1 stub: returns an empty entries list.
func (s *RaftRPCServer) SyncLog(_ context.Context, req *proto.SyncLogRequest) (*proto.SyncLogResponse, error) {
	s.logger.Debug("SyncLog received",
		zap.String("replicaId", req.ReplicaId),
		zap.Int64("fromIndex", req.FromIndex),
	)

	commitIndex := s.node.log.GetCommitIndex()
	return &proto.SyncLogResponse{
		Entries:     []*proto.LogEntry{},
		CommitIndex: commitIndex,
	}, nil
}
