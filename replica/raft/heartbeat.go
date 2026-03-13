package raft

import (
	"context"
	"encoding/json"
	"time"

	rafflog "miniraft/replica/log"
	proto "miniraft/replica/proto"

	"go.uber.org/zap"
)

const heartbeatInterval = 150 * time.Millisecond

// sendHeartbeats runs in its own goroutine while the node is Leader.
// Every heartbeatInterval it sends AppendEntries (with any pending entries) or
// a lightweight Heartbeat (if nothing new) to every peer.
func sendHeartbeats(n *RaftNode) {
	ticker := time.NewTicker(heartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			n.mu.Lock()
			if n.state != Leader {
				n.mu.Unlock()
				return
			}

			term := n.currentTerm
			leaderID := n.id
			commitIndex := n.commitIndex
			lastIdx := n.log.LastIndex()

			type peerTask struct {
				peer         string
				prevLogIndex int64
				prevLogTerm  int64
				entries      []rafflog.LogEntry
				nextIdx      int64
			}

			tasks := make([]peerTask, 0, len(n.peers))
			for _, peer := range n.peers {
				ni := n.nextIndex[peer]
				if ni <= 0 {
					ni = 1
				}

				prevLogIndex := ni - 1
				var prevLogTerm int64
				if prevLogIndex > 0 {
					if e, ok := n.log.GetEntry(prevLogIndex); ok {
						prevLogTerm = e.Term
					}
				}

				var entries []rafflog.LogEntry
				if ni <= lastIdx {
					entries = n.log.GetEntriesFrom(ni)
				}

				tasks = append(tasks, peerTask{
					peer:         peer,
					prevLogIndex: prevLogIndex,
					prevLogTerm:  prevLogTerm,
					entries:      entries,
					nextIdx:      ni,
				})
			}
			n.mu.Unlock()

			for _, task := range tasks {
				go func(task peerTask) {
					client, ok := n.getPeerClient(task.peer)
					if !ok {
						return
					}

					if len(task.entries) == 0 {
						// Empty heartbeat.
						ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
						defer cancel()

						resp, err := client.Heartbeat(ctx, &proto.HeartbeatRequest{
							Term:        term,
							LeaderId:    leaderID,
							CommitIndex: commitIndex,
						})
						if err != nil {
							n.logger.Debug("heartbeat RPC failed",
								zap.String("peer", task.peer),
								zap.Error(err),
							)
							return
						}
						if resp.Term > term {
							n.BecomeFollower(resp.Term, "")
						}
					} else {
						// AppendEntries with real log entries.
						protoEntries := make([]*proto.LogEntry, len(task.entries))
						for i, e := range task.entries {
							data, _ := json.Marshal(e.Data)
							protoEntries[i] = &proto.LogEntry{
								Index:     e.Index,
								Term:      e.Term,
								Type:      string(e.Type),
								StrokeId:  e.StrokeID,
								UserId:    e.UserID,
								Data:      data,
								Timestamp: e.Timestamp,
							}
						}

						ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
						defer cancel()

						resp, err := client.AppendEntries(ctx, &proto.AppendEntriesRequest{
							Term:         term,
							LeaderId:     leaderID,
							PrevLogIndex: task.prevLogIndex,
							PrevLogTerm:  task.prevLogTerm,
							Entries:      protoEntries,
							LeaderCommit: commitIndex,
						})
						if err != nil {
							n.logger.Debug("AppendEntries RPC failed",
								zap.String("peer", task.peer),
								zap.Error(err),
							)
							return
						}

						if resp.Term > term {
							n.BecomeFollower(resp.Term, "")
							return
						}

						if resp.Success {
							n.mu.Lock()
							lastNewIdx := task.nextIdx + int64(len(task.entries)) - 1
							if lastNewIdx > n.matchIndex[task.peer] {
								n.matchIndex[task.peer] = lastNewIdx
								n.nextIndex[task.peer] = lastNewIdx + 1
							}
							n.tryAdvanceCommitIndex()
							n.mu.Unlock()
						} else {
							// Back off nextIndex using the follower's conflict hint.
							n.mu.Lock()
							if resp.ConflictIndex > 0 {
								n.nextIndex[task.peer] = resp.ConflictIndex
							} else if n.nextIndex[task.peer] > 1 {
								n.nextIndex[task.peer]--
							}
							n.mu.Unlock()
						}
					}

					if n.metrics != nil {
						n.metrics.RaftHeartbeatsSent.Inc()
					}
				}(task)
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
