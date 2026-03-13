package log

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"

	"go.uber.org/zap"
)

type WALRecordType string

const (
	WALRecordTypeTerm   WALRecordType = "TERM"
	WALRecordTypeVote   WALRecordType = "VOTE"
	WALRecordTypeEntry  WALRecordType = "ENTRY"
	WALRecordTypeCommit WALRecordType = "COMMIT"
)

type WALRecord struct {
	Type        WALRecordType `json:"type"`
	Term        int64         `json:"term,omitempty"`
	VotedFor    string        `json:"votedFor,omitempty"`
	Entry       *LogEntry     `json:"entry,omitempty"`
	CommitIndex int64         `json:"commitIndex,omitempty"`
}

type WALState struct {
	Term        int64
	VotedFor    string
	Entries     []LogEntry
	CommitIndex int64
}

type WAL struct {
	mu     sync.Mutex
	file   *os.File
	enc    *json.Encoder
	logger *zap.Logger
}

func NewWAL(dataDir string, logger *zap.Logger) (*WAL, error) {
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return nil, err
	}
	path := filepath.Join(dataDir, "wal.log")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return nil, err
	}
	return &WAL{
		file:   f,
		enc:    json.NewEncoder(f),
		logger: logger,
	}, nil
}

func (w *WAL) write(rec WALRecord) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := w.enc.Encode(rec); err != nil {
		return err
	}
	return w.file.Sync()
}

func (w *WAL) WriteTerm(term int64) error {
	return w.write(WALRecord{Type: WALRecordTypeTerm, Term: term})
}

func (w *WAL) WriteVote(votedFor string) error {
	return w.write(WALRecord{Type: WALRecordTypeVote, VotedFor: votedFor})
}

func (w *WAL) WriteEntry(entry LogEntry) error {
	return w.write(WALRecord{Type: WALRecordTypeEntry, Entry: &entry})
}

func (w *WAL) WriteCommit(commitIndex int64) error {
	return w.write(WALRecord{Type: WALRecordTypeCommit, CommitIndex: commitIndex})
}

func (w *WAL) Replay() (WALState, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	// Open a separate read handle so we don't disturb the append file.
	path := w.file.Name()
	rf, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return WALState{}, nil
		}
		return WALState{}, err
	}
	defer rf.Close()

	state := WALState{}
	scanner := bufio.NewScanner(rf)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var rec WALRecord
		if err := json.Unmarshal(line, &rec); err != nil {
			w.logger.Warn("WAL replay: skipping malformed record", zap.Error(err))
			continue
		}
		switch rec.Type {
		case WALRecordTypeTerm:
			state.Term = rec.Term
		case WALRecordTypeVote:
			state.VotedFor = rec.VotedFor
		case WALRecordTypeEntry:
			if rec.Entry != nil {
				state.Entries = append(state.Entries, *rec.Entry)
			}
		case WALRecordTypeCommit:
			state.CommitIndex = rec.CommitIndex
		}
	}
	if err := scanner.Err(); err != nil {
		return state, err
	}
	return state, nil
}

func (w *WAL) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.file.Close()
}
