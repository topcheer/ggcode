package im

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/session"
)

// BindingStore persists channel bindings keyed by (workspace, adapter).
type BindingStore interface {
	Save(binding ChannelBinding) error
	Delete(workspace, adapter string) error
	List() ([]ChannelBinding, error)
	ListByWorkspace(workspace string) ([]ChannelBinding, error)
	ListByAdapter(adapter string) ([]ChannelBinding, error)
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
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".ggcode", "im-bindings.json"), nil
}

func (s *JSONFileBindingStore) Save(binding ChannelBinding) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	all, err := s.readAllLocked()
	if err != nil {
		return err
	}
	binding.Workspace = normalizeWorkspace(binding.Workspace)
	if binding.BoundAt.IsZero() {
		binding.BoundAt = time.Now()
	}
	all[compositeKey(binding.Workspace, binding.Adapter)] = binding
	return s.writeAllLocked(all)
}

func (s *JSONFileBindingStore) Delete(workspace, adapter string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
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
		_ = s.writeAllLocked(raw)
	}
	return raw, nil
}

func (s *JSONFileBindingStore) writeAllLocked(bindings map[string]ChannelBinding) error {
	data, err := json.MarshalIndent(bindings, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal IM bindings: %w", err)
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("writing IM bindings: %w", err)
	}
	return os.Rename(tmp, s.path)
}

func normalizeWorkspace(path string) string {
	return session.NormalizeWorkspacePath(path)
}
