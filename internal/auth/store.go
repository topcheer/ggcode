package auth

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	ProviderGitHubCopilot = "github-copilot"
)

type Info struct {
	ProviderID    string    `json:"provider_id"`
	Type          string    `json:"type"`
	AccessToken   string    `json:"access_token,omitempty"`
	RefreshToken  string    `json:"refresh_token,omitempty"`
	EnterpriseURL string    `json:"enterprise_url,omitempty"`
	ExpiresAt     time.Time `json:"expires_at,omitempty"`
	UpdatedAt     time.Time `json:"updated_at"`
}

type Store struct {
	path string
}

func DefaultPath() string {
	return filepath.Join(homeDir(), ".ggcode", "provider_auth.json")
}

func NewStore(path string) *Store {
	path = strings.TrimSpace(path)
	if path == "" {
		path = DefaultPath()
	}
	return &Store{path: path}
}

func DefaultStore() *Store {
	return NewStore(DefaultPath())
}

func (s *Store) Load(providerID string) (*Info, error) {
	all, err := s.loadAll()
	if err != nil {
		return nil, err
	}
	info, ok := all[strings.TrimSpace(providerID)]
	if !ok {
		return nil, nil
	}
	copy := info
	return &copy, nil
}

func (s *Store) Save(info *Info) error {
	if info == nil {
		return fmt.Errorf("auth info is nil")
	}
	providerID := strings.TrimSpace(info.ProviderID)
	if providerID == "" {
		return fmt.Errorf("provider id is empty")
	}
	if strings.TrimSpace(info.Type) == "" {
		return fmt.Errorf("auth type is empty")
	}

	all, err := s.loadAll()
	if err != nil {
		return err
	}
	next := *info
	next.ProviderID = providerID
	next.UpdatedAt = time.Now()
	all[providerID] = next
	return s.saveAll(all)
}

func (s *Store) Delete(providerID string) error {
	all, err := s.loadAll()
	if err != nil {
		return err
	}
	delete(all, strings.TrimSpace(providerID))
	return s.saveAll(all)
}

func (s *Store) HasUsableToken(providerID string) (bool, error) {
	info, err := s.Load(providerID)
	if err != nil || info == nil {
		return false, err
	}
	if strings.TrimSpace(info.AccessToken) == "" {
		return false, nil
	}
	if !info.ExpiresAt.IsZero() && time.Now().After(info.ExpiresAt) {
		return false, nil
	}
	return true, nil
}

func (s *Store) loadAll() (map[string]Info, error) {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]Info{}, nil
		}
		return nil, fmt.Errorf("reading auth store: %w", err)
	}
	if len(data) == 0 {
		return map[string]Info{}, nil
	}
	var all map[string]Info
	if err := json.Unmarshal(data, &all); err != nil {
		return nil, fmt.Errorf("parsing auth store: %w", err)
	}
	if all == nil {
		all = map[string]Info{}
	}
	return all, nil
}

func (s *Store) saveAll(all map[string]Info) error {
	if all == nil {
		all = map[string]Info{}
	}
	data, err := json.MarshalIndent(all, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling auth store: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return fmt.Errorf("creating auth store directory: %w", err)
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("writing auth store: %w", err)
	}
	return os.Rename(tmp, s.path)
}

func homeDir() string {
	if home := os.Getenv("HOME"); strings.TrimSpace(home) != "" {
		return home
	}
	if home, err := os.UserHomeDir(); err == nil && strings.TrimSpace(home) != "" {
		return home
	}
	return "/tmp"
}
