package lanchat

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/topcheer/ggcode/internal/debug"
)

// Store persists chat messages per session to JSONL files.
// Each message is one line appended with O_APPEND (safe across multiple
// processes sharing the same session directory — see #990). The on-disk
// file is not trimmed on write; LoadRecent enforces maxHistoryPerSession
// on the read side instead.
type Store struct {
	mu  sync.Mutex
	dir string // ~/.ggcode/lanchat/
}

// NewStore creates a message store rooted at the given directory.
func NewStore(dir string) *Store {
	return &Store{dir: dir}
}

// Append writes a message to the session's history file.
//
// The message is written as a single O_APPEND line instead of the old
// read-modify-write rewrite (#990): s.mu only serializes within one
// process, but TUI + daemon instances routinely share the same session
// directory, and two concurrent RMW rewrites silently lose the earlier
// write. A single O_APPEND write of one line is effectively atomic on
// local filesystems, so concurrent appenders can no longer clobber each
// other. History is capped by LoadRecent on read, not on disk.
func (s *Store) Append(sessionID string, msg Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return err
	}

	line, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	line = append(line, '\n')

	path := s.sessionPath(sessionID)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()

	// Self-heal a torn tail: if a previous writer crashed mid-line, the
	// file does not end with '\n' and this message would be glued onto the
	// unparseable fragment (losing BOTH lines on read). Terminate the
	// fragment first; a concurrent healer writing a duplicate '\n' only
	// produces a blank line, which readLocked skips.
	if fi, err := f.Stat(); err == nil && fi.Size() > 0 {
		var last [1]byte
		if _, err := f.ReadAt(last[:], fi.Size()-1); err == nil && last[0] != '\n' {
			if _, err := f.Write([]byte{'\n'}); err != nil {
				return err
			}
		}
	}

	_, err = f.Write(line)
	return err
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

// SaveTeam persists the team to <dir>/lanchat-team.
func SaveTeam(dir, team string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create team dir %s: %w", dir, err)
	}
	return os.WriteFile(filepath.Join(dir, "lanchat-team"), []byte(team), 0o644)
}

// LoadTeam reads the team from <dir>/lanchat-team.
// Returns "" and no error if the file does not exist.
func LoadTeam(dir string) (string, error) {
	data, err := os.ReadFile(filepath.Join(dir, "lanchat-team"))
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
// A corrupt file returns the parse error so callers can distinguish "no
// policies yet" from "policies exist but could not be read" (#990); the
// returned map is nil in that case.
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
		debug.Log("lanchat", "approval-policies.json in %s is corrupt: %v", dir, err)
		return nil, err
	}
	return policies, nil
}

// SaveApprovalPolicies persists approval policies to <dir>/approval-policies.json
// using a tmp+rename atomic write, mirroring the Store write pattern: a
// crash mid-write must never leave a half-written JSON file behind (#990).
func SaveApprovalPolicies(dir string, policies map[string]string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(policies, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(dir, "approval-policies.json")
	tmpPath := path + ".tmp"
	f, err := os.Create(tmpPath)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(tmpPath)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmpPath)
		return err
	}
	return os.Rename(tmpPath, path)
}
