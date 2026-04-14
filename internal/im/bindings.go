package im

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/session"
)

type BindingStore interface {
	Load(workspace string) (*ChannelBinding, error)
	Save(binding ChannelBinding) error
	Delete(workspace string) error
	List() ([]ChannelBinding, error)
}

type MemoryBindingStore struct {
	mu       sync.RWMutex
	bindings map[string]ChannelBinding
}

func NewMemoryBindingStore() *MemoryBindingStore {
	return &MemoryBindingStore{bindings: make(map[string]ChannelBinding)}
}

func (s *MemoryBindingStore) Load(workspace string) (*ChannelBinding, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	binding, ok := s.bindings[normalizeWorkspace(workspace)]
	if !ok {
		return nil, nil
	}
	copy := binding
	return &copy, nil
}

func (s *MemoryBindingStore) Save(binding ChannelBinding) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	binding.Workspace = normalizeWorkspace(binding.Workspace)
	if binding.BoundAt.IsZero() {
		binding.BoundAt = time.Now()
	}
	s.bindings[binding.Workspace] = binding
	return nil
}

func (s *MemoryBindingStore) Delete(workspace string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.bindings, normalizeWorkspace(workspace))
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

func (s *JSONFileBindingStore) Load(workspace string) (*ChannelBinding, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	all, err := s.readAllLocked()
	if err != nil {
		return nil, err
	}
	binding, ok := all[normalizeWorkspace(workspace)]
	if !ok {
		return nil, nil
	}
	copy := binding
	return &copy, nil
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
	all[binding.Workspace] = binding
	return s.writeAllLocked(all)
}

func (s *JSONFileBindingStore) Delete(workspace string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	all, err := s.readAllLocked()
	if err != nil {
		return err
	}
	delete(all, normalizeWorkspace(workspace))
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

func (s *JSONFileBindingStore) readAllLocked() (map[string]ChannelBinding, error) {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return make(map[string]ChannelBinding), nil
		}
		return nil, fmt.Errorf("reading IM bindings: %w", err)
	}
	var bindings map[string]ChannelBinding
	if err := json.Unmarshal(data, &bindings); err != nil {
		return nil, fmt.Errorf("parsing IM bindings: %w", err)
	}
	if bindings == nil {
		bindings = make(map[string]ChannelBinding)
	}
	return bindings, nil
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
