package lanchat

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

// Store persists chat messages per session to JSONL files.
// Each session gets its own file with at most maxHistoryPerSession messages.
type Store struct {
	mu  sync.Mutex
	dir string // ~/.ggcode/lanchat/
}

// NewStore creates a message store rooted at the given directory.
func NewStore(dir string) *Store {
	return &Store{dir: dir}
}

// Append writes a message to the session's history file, trimming to the
// most recent maxHistoryPerSession entries.
func (s *Store) Append(sessionID string, msg Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return err
	}

	path := s.sessionPath(sessionID)

	// Read existing messages.
	msgs, err := s.readLocked(path)
	if err != nil {
		return err
	}

	// Append new message.
	msgs = append(msgs, msg)

	// Trim to max history.
	if len(msgs) > maxHistoryPerSession {
		msgs = msgs[len(msgs)-maxHistoryPerSession:]
	}

	// Rewrite file.
	return s.writeLocked(path, msgs)
}

// LoadRecent returns up to limit most recent messages for a session.
// If limit <= 0, returns maxHistoryPerSession.
func (s *Store) LoadRecent(sessionID string, limit int) ([]Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if limit <= 0 {
		limit = maxHistoryPerSession
	}

	msgs, err := s.readLocked(s.sessionPath(sessionID))
	if err != nil {
		return nil, err
	}

	if len(msgs) > limit {
		msgs = msgs[len(msgs)-limit:]
	}
	return msgs, nil
}

func (s *Store) sessionPath(sessionID string) string {
	return filepath.Join(s.dir, sessionID+".jsonl")
}

func (s *Store) readLocked(path string) ([]Message, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var msgs []Message
	for _, line := range splitLines(data) {
		if len(line) == 0 {
			continue
		}
		var m Message
		if err := json.Unmarshal(line, &m); err != nil {
			continue // skip malformed lines
		}
		msgs = append(msgs, m)
	}
	return msgs, nil
}

func (s *Store) writeLocked(path string, msgs []Message) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	enc := json.NewEncoder(f)
	for _, m := range msgs {
		if err := enc.Encode(m); err != nil {
			return err
		}
	}
	return nil
}

// splitLines splits byte data into newline-delimited chunks.
func splitLines(data []byte) [][]byte {
	var lines [][]byte
	start := 0
	for i, b := range data {
		if b == '\n' {
			lines = append(lines, data[start:i])
			start = i + 1
		}
	}
	if start < len(data) {
		lines = append(lines, data[start:])
	}
	return lines
}

// LoadNick reads the persisted nickname from ~/.ggcode/lanchat-nick.
func LoadNick(dir string) (string, error) {
	data, err := os.ReadFile(filepath.Join(dir, "lanchat-nick"))
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// SaveNick persists the nickname to ~/.ggcode/lanchat-nick.
func SaveNick(dir, nick string) error {
	return os.WriteFile(filepath.Join(dir, "lanchat-nick"), []byte(nick), 0o644)
}
