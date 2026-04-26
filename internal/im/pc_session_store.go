package im

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// pcPersistedSession contains the minimal data needed to restore a PC session after restart.
type pcPersistedSession struct {
	SessionID  string `json:"sessionId"`
	SessionKey string `json:"sessionKey"`
	AppWsURL   string `json:"appWsUrl"`
	ExpiresAt  string `json:"expiresAt"`
	GroupMode  bool   `json:"groupMode"`
	Label      string `json:"label"`
	CreatedAt  string `json:"createdAt"`
}

func (s pcPersistedSession) isExpired() bool {
	t, err := time.Parse(time.RFC3339, s.ExpiresAt)
	if err != nil {
		return true
	}
	return time.Now().After(t)
}

// PCSessionStore persists PC session data across restarts.
type PCSessionStore interface {
	LoadAll() ([]pcPersistedSession, error)
	SaveAll(sessions []pcPersistedSession) error
}

// MemoryPCSessionStore is an in-memory PC session store for testing.
type MemoryPCSessionStore struct {
	mu       sync.RWMutex
	sessions []pcPersistedSession
}

func NewMemoryPCSessionStore() *MemoryPCSessionStore {
	return &MemoryPCSessionStore{}
}

func (s *MemoryPCSessionStore) LoadAll() ([]pcPersistedSession, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]pcPersistedSession, len(s.sessions))
	copy(out, s.sessions)
	return out, nil
}

func (s *MemoryPCSessionStore) SaveAll(sessions []pcPersistedSession) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions = make([]pcPersistedSession, len(sessions))
	copy(s.sessions, sessions)
	return nil
}

// JSONFilePCSessionStore persists PC sessions to a JSON file.
type JSONFilePCSessionStore struct {
	path string
	mu   sync.Mutex
}

type pcSessionFilePayload struct {
	Sessions []pcPersistedSession `json:"sessions"`
}

func NewJSONFilePCSessionStore(path string) (*JSONFilePCSessionStore, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("creating PC session store directory: %w", err)
	}
	return &JSONFilePCSessionStore{path: path}, nil
}

func DefaultPCSessionStorePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".ggcode", "im-pc-sessions.json"), nil
}

func (s *JSONFilePCSessionStore) LoadAll() ([]pcPersistedSession, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.readAllLocked()
}

func (s *JSONFilePCSessionStore) SaveAll(sessions []pcPersistedSession) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.writeAllLocked(sessions)
}

func (s *JSONFilePCSessionStore) readAllLocked() ([]pcPersistedSession, error) {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading PC sessions: %w", err)
	}
	var payload pcSessionFilePayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, fmt.Errorf("parsing PC sessions: %w", err)
	}
	return payload.Sessions, nil
}

func (s *JSONFilePCSessionStore) writeAllLocked(sessions []pcPersistedSession) error {
	payload := pcSessionFilePayload{Sessions: sessions}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal PC sessions: %w", err)
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("writing PC sessions: %w", err)
	}
	return os.Rename(tmp, s.path)
}
