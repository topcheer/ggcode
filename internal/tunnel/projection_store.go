package tunnel

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/debug"
)

const ProjectionReplayLimit = 1000

type ProjectionStore struct {
	dir   string
	mu    sync.Mutex
	cache map[string]*projectionFile
}

type projectionFile struct {
	Version        int              `json:"version"`
	SessionID      string           `json:"session_id"`
	AuthorityEpoch uint64           `json:"authority_epoch,omitempty"`
	SessionInfo    *GatewayMessage  `json:"session_info,omitempty"`
	Status         *GatewayMessage  `json:"status,omitempty"`
	Activity       *GatewayMessage  `json:"activity,omitempty"`
	Events         []GatewayMessage `json:"events,omitempty"`
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
	current := state.ensureAuthorityEpoch()
	// #666: events from a superseded authority epoch must not be "laundered"
	// into the current (post-CutAuthority) epoch. If the message carries an
	// epoch below the store's, it is a late arrival from the old authority's
	// recorder (the broker callback is not synchronized with the cut) — drop
	// it instead of persisting it, so replay and the last-writer snapshot
	// slots stay free of old-authority pollution.
	if cloned.AuthorityEpoch != 0 && cloned.AuthorityEpoch < current {
		// Observability (#673): the intentional nil made a dropped event
		// indistinguishable from a successful persist — callers (e.g.
		// tunnel_host AppendProjectionEvent) could not flag projBroken. Log
		// the drop so stale-epoch late arrivals are diagnosable.
		debug.Log("tunnel", "projection store: dropped stale-epoch event (session=%s type=%s event_id=%s epoch=%d < current=%d) — late arrival from a superseded authority, not persisted",
			sessionID, cloned.Type, cloned.EventID, cloned.AuthorityEpoch, current)
		return nil
	}
	if cloned.AuthorityEpoch == 0 {
		// Legacy messages without an epoch adopt the store's current epoch
		// (pre-#666 behavior).
		cloned.AuthorityEpoch = current
	}
	switch cloned.Type {
	case EventSessionInfo:
		state.SessionInfo = replaceProjectionSlot(state.SessionInfo, &cloned)
	case EventStatus:
		state.Status = replaceProjectionSlot(state.Status, &cloned)
	case EventActivity:
		state.Activity = replaceProjectionSlot(state.Activity, &cloned)
	}

	state.Events = append(state.Events, cloned)
	if len(state.Events) > ProjectionReplayLimit {
		state.Events = append([]GatewayMessage(nil), state.Events[len(state.Events)-ProjectionReplayLimit:]...)
	}
	if err := s.saveLocked(state); err != nil {
		// #1400-A: same poison-eviction as CutAuthority - the appended event
		// must not linger in cache while disk kept the pre-append truth.
		delete(s.cache, sessionID)
		return err
	}
	return nil
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

func (s *ProjectionStore) AuthorityEpoch(sessionID string) (uint64, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return 1, nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	state, err := s.loadLocked(sessionID)
	if err != nil {
		return 0, err
	}
	return state.ensureAuthorityEpoch(), nil
}

func (s *ProjectionStore) CutAuthority(sessionID string) (uint64, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return 0, nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	state, err := s.loadLocked(sessionID)
	if err != nil {
		return 0, err
	}
	state.AuthorityEpoch = state.ensureAuthorityEpoch() + 1
	state.SessionInfo = nil
	state.Status = nil
	state.Activity = nil
	state.Events = nil
	if err := s.saveLocked(state); err != nil {
		// #1400-A: the in-memory state above was already mutated - keeping it
		// in cache leaves memory/disk split (memory has the cut, disk the
		// pre-cut history; a later successful Append would then persist the
		// promoted epoch OVER the old events). Evict so the next load rebuilds
		// from disk truth.
		delete(s.cache, sessionID)
		return 0, err
	}
	return state.AuthorityEpoch, nil
}

func (s *ProjectionStore) DeleteSession(sessionID string) error {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.cache, sessionID)
	path := s.sessionPath(sessionID)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
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
	authorityEpoch := state.ensureAuthorityEpoch()
	for i := range out {
		if out[i].AuthorityEpoch == 0 {
			out[i].AuthorityEpoch = authorityEpoch
		}
	}
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
			state := &projectionFile{Version: 1, SessionID: sessionID, AuthorityEpoch: 1}
			s.cache[sessionID] = state
			return state, nil
		}
		return nil, err
	}

	var state projectionFile
	if err := json.Unmarshal(data, &state); err != nil {
		// #1801 case 1: a torn/zero-length file (crash mid-rename before
		// the fsync fix) used to fail this session's every
		// Append/ReplayEvents/CutAuthority FOREVER. Quarantine the bad
		// file and rebuild fresh state instead.
		debug.Log("tunnel-projection", "corrupt projection file for %s (%v) - quarantining and rebuilding", sessionID, err)
		_ = os.Rename(s.sessionPath(sessionID), s.sessionPath(sessionID)+".corrupt")
		return &projectionFile{Version: 1, SessionID: sessionID}, nil
	}
	if state.Version == 0 {
		state.Version = 1
	}
	if state.SessionID == "" {
		state.SessionID = sessionID
	}
	state.ensureAuthorityEpoch()
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
	state.ensureAuthorityEpoch()
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	path := s.sessionPath(state.SessionID)
	// #1801 case 3: the daemon and the desktop/agent EACH construct a
	// store over the SAME directory - a FIXED tmp name meant two processes
	// could interleave writes on one file (O_TRUNC, not O_EXCL) and
	// publish mixed content via rename. Unique tmp per write.
	tmp, cerr := os.CreateTemp(s.dir, ".projection-*.tmp")
	if cerr != nil {
		return cerr
	}
	tmpPath := tmp.Name()
	if _, werr := tmp.Write(data); werr != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return werr
	}
	// #1801 case 1: fsync the file BEFORE rename - a crash between
	// WriteFile and Rename could publish a torn/zero-length JSON, and the
	// load side then failed that session FOREVER (the only way out was
	// deleting the file by hand).
	if serr := tmp.Sync(); serr != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return serr
	}
	if cerr := tmp.Close(); cerr != nil {
		os.Remove(tmpPath)
		return cerr
	}
	if rerr := os.Rename(tmpPath, path); rerr != nil {
		os.Remove(tmpPath)
		return rerr
	}
	// Best-effort directory fsync so the rename itself survives a crash.
	if d, derr := os.Open(s.dir); derr == nil {
		d.Sync()
		d.Close()
	}
	return nil
}

func (s *ProjectionStore) sessionPath(sessionID string) string {
	// #1801 case 2: only filepath.Separator was replaced - on Windows
	// that is a backslash, so 'a/../../x' kept its forward slashes and
	// Join escaped the directory. SessionID arrives from the gateway
	// (remote input); replace BOTH separators.
	name := strings.ReplaceAll(sessionID, "/", "_")
	name = strings.ReplaceAll(name, "\\", "_")
	return filepath.Join(s.dir, name+".json")
}

func cloneGatewayMessage(msg GatewayMessage) GatewayMessage {
	cloned := msg
	if msg.Data != nil {
		cloned.Data = append(json.RawMessage(nil), msg.Data...)
	}
	return cloned
}

func (s *projectionFile) ensureAuthorityEpoch() uint64 {
	if s == nil || s.AuthorityEpoch == 0 {
		if s != nil {
			s.AuthorityEpoch = 1
		}
		return 1
	}
	return s.AuthorityEpoch
}

// replaceProjectionSlot implements the last-writer-wins snapshot slot update
// with an epoch guard (#666): an incoming snapshot event only replaces the
// current slot when it is from the same or a newer authority epoch, so a late
// old-authority status/activity cannot clobber the new authority's snapshot.
func replaceProjectionSlot(existing, incoming *GatewayMessage) *GatewayMessage {
	if existing == nil || incoming.AuthorityEpoch >= existing.AuthorityEpoch {
		return incoming
	}
	return existing
}
