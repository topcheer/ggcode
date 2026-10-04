package im

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/session"
)

// BindingStore persists channel bindings keyed by (workspace, adapter).
type BindingStore interface {
	Save(binding ChannelBinding) error
	Delete(workspace, adapter string) error
	List() ([]ChannelBinding, error)
	ListByWorkspace(workspace string) ([]ChannelBinding, error)
	ListByAdapter(adapter string) ([]ChannelBinding, error)
	// BindExclusive atomically removes ALL existing bindings for the given
	// adapter (across all workspaces) and saves the new binding. This prevents
	// cross-process TOCTOU races where two ggcode instances in different
	// workspaces bind the same adapter simultaneously.
	BindExclusive(binding ChannelBinding) error
	// UpdateSessionID persists the LastSessionID field for a binding identified
	// by (workspace, adapter). If sessionID is empty, the field is cleared.
	UpdateSessionID(workspace, adapter, sessionID string) error
}

// PassiveSeqReserver is an optional BindingStore capability (#3319):
// atomically advancing PassiveReplyCount by n and returning the first
// reserved seq (newCount-n+1) with the whole read-modify-write under the
// store's cross-process lock. Two ggcode instances (e.g. TUI + daemon)
// can then never reserve the same (msg_id, msg_seq) pair and have the QQ
// server deduplicate one of the sends away. Stores that do not implement
// this (test mocks) fall back to in-process reservation in the Manager.
type PassiveSeqReserver interface {
	ReservePassiveSeqs(workspace, messageID string, n int) (int, error)
}

// compositeKey builds a map key from workspace and adapter name.
func compositeKey(workspace, adapter string) string {
	return normalizeWorkspace(workspace) + "\x00" + adapter
}

func splitCompositeKey(key string) (workspace, adapter string) {
	i := strings.IndexByte(key, '\x00')
	if i < 0 {
		return key, ""
	}
	return key[:i], key[i+1:]
}

// MemoryBindingStore is an in-memory BindingStore for tests.
type MemoryBindingStore struct {
	mu       sync.RWMutex
	bindings map[string]ChannelBinding // compositeKey -> binding
}

func NewMemoryBindingStore() *MemoryBindingStore {
	return &MemoryBindingStore{bindings: make(map[string]ChannelBinding)}
}

func (s *MemoryBindingStore) Save(binding ChannelBinding) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	binding.Workspace = normalizeWorkspace(binding.Workspace)
	if binding.BoundAt.IsZero() {
		binding.BoundAt = time.Now()
	}
	s.bindings[compositeKey(binding.Workspace, binding.Adapter)] = binding
	return nil
}

func (s *MemoryBindingStore) Delete(workspace, adapter string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.bindings, compositeKey(normalizeWorkspace(workspace), adapter))
	return nil
}

func (s *MemoryBindingStore) List() ([]ChannelBinding, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]ChannelBinding, 0, len(s.bindings))
	for _, binding := range s.bindings {
		out = append(out, binding)
	}
	return out, nil
}

func (s *MemoryBindingStore) ListByWorkspace(workspace string) ([]ChannelBinding, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ws := normalizeWorkspace(workspace)
	var out []ChannelBinding
	for _, binding := range s.bindings {
		if normalizeWorkspace(binding.Workspace) == ws {
			out = append(out, binding)
		}
	}
	return out, nil
}

func (s *MemoryBindingStore) ListByAdapter(adapter string) ([]ChannelBinding, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []ChannelBinding
	for _, binding := range s.bindings {
		if binding.Adapter == adapter {
			out = append(out, binding)
		}
	}
	return out, nil
}

// BindExclusive atomically removes all existing bindings for the given adapter
// and saves the new one under a single lock.
func (s *MemoryBindingStore) BindExclusive(binding ChannelBinding) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	binding.Workspace = normalizeWorkspace(binding.Workspace)
	if binding.BoundAt.IsZero() {
		binding.BoundAt = time.Now()
	}
	for key, b := range s.bindings {
		if b.Adapter == binding.Adapter {
			delete(s.bindings, key)
		}
	}
	s.bindings[compositeKey(binding.Workspace, binding.Adapter)] = binding
	return nil
}

// ReservePassiveSeqs implements PassiveSeqReserver. In-memory
// store: the mutex already makes read-modify-write atomic within the
// process (tests use this store; production multi-instance uses the JSON
// file store whose file lock spans processes).
func (s *MemoryBindingStore) ReservePassiveSeqs(workspace, messageID string, n int) (int, error) {
	if n < 1 {
		return 0, fmt.Errorf("reserving %d passive seqs: n must be >= 1", n)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	ws := normalizeWorkspace(workspace)
	for key, b := range s.bindings {
		if b.Workspace != ws || strings.TrimSpace(b.LastInboundMessageID) != messageID {
			continue
		}
		if b.PassiveReplyStartedAt.IsZero() {
			b.PassiveReplyStartedAt = time.Now()
		}
		b.PassiveReplyCount += n
		s.bindings[key] = b
		return b.PassiveReplyCount - n + 1, nil
	}
	return 0, ErrNoChannelBound
}

func (s *MemoryBindingStore) UpdateSessionID(workspace, adapter, sessionID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := compositeKey(normalizeWorkspace(workspace), adapter)
	if b, ok := s.bindings[key]; ok {
		b.LastSessionID = sessionID
		s.bindings[key] = b
	}
	return nil
}

// JSONFileBindingStore persists bindings to a JSON file with atomic writes.
type JSONFileBindingStore struct {
	path string
	mu   sync.Mutex
}

func NewJSONFileBindingStore(path string) (*JSONFileBindingStore, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("creating IM binding directory: %w", err)
	}
	return &JSONFileBindingStore{path: path}, nil
}

func DefaultBindingsPath() (string, error) {
	home := config.HomeDir()
	return filepath.Join(home, ".ggcode", "im-bindings.json"), nil
}

func (s *JSONFileBindingStore) Save(binding ChannelBinding) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	unlock, err := lockBindingsFile(s.path)
	if err != nil {
		return fmt.Errorf("acquiring IM bindings lock: %w", err)
	}
	defer unlock()
	all, err := s.readAllLocked()
	if err != nil {
		return err
	}
	binding.Workspace = normalizeWorkspace(binding.Workspace)
	if binding.BoundAt.IsZero() {
		binding.BoundAt = time.Now()
	}
	key := compositeKey(binding.Workspace, binding.Adapter)
	// Preserve LastSessionID from the file if it was recently updated by
	// another instance (e.g. via UnmuteBinding/EnableBinding). Without this,
	// a persistBinding call (triggered by inbound message, context token
	// update, etc.) would overwrite the file with a stale in-memory
	// LastSessionID, causing the binding watcher to auto-mute the adapter.
	if existing, ok := all[key]; ok && existing.LastSessionID != "" && binding.LastSessionID != existing.LastSessionID {
		binding.LastSessionID = existing.LastSessionID
	}
	all[key] = binding
	return s.writeAllLocked(all)
}

// ReservePassiveSeqs implements PassiveSeqReserver. The whole
// read-modify-write cycle runs under the cross-process bindings file lock,
// so concurrent instances serialize here instead of racing in memory (#3319).
func (s *JSONFileBindingStore) ReservePassiveSeqs(workspace, messageID string, n int) (int, error) {
	if n < 1 {
		return 0, fmt.Errorf("reserving %d passive seqs: n must be >= 1", n)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	unlock, err := lockBindingsFile(s.path)
	if err != nil {
		return 0, fmt.Errorf("acquiring IM bindings lock: %w", err)
	}
	defer unlock()
	all, err := s.readAllLocked()
	if err != nil {
		return 0, err
	}
	ws := normalizeWorkspace(workspace)
	for key, b := range all {
		if b.Workspace != ws || strings.TrimSpace(b.LastInboundMessageID) != messageID {
			continue
		}
		if b.PassiveReplyStartedAt.IsZero() {
			b.PassiveReplyStartedAt = time.Now()
		}
		b.PassiveReplyCount += n
		all[key] = b
		if err := s.writeAllLocked(all); err != nil {
			return 0, err
		}
		return b.PassiveReplyCount - n + 1, nil
	}
	return 0, ErrNoChannelBound
}

func (s *JSONFileBindingStore) Delete(workspace, adapter string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	unlock, err := lockBindingsFile(s.path)
	if err != nil {
		return fmt.Errorf("acquiring IM bindings lock: %w", err)
	}
	defer unlock()
	all, err := s.readAllLocked()
	if err != nil {
		return err
	}
	delete(all, compositeKey(normalizeWorkspace(workspace), adapter))
	return s.writeAllLocked(all)
}

func (s *JSONFileBindingStore) List() ([]ChannelBinding, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	all, err := s.readAllLocked()
	if err != nil {
		return nil, err
	}
	out := make([]ChannelBinding, 0, len(all))
	for _, binding := range all {
		out = append(out, binding)
	}
	return out, nil
}

// BindExclusive atomically removes all bindings for the given adapter across
// all workspaces, then saves the new binding — under a single file lock.
// This prevents cross-process TOCTOU where two processes read the file
// simultaneously, each deleting the other's binding, then both writing back.
func (s *JSONFileBindingStore) BindExclusive(binding ChannelBinding) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	unlock, err := lockBindingsFile(s.path)
	if err != nil {
		return fmt.Errorf("acquiring IM bindings lock: %w", err)
	}
	defer unlock()

	all, err := s.readAllLocked()
	if err != nil {
		return err
	}

	binding.Workspace = normalizeWorkspace(binding.Workspace)
	if binding.BoundAt.IsZero() {
		binding.BoundAt = time.Now()
	}

	// Remove every binding for this adapter, regardless of workspace.
	for key, b := range all {
		if b.Adapter == binding.Adapter {
			delete(all, key)
		}
	}

	// Insert the new binding.
	all[compositeKey(binding.Workspace, binding.Adapter)] = binding
	return s.writeAllLocked(all)
}

func (s *JSONFileBindingStore) ListByWorkspace(workspace string) ([]ChannelBinding, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	all, err := s.readAllLocked()
	if err != nil {
		return nil, err
	}
	ws := normalizeWorkspace(workspace)
	var out []ChannelBinding
	for _, binding := range all {
		if normalizeWorkspace(binding.Workspace) == ws {
			out = append(out, binding)
		}
	}
	return out, nil
}

func (s *JSONFileBindingStore) ListByAdapter(adapter string) ([]ChannelBinding, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	all, err := s.readAllLocked()
	if err != nil {
		return nil, err
	}
	var out []ChannelBinding
	for _, binding := range all {
		if binding.Adapter == adapter {
			out = append(out, binding)
		}
	}
	return out, nil
}

func (s *JSONFileBindingStore) UpdateSessionID(workspace, adapter, sessionID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	unlock, err := lockBindingsFile(s.path)
	if err != nil {
		return fmt.Errorf("acquiring IM bindings lock: %w", err)
	}
	defer unlock()
	all, err := s.readAllLocked()
	if err != nil {
		return err
	}
	key := compositeKey(normalizeWorkspace(workspace), adapter)
	if b, ok := all[key]; ok {
		b.LastSessionID = sessionID
		all[key] = b
		return s.writeAllLocked(all)
	}
	return nil // binding not found — no-op
}

func (s *JSONFileBindingStore) readAllLocked() (map[string]ChannelBinding, error) {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return make(map[string]ChannelBinding), nil
		}
		return nil, fmt.Errorf("reading IM bindings: %w", err)
	}
	var raw map[string]ChannelBinding
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parsing IM bindings: %w", err)
	}
	if raw == nil {
		raw = make(map[string]ChannelBinding)
	}
	// Migrate legacy format: keys that don't contain \x00 are old workspace-only keys.
	migrated := false
	for key, binding := range raw {
		if strings.ContainsRune(key, '\x00') {
			continue
		}
		// Legacy key — rebuild with composite key.
		if strings.TrimSpace(binding.Adapter) == "" {
			continue
		}
		newKey := compositeKey(key, binding.Adapter)
		raw[newKey] = binding
		delete(raw, key)
		migrated = true
	}
	if migrated {
		if err := s.writeAllLocked(raw); err != nil {
			debug.Log("im", "bindings migration write failed: %v", err)
		}
	}
	return raw, nil
}

func (s *JSONFileBindingStore) writeAllLocked(bindings map[string]ChannelBinding) error {
	data, err := json.MarshalIndent(bindings, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal IM bindings: %w", err)
	}
	// Use a unique temp file name to avoid collision with other processes
	// that may be writing concurrently (each holds the flock but uses the
	// same .tmp path without the lock in legacy code paths).
	tmp := fmt.Sprintf("%s.tmp.%d", s.path, os.Getpid())
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("writing IM bindings: %w", err)
	}
	return os.Rename(tmp, s.path)
}

func normalizeWorkspace(path string) string {
	return session.NormalizeWorkspacePath(path)
}
