package lanchat

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

// SaveNick persists the nickname to <dir>/lanchat-nick.
// The directory is created if it does not exist.
func SaveNick(dir, nick string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create nick dir %s: %w", dir, err)
	}
	return os.WriteFile(filepath.Join(dir, "lanchat-nick"), []byte(nick), 0o644)
}

// LoadNick reads the nickname from <dir>/lanchat-nick.
// Returns "" and no error if the file does not exist.
func LoadNick(dir string) (string, error) {
	data, err := os.ReadFile(filepath.Join(dir, "lanchat-nick"))
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

// SaveRole persists the role to <dir>/lanchat-role.
func SaveRole(dir, role string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create role dir %s: %w", dir, err)
	}
	return os.WriteFile(filepath.Join(dir, "lanchat-role"), []byte(role), 0o644)
}

// LoadRole reads the role from <dir>/lanchat-role.
// Returns "" and no error if the file does not exist.
func LoadRole(dir string) (string, error) {
	data, err := os.ReadFile(filepath.Join(dir, "lanchat-role"))
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

// LoadApprovalPolicies reads persisted approval policies from <dir>/approval-policies.json.
// Returns map[peerNodeID]policy. Missing file returns empty map + nil error.
func LoadApprovalPolicies(dir string) (map[string]string, error) {
	data, err := os.ReadFile(filepath.Join(dir, "approval-policies.json"))
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]string{}, nil
		}
		return nil, err
	}
	var policies map[string]string
	if err := json.Unmarshal(data, &policies); err != nil {
		return map[string]string{}, nil
	}
	return policies, nil
}

// SaveApprovalPolicies persists approval policies to <dir>/approval-policies.json.
func SaveApprovalPolicies(dir string, policies map[string]string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(policies, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "approval-policies.json"), data, 0o644)
}
