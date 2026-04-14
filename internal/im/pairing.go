package im

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type PairingKind string

const (
	PairingKindBind   PairingKind = "bind"
	PairingKindRebind PairingKind = "rebind"
)

type PairingChallenge struct {
	Kind                 PairingKind
	Workspace            string
	Adapter              string
	Platform             Platform
	ChannelID            string
	ThreadID             string
	SenderID             string
	SenderName           string
	Code                 string
	RequestedAt          time.Time
	LastInboundMessageID string
	LastInboundAt        time.Time
	ExistingBinding      *ChannelBinding
}

func (c PairingChallenge) ReplyBinding() ChannelBinding {
	return ChannelBinding{
		Workspace:            c.Workspace,
		Platform:             c.Platform,
		Adapter:              c.Adapter,
		TargetID:             firstNonEmpty(strings.TrimSpace(c.SenderID), strings.TrimSpace(c.ChannelID)),
		ChannelID:            c.ChannelID,
		ThreadID:             c.ThreadID,
		LastInboundMessageID: c.LastInboundMessageID,
		LastInboundAt:        c.LastInboundAt,
	}
}

type PairingChannelState struct {
	Adapter       string
	Platform      Platform
	ChannelID     string
	RejectCount   int
	BlacklistedAt time.Time
	UpdatedAt     time.Time
}

func (s PairingChannelState) IsBlacklisted() bool {
	return !s.BlacklistedAt.IsZero()
}

type PairingStateStore interface {
	LoadAll() (map[string]PairingChannelState, error)
	SaveAll(map[string]PairingChannelState) error
}

type MemoryPairingStore struct {
	mu     sync.RWMutex
	states map[string]PairingChannelState
}

func NewMemoryPairingStore() *MemoryPairingStore {
	return &MemoryPairingStore{states: make(map[string]PairingChannelState)}
}

func (s *MemoryPairingStore) LoadAll() (map[string]PairingChannelState, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]PairingChannelState, len(s.states))
	for key, value := range s.states {
		out[key] = value
	}
	return out, nil
}

func (s *MemoryPairingStore) SaveAll(states map[string]PairingChannelState) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.states = make(map[string]PairingChannelState, len(states))
	for key, value := range states {
		s.states[key] = value
	}
	return nil
}

type JSONFilePairingStore struct {
	path string
	mu   sync.Mutex
}

type pairingFilePayload struct {
	Channels map[string]PairingChannelState `json:"channels"`
}

func NewJSONFilePairingStore(path string) (*JSONFilePairingStore, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("creating IM pairing directory: %w", err)
	}
	return &JSONFilePairingStore{path: path}, nil
}

func DefaultPairingStatePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".ggcode", "im-pairing.json"), nil
}

func (s *JSONFilePairingStore) LoadAll() (map[string]PairingChannelState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.readAllLocked()
}

func (s *JSONFilePairingStore) SaveAll(states map[string]PairingChannelState) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.writeAllLocked(states)
}

func (s *JSONFilePairingStore) readAllLocked() (map[string]PairingChannelState, error) {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return make(map[string]PairingChannelState), nil
		}
		return nil, fmt.Errorf("reading IM pairing state: %w", err)
	}
	var payload pairingFilePayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, fmt.Errorf("parsing IM pairing state: %w", err)
	}
	if payload.Channels == nil {
		payload.Channels = make(map[string]PairingChannelState)
	}
	return payload.Channels, nil
}

func (s *JSONFilePairingStore) writeAllLocked(states map[string]PairingChannelState) error {
	payload := pairingFilePayload{Channels: states}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal IM pairing state: %w", err)
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("writing IM pairing state: %w", err)
	}
	return os.Rename(tmp, s.path)
}

func pairingStateKey(adapter, channelID string) string {
	return strings.ToLower(strings.TrimSpace(adapter)) + "::" + strings.TrimSpace(channelID)
}
