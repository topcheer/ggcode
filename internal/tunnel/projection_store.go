package tunnel

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/topcheer/ggcode/internal/config"
)

const ProjectionReplayLimit = 1000

type ProjectionStore struct {
	dir   string
	mu    sync.Mutex
	cache map[string]*projectionFile
}

type projectionFile struct {
	Version     int              `json:"version"`
	SessionID   string           `json:"session_id"`
	SessionInfo *GatewayMessage  `json:"session_info,omitempty"`
	Status      *GatewayMessage  `json:"status,omitempty"`
	Activity    *GatewayMessage  `json:"activity,omitempty"`
	Events      []GatewayMessage `json:"events,omitempty"`
}

func NewDefaultProjectionStore() (*ProjectionStore, error) {
	return NewProjectionStore(filepath.Join(config.HomeDir(), ".ggcode", "mobile-projection"))
}

func NewProjectionStore(dir string) (*ProjectionStore, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	return &ProjectionStore{
		dir:   dir,
		cache: make(map[string]*projectionFile),
	}, nil
}

func (s *ProjectionStore) Append(msg GatewayMessage) error {
	sessionID := strings.TrimSpace(msg.SessionID)
	if sessionID == "" {
		return fmt.Errorf("projection store: empty session id")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	state, err := s.loadLocked(sessionID)
	if err != nil {
		return err
	}

	cloned := cloneGatewayMessage(msg)
	switch cloned.Type {
	case EventSessionInfo:
		state.SessionInfo = &cloned
	case EventStatus:
		state.Status = &cloned
	case EventActivity:
		state.Activity = &cloned
	}

	state.Events = append(state.Events, cloned)
	if len(state.Events) > ProjectionReplayLimit {
		state.Events = append([]GatewayMessage(nil), state.Events[len(state.Events)-ProjectionReplayLimit:]...)
	}
	return s.saveLocked(state)
}

func (s *ProjectionStore) ReplayEvents(sessionID string) ([]GatewayMessage, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil, nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	state, err := s.loadLocked(sessionID)
	if err != nil {
		return nil, err
	}
	return buildProjectionReplay(state), nil
}

func buildProjectionReplay(state *projectionFile) []GatewayMessage {
	if state == nil {
		return nil
	}

	var bootstrap []GatewayMessage
	if state.SessionInfo != nil {
		bootstrap = append(bootstrap, cloneGatewayMessage(*state.SessionInfo))
	}
	if state.Status != nil {
		bootstrap = append(bootstrap, cloneGatewayMessage(*state.Status))
	}
	if state.Activity != nil {
		bootstrap = append(bootstrap, cloneGatewayMessage(*state.Activity))
	}

	seen := make(map[string]struct{}, len(bootstrap))
	orderedBootstrap := make([]GatewayMessage, 0, len(bootstrap))
	for _, msg := range bootstrap {
		if msg.EventID != "" {
			if _, ok := seen[msg.EventID]; ok {
				continue
			}
			seen[msg.EventID] = struct{}{}
		}
		orderedBootstrap = append(orderedBootstrap, msg)
	}

	tailLimit := ProjectionReplayLimit - len(orderedBootstrap)
	if tailLimit < 0 {
		tailLimit = 0
	}
	events := append([]GatewayMessage(nil), state.Events...)
	if len(events) > tailLimit {
		events = events[len(events)-tailLimit:]
	}

	eventIDs := make(map[string]struct{}, len(events))
	for _, msg := range events {
		if msg.EventID != "" {
			eventIDs[msg.EventID] = struct{}{}
		}
	}

	out := make([]GatewayMessage, 0, len(orderedBootstrap)+len(events))
	for _, msg := range orderedBootstrap {
		if msg.EventID != "" {
			if _, ok := eventIDs[msg.EventID]; ok {
				continue
			}
		}
		out = append(out, msg)
	}
	out = append(out, events...)
	SortReplayEvents(out)
	return out
}

func (s *ProjectionStore) loadLocked(sessionID string) (*projectionFile, error) {
	if cached, ok := s.cache[sessionID]; ok {
		return cached, nil
	}

	path := s.sessionPath(sessionID)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			state := &projectionFile{Version: 1, SessionID: sessionID}
			s.cache[sessionID] = state
			return state, nil
		}
		return nil, err
	}

	var state projectionFile
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, err
	}
	if state.Version == 0 {
		state.Version = 1
	}
	if state.SessionID == "" {
		state.SessionID = sessionID
	}
	s.cache[sessionID] = &state
	return &state, nil
}

func (s *ProjectionStore) saveLocked(state *projectionFile) error {
	if state == nil {
		return nil
	}
	if state.Version == 0 {
		state.Version = 1
	}
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	path := s.sessionPath(state.SessionID)
	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

func (s *ProjectionStore) sessionPath(sessionID string) string {
	name := strings.ReplaceAll(sessionID, string(filepath.Separator), "_")
	return filepath.Join(s.dir, name+".json")
}

func cloneGatewayMessage(msg GatewayMessage) GatewayMessage {
	cloned := msg
	if msg.Data != nil {
		cloned.Data = append(json.RawMessage(nil), msg.Data...)
	}
	return cloned
}
