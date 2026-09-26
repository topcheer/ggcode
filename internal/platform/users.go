package platform

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// pbkdf2Iterations balances login latency against offline-crack resistance.
const pbkdf2Iterations = 100_000

// MinPasswordLength is the weakest accepted password.
const MinPasswordLength = 8

// User is one registry entry. Only the PBKDF2 hash is persisted.
type User struct {
	Name      string    `json:"name"`
	Salt      string    `json:"salt"` // hex
	Hash      string    `json:"hash"` // hex
	Admin     bool      `json:"admin,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// UserStore is the shared user registry (users.json under the platform dir).
type UserStore struct {
	mu    sync.Mutex
	path  string
	Users []User `json:"users"`
}

// LoadUserStore loads users.json; a missing file yields an empty store so
// first-run `platform user add` works without ceremony.
func LoadUserStore(path string) (*UserStore, error) {
	s := &UserStore{path: path}
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return s, nil
		}
		return nil, fmt.Errorf("platform: read users: %w", err)
	}
	if err := json.Unmarshal(b, s); err != nil {
		return nil, fmt.Errorf("platform: parse users %s: %w", path, err)
	}
	return s, nil
}

// Save persists the registry with 0600 permissions.
func (s *UserStore) Save() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return fmt.Errorf("platform: user dir: %w", err)
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(s.path, b, 0o600); err != nil {
		return fmt.Errorf("platform: write users: %w", err)
	}
	return nil
}

// Add registers name/password (min length enforced, duplicates rejected).
func (s *UserStore) Add(name, password string, admin bool) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("platform: empty username")
	}
	if len(password) < MinPasswordLength {
		return fmt.Errorf("platform: password must be at least %d chars", MinPasswordLength)
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return fmt.Errorf("platform: salt: %w", err)
	}
	hash, err := pbkdf2.Key(sha256.New, password, salt, pbkdf2Iterations, 32)
	if err != nil {
		return fmt.Errorf("platform: pbkdf2: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, u := range s.Users {
		if u.Name == name {
			return fmt.Errorf("platform: user %q already exists", name)
		}
	}
	s.Users = append(s.Users, User{
		Name: name, Salt: hex.EncodeToString(salt), Hash: hex.EncodeToString(hash),
		Admin: admin, CreatedAt: time.Now().UTC(),
	})
	return nil
}

// Remove deletes a user by name.
func (s *UserStore) Remove(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, u := range s.Users {
		if u.Name == name {
			s.Users = append(s.Users[:i], s.Users[i+1:]...)
			return nil
		}
	}
	return fmt.Errorf("platform: user %q not found", name)
}

// Get returns a copy of the user record.
func (s *UserStore) Get(name string) (User, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, u := range s.Users {
		if u.Name == name {
			return u, true
		}
	}
	return User{}, false
}

// Authenticate verifies name/password in constant time.
func (s *UserStore) Authenticate(name, password string) (User, bool) {
	salt, err := hex.DecodeString(s.saltLocked(name))
	if err != nil {
		return User{}, false
	}
	hash, err := pbkdf2.Key(sha256.New, password, salt, pbkdf2Iterations, 32)
	if err != nil {
		return User{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, u := range s.Users {
		if u.Name != name {
			continue
		}
		want, err := hex.DecodeString(u.Hash)
		if err != nil {
			return User{}, false
		}
		if string(hash) == string(want) {
			return u, true
		}
		return User{}, false
	}
	return User{}, false
}

// saltLocked returns the stored salt hex without exposing the record.
func (s *UserStore) saltLocked(name string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, u := range s.Users {
		if u.Name == name {
			return u.Salt
		}
	}
	return ""
}
