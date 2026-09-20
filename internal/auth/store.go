package auth

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/util"
)

const (
	ProviderGitHubCopilot = "github-copilot"
	ProviderAnthropic     = "anthropic"
)

type Info struct {
	ProviderID    string    `json:"provider_id"`
	Type          string    `json:"type"`
	AccessToken   string    `json:"access_token,omitempty"`
	RefreshToken  string    `json:"refresh_token,omitempty"`
	EnterpriseURL string    `json:"enterprise_url,omitempty"`
	OAuthIssuer   string    `json:"oauth_issuer,omitempty"`
	OAuthResource string    `json:"oauth_resource,omitempty"`
	OAuthClientID string    `json:"oauth_client_id,omitempty"`
	OAuthSecret   string    `json:"oauth_client_secret,omitempty"`
	ExpiresAt     time.Time `json:"expires_at,omitempty"`
	UpdatedAt     time.Time `json:"updated_at"`
}

type Store struct {
	path string
}

var (
	// storeMuRegistryMu guards storeMuRegistry itself.
	storeMuRegistryMu sync.Mutex
	// storeMuRegistry keys store paths to a mutex shared by EVERY Store
	// instance bound to that path (#1505 case 3): DefaultStore() hands out a
	// fresh instance per call, so a per-instance s.mu never serialized
	// anything — overlapping background-refresh, login and panel saves ran
	// interleaved load-modify-write cycles that silently rolled back rotated
	// refresh tokens or dropped concurrent providers' entries.
	storeMuRegistry = map[string]*sync.Mutex{}
)

// muFor returns the mutex shared by all Store instances using the same
// path, so separate instances (DefaultStore is per-call) still serialize
// their mutations.
func muFor(path string) *sync.Mutex {
	storeMuRegistryMu.Lock()
	defer storeMuRegistryMu.Unlock()
	if m, ok := storeMuRegistry[path]; ok {
		return m
	}
	m := &sync.Mutex{}
	storeMuRegistry[path] = m
	return m
}

func DefaultPath() string {
	return filepath.Join(util.HomeDir(), ".ggcode", "provider_auth.json")
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
	m := muFor(s.path)
	m.Lock()
	defer m.Unlock()
	return s.loadLocked(providerID)
}

// loadLocked is the internal unlocked version of Load.
func (s *Store) loadLocked(providerID string) (*Info, error) {
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
	m := muFor(s.path)
	m.Lock()
	defer m.Unlock()
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
	return s.withFileLock(s.saveAll, all)
}

func (s *Store) Delete(providerID string) error {
	m := muFor(s.path)
	m.Lock()
	defer m.Unlock()
	all, err := s.loadAll()
	if err != nil {
		return err
	}
	delete(all, strings.TrimSpace(providerID))
	return s.withFileLock(s.saveAll, all)
}

// withFileLock takes a best-effort cross-process exclusive lock around a
// mutating store write (#1505 case 3): the in-process shared mutex cannot
// see the desktop/daemon/TUI processes the product explicitly supports
// running in parallel, whose interleaved load-modify-write cycles lose
// updates the same way. Following the FileLock contract (#1337), lock
// failure fails OPEN: the atomic rename still prevents torn files, only
// update serialization is lost, and persistence never hard-fails.
func (s *Store) withFileLock(fn func(map[string]Info) error, all map[string]Info) error {
	unlock, err := util.FileLock(s.path + ".lock")
	if err != nil {
		debug.Log("auth", "auth store lock unavailable (proceeding unlocked): %v", err)
		return fn(all)
	}
	defer unlock()
	return fn(all)
}

// IsExpired returns true if the token is expired or will expire within 5 minutes.
func (i *Info) IsExpired() bool {
	if i == nil {
		return true
	}
	if i.ExpiresAt.IsZero() {
		return false
	}
	return time.Now().Add(5 * time.Minute).After(i.ExpiresAt)
}

func (s *Store) HasUsableToken(providerID string) (bool, error) {
	m := muFor(s.path)
	m.Lock()
	defer m.Unlock()
	info, err := s.loadLocked(providerID)
	if err != nil || info == nil {
		return false, err
	}
	if strings.TrimSpace(info.AccessToken) == "" {
		return false, nil
	}
	if !info.ExpiresAt.IsZero() && time.Now().After(info.ExpiresAt) {
		// Add 30s clock skew tolerance - if token expired less than 30s ago,
		// consider it still valid to avoid race conditions at the exact expiration moment
		if time.Now().Sub(info.ExpiresAt) < 30*time.Second {
			return true, nil
		}
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
	// #1505 case 3: the fixed ".tmp" name let two writers (processes or
	// unlocked instances) O_TRUNC the same scratch file concurrently, mixing
	// two JSON documents before rename and permanently corrupting the store.
	// CreateTemp gives every writer its own scratch file, created 0600.
	f, err := os.CreateTemp(filepath.Dir(s.path), filepath.Base(s.path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("creating auth store temp file: %w", err)
	}
	tmp := f.Name()
	if _, err := f.Write(append(data, '\n')); err != nil {
		f.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("writing auth store: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("writing auth store: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("renaming auth store: %w", err)
	}
	return nil
}
