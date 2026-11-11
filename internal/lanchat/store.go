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

type Store struct {
	mu  sync.Mutex
	dir string
}

func NewStore(dir string) *Store {
	return &Store{dir: dir}
}

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
			continue
		}
		msgs = append(msgs, m)
	}
	return msgs, nil
}

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

func SaveNick(dir, nick string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create nick dir %s: %w", dir, err)
	}
	return os.WriteFile(filepath.Join(dir, "lanchat-nick"), []byte(nick), 0o644)
}

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

func SaveRole(dir, role string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create role dir %s: %w", dir, err)
	}
	return os.WriteFile(filepath.Join(dir, "lanchat-role"), []byte(role), 0o644)
}

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

func SaveTeam(dir, team string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create team dir %s: %w", dir, err)
	}
	return os.WriteFile(filepath.Join(dir, "lanchat-team"), []byte(team), 0o644)
}

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

// SaveTeamExplicit marks the persisted team as user-chosen (set_identity),
// so later sessions keep it instead of re-deriving the workspace default.
func SaveTeamExplicit(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create team dir %s: %w", dir, err)
	}
	return os.WriteFile(filepath.Join(dir, "lanchat-team-explicit"), []byte("1"), 0o644)
}

// LoadTeamExplicit reports whether the team was explicitly set by the user.
func LoadTeamExplicit(dir string) bool {
	data, err := os.ReadFile(filepath.Join(dir, "lanchat-team-explicit"))
	return err == nil && strings.TrimSpace(string(data)) == "1"
}

func LoadTrustedPeers(dir string) (map[string]string, error) {
	data, err := os.ReadFile(filepath.Join(dir, "trusted-peers.json"))
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]string{}, nil
		}
		return nil, err
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		debug.Log("lanchat", "trusted-peers.json in %s is corrupt: %v", dir, err)
		return nil, err
	}
	peers := make(map[string]string, len(raw))
	for id, v := range raw {
		var fp string
		if err := json.Unmarshal(v, &fp); err == nil && fp != "" && fp != "true" {
			peers[id] = fp
			continue
		}
		peers[id] = ""
	}
	return peers, nil
}

func LoadKeyPins(dir string) (map[string]string, error) {
	data, err := os.ReadFile(filepath.Join(dir, "node-key-pins.json"))
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]string{}, nil
		}
		return nil, err
	}
	var pins map[string]string
	if err := json.Unmarshal(data, &pins); err != nil {
		debug.Log("lanchat", "node-key-pins.json in %s is corrupt: %v", dir, err)
		return nil, err
	}
	return pins, nil
}

func SaveKeyPins(dir string, pins map[string]string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(pins, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(dir, "node-key-pins.json")
	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		os.Remove(tmpPath)
		return err
	}
	return nil
}

func SaveTrustedPeers(dir string, peers map[string]string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(peers, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(dir, "trusted-peers.json")
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
	if err := f.Sync(); err != nil {
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
