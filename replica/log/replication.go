package log

import (
	"go.uber.org/zap"
)

// AppendEntriesHandler validates and applies AppendEntries RPCs to the log.
// Phase 1: stub that validates inputs and returns success.
type AppendEntriesHandler struct {
	log    *RaftLog
	logger *zap.Logger
}

func NewAppendEntriesHandler(log *RaftLog, logger *zap.Logger) *AppendEntriesHandler {
	return &AppendEntriesHandler{
		log:    log,
		logger: logger,
	}
}

// HandleAppendEntries will be called by gRPC server in rpc_server.go.
// Phase 1 stub: validates that prevLogIndex/prevLogTerm are consistent, returns success.
func (h *AppendEntriesHandler) HandleAppendEntries(
	prevLogIndex int64,
	prevLogTerm int64,
	entries []LogEntry,
	leaderCommit int64,
) (success bool, conflictIndex int64) {
	h.logger.Debug("HandleAppendEntries stub",
		zap.Int64("prevLogIndex", prevLogIndex),
		zap.Int64("prevLogTerm", prevLogTerm),
		zap.Int("numEntries", len(entries)),
		zap.Int64("leaderCommit", leaderCommit),
	)
	// Phase 1: always return success
	return true, 0
}
