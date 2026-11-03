package auth

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/util"
)

type TokenCache struct {
	dir string
	mu  sync.Mutex
}

type tokenEntry struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	TokenType    string    `json:"token_type"`
	Expiry       time.Time `json:"expiry,omitempty"`
	Scope        string    `json:"scope,omitempty"`
	ClientID     string    `json:"client_id"`
	UpdatedAt    time.Time `json:"updated_at"`
}

func NewTokenCache(dir string) *TokenCache {
	return &TokenCache{dir: dir}
}

func DefaultTokenCacheDir() string {
	return filepath.Join(util.HomeDir(), ".ggcode", "oauth-tokens")
}

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

	path := tc.path(provider)
	f, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	if _, err := f.Write(data); err != nil {
		f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}

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

	if !token.Expiry.IsZero() && time.Now().After(token.Expiry) && token.RefreshToken == "" {
		return nil
	}

	return token
}

func (tc *TokenCache) LoadValid(provider string) *PKCEToken {
	token := tc.Load(provider)
	if token == nil {
		return nil
	}

	if !token.Expiry.IsZero() && time.Now().Add(5*time.Minute).After(token.Expiry) {
		if token.RefreshToken == "" {
			return nil
		}
	}

	return token
}

func (tc *TokenCache) Delete(provider string) error {
	tc.mu.Lock()
	defer tc.mu.Unlock()
	return os.Remove(tc.path(provider))
}

func (tc *TokenCache) path(provider string) string {
	return filepath.Join(tc.dir, provider+".json")
}

func CacheKey(provider, clientID string) string {
	if clientID == "" {
		return provider
	}
	suffix := clientID
	if len(suffix) > 12 {
		suffix = suffix[:12]
	}
	return provider + "-" + suffix
}
