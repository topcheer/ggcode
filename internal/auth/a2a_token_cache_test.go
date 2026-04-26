package auth

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestTokenCacheSaveLoad(t *testing.T) {
	dir := t.TempDir()
	cache := NewTokenCache(dir)

	token := &PKCEToken{
		AccessToken:  "gho_test123",
		RefreshToken: "refresh456",
		TokenType:    "bearer",
		Expiry:       time.Now().Add(time.Hour),
		Scope:        "read:user",
	}

	err := cache.Save("github", token, "client-abc")
	if err != nil {
		t.Fatal(err)
	}

	// Verify file content
	data, err := os.ReadFile(filepath.Join(dir, "github.json"))
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("Cached: %s", string(data))

	// Load it back
	loaded := cache.Load("github")
	if loaded == nil {
		t.Fatal("expected non-nil token")
	}
	if loaded.AccessToken != "gho_test123" {
		t.Errorf("expected gho_test123, got %s", loaded.AccessToken)
	}
	if loaded.RefreshToken != "refresh456" {
		t.Errorf("expected refresh456, got %s", loaded.RefreshToken)
	}
}

func TestTokenCacheExpiredNoRefresh(t *testing.T) {
	dir := t.TempDir()
	cache := NewTokenCache(dir)

	token := &PKCEToken{
		AccessToken: "expired-token",
		Expiry:      time.Now().Add(-time.Hour), // expired
	}
	cache.Save("github", token, "client-abc")

	loaded := cache.Load("github")
	if loaded != nil {
		t.Error("expected nil for expired token without refresh_token")
	}
}

func TestTokenCacheExpiredWithRefresh(t *testing.T) {
	dir := t.TempDir()
	cache := NewTokenCache(dir)

	token := &PKCEToken{
		AccessToken:  "expired-but-refreshable",
		RefreshToken: "refresh-token",
		Expiry:       time.Now().Add(-time.Hour),
	}
	cache.Save("github", token, "client-abc")

	loaded := cache.Load("github")
	if loaded == nil {
		t.Error("expired token WITH refresh_token should still be returned")
	}
}

func TestTokenCacheLoadValidExpired(t *testing.T) {
	dir := t.TempDir()
	cache := NewTokenCache(dir)

	token := &PKCEToken{
		AccessToken:  "almost-expired",
		RefreshToken: "",
		Expiry:       time.Now().Add(2 * time.Minute), // within 5-min buffer
	}
	cache.Save("github", token, "client-abc")

	loaded := cache.LoadValid("github")
	if loaded != nil {
		t.Error("token expiring within 5-min buffer should be considered invalid for LoadValid")
	}
}

func TestTokenCacheLoadValidFresh(t *testing.T) {
	dir := t.TempDir()
	cache := NewTokenCache(dir)

	token := &PKCEToken{
		AccessToken: "fresh-token",
		Expiry:      time.Now().Add(time.Hour),
	}
	cache.Save("github", token, "client-abc")

	loaded := cache.LoadValid("github")
	if loaded == nil {
		t.Fatal("expected valid token")
	}
	if loaded.AccessToken != "fresh-token" {
		t.Errorf("expected fresh-token, got %s", loaded.AccessToken)
	}
}

func TestTokenCacheNotExist(t *testing.T) {
	dir := t.TempDir()
	cache := NewTokenCache(dir)

	loaded := cache.Load("nonexistent")
	if loaded != nil {
		t.Error("expected nil for nonexistent provider")
	}
}

func TestTokenCacheDelete(t *testing.T) {
	dir := t.TempDir()
	cache := NewTokenCache(dir)

	token := &PKCEToken{AccessToken: "test", Expiry: time.Now().Add(time.Hour)}
	cache.Save("github", token, "client")
	cache.Delete("github")

	loaded := cache.Load("github")
	if loaded != nil {
		t.Error("expected nil after delete")
	}
}

func TestTokenCacheFilePermissions(t *testing.T) {
	dir := t.TempDir()
	cache := NewTokenCache(dir)

	token := &PKCEToken{AccessToken: "secret", Expiry: time.Now().Add(time.Hour)}
	cache.Save("github", token, "client")

	info, err := os.Stat(filepath.Join(dir, "github.json"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Errorf("expected 0600, got %o", info.Mode().Perm())
	}
}

func TestCacheKeyIsolation(t *testing.T) {
	// Same provider, different clientIDs → different keys
	key1 := CacheKey("github", "client-aaa")
	key2 := CacheKey("github", "client-bbb")
	if key1 == key2 {
		t.Errorf("different clientIDs should produce different keys: %s == %s", key1, key2)
	}
	t.Logf("key1=%s key2=%s", key1, key2)

	// Same provider, same clientID → same key
	key3 := CacheKey("github", "client-aaa")
	if key1 != key3 {
		t.Errorf("same inputs should produce same key")
	}

	// No clientID → just provider name
	key4 := CacheKey("github", "")
	if key4 != "github" {
		t.Errorf("expected 'github', got %s", key4)
	}
}

func TestTokenCacheMultiClientIsolation(t *testing.T) {
	dir := t.TempDir()
	cache := NewTokenCache(dir)

	// Instance A with client-aaa
	tokenA := &PKCEToken{
		AccessToken: "token-for-aaa",
		Expiry:      time.Now().Add(time.Hour),
	}
	cache.Save(CacheKey("github", "client-aaa"), tokenA, "client-aaa")

	// Instance B with client-bbb
	tokenB := &PKCEToken{
		AccessToken: "token-for-bbb",
		Expiry:      time.Now().Add(time.Hour),
	}
	cache.Save(CacheKey("github", "client-bbb"), tokenB, "client-bbb")

	// Load each — should get the right one
	loadedA := cache.LoadValid(CacheKey("github", "client-aaa"))
	if loadedA == nil || loadedA.AccessToken != "token-for-aaa" {
		t.Errorf("expected token-for-aaa, got %v", loadedA)
	}

	loadedB := cache.LoadValid(CacheKey("github", "client-bbb"))
	if loadedB == nil || loadedB.AccessToken != "token-for-bbb" {
		t.Errorf("expected token-for-bbb, got %v", loadedB)
	}
}
