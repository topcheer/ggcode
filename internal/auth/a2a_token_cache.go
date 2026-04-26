package auth

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// TokenCache persists OAuth2 tokens to disk so they survive restarts.
// File location: ~/.ggcode/oauth-tokens/{provider}.json
type TokenCache struct {
	dir string
	mu  sync.Mutex
}

// tokenEntry is the on-disk format for a cached token.
type tokenEntry struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	TokenType    string    `json:"token_type"`
	Expiry       time.Time `json:"expiry,omitempty"`
	Scope        string    `json:"scope,omitempty"`
	ClientID     string    `json:"client_id"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// NewTokenCache creates a token cache in the given directory.
func NewTokenCache(dir string) *TokenCache {
	return &TokenCache{dir: dir}
}

// DefaultTokenCacheDir returns the default cache directory.
func DefaultTokenCacheDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".ggcode", "oauth-tokens")
}

// Save writes a token to the cache.
func (tc *TokenCache) Save(provider string, token *PKCEToken, clientID string) error {
	tc.mu.Lock()
	defer tc.mu.Unlock()

	if err := os.MkdirAll(tc.dir, 0700); err != nil {
		return err
	}

	entry := tokenEntry{
		AccessToken:  token.AccessToken,
		RefreshToken: token.RefreshToken,
		TokenType:    token.TokenType,
		Expiry:       token.Expiry,
		Scope:        token.Scope,
		ClientID:     clientID,
		UpdatedAt:    time.Now(),
	}

	data, err := json.MarshalIndent(entry, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(tc.path(provider), data, 0600)
}

// Load reads a cached token for the given provider.
// Returns nil if no cache exists or if the token is expired.
func (tc *TokenCache) Load(provider string) *PKCEToken {
	tc.mu.Lock()
	defer tc.mu.Unlock()

	data, err := os.ReadFile(tc.path(provider))
	if err != nil {
		return nil
	}

	var entry tokenEntry
	if err := json.Unmarshal(data, &entry); err != nil {
		return nil
	}

	token := &PKCEToken{
		AccessToken:  entry.AccessToken,
		RefreshToken: entry.RefreshToken,
		TokenType:    entry.TokenType,
		Expiry:       entry.Expiry,
		Scope:        entry.Scope,
	}

	// Return nil if expired (no refresh token to revive it)
	if !token.Expiry.IsZero() && time.Now().After(token.Expiry) && token.RefreshToken == "" {
		return nil
	}

	return token
}

// LoadValid reads a cached token that is still valid (not expired).
// Returns nil if expired or no cache.
func (tc *TokenCache) LoadValid(provider string) *PKCEToken {
	token := tc.Load(provider)
	if token == nil {
		return nil
	}

	// Check if expired (with 5-minute buffer)
	if !token.Expiry.IsZero() && time.Now().Add(5*time.Minute).After(token.Expiry) {
		// Has refresh token → might still be usable
		if token.RefreshToken == "" {
			return nil
		}
	}

	return token
}

// Delete removes a cached token.
func (tc *TokenCache) Delete(provider string) error {
	return os.Remove(tc.path(provider))
}

func (tc *TokenCache) path(provider string) string {
	return filepath.Join(tc.dir, provider+".json")
}
