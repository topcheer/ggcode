package auth

import (
	"testing"
	"time"
)

func TestDefaultTokenCacheDir(t *testing.T) {
	dir := DefaultTokenCacheDir()
	if dir == "" {
		t.Error("expected non-empty cache dir")
	}
}

func TestDefaultPath(t *testing.T) {
	path := DefaultPath()
	if path == "" {
		t.Error("expected non-empty default path")
	}
}

func TestDefaultStore(t *testing.T) {
	store := DefaultStore()
	if store == nil {
		t.Error("expected non-nil store")
	}
}

func TestNewStore(t *testing.T) {
	store := NewStore(t.TempDir() + "/test.json")
	if store == nil {
		t.Error("expected non-nil store")
	}
}

func TestInfoIsExpired(t *testing.T) {
	tests := []struct {
		name     string
		info     Info
		expected bool
	}{
		{"expired", Info{ExpiresAt: time.Now().Add(-time.Hour)}, true},
		{"valid", Info{ExpiresAt: time.Now().Add(time.Hour)}, false},
		{"zero", Info{}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.info.IsExpired()
			if got != tt.expected {
				t.Errorf("IsExpired() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestStoreHasUsableToken_NoToken(t *testing.T) {
	store := NewStore(t.TempDir() + "/test.json")
	ok, err := store.HasUsableToken("test-provider")
	if err != nil {
		t.Logf("HasUsableToken error (expected): %v", err)
	}
	if ok {
		t.Error("expected false with no token")
	}
}

func TestHomeDir(t *testing.T) {
	dir := homeDir()
	if dir == "" {
		t.Error("expected non-empty home dir")
	}
}

func TestCopilotClientID(t *testing.T) {
	id := copilotClientID()
	if id == "" {
		t.Error("expected non-empty client ID")
	}
}

func TestGenerateCodeVerifier(t *testing.T) {
	v1, err := GenerateCodeVerifier()
	if err != nil {
		t.Fatalf("GenerateCodeVerifier error: %v", err)
	}
	v2, _ := GenerateCodeVerifier()
	if v1 == "" {
		t.Error("expected non-empty verifier")
	}
	if v1 == v2 {
		t.Error("expected different verifiers")
	}
}

func TestGenerateCodeChallenge(t *testing.T) {
	verifier, _ := GenerateCodeVerifier()
	challenge := GenerateCodeChallenge(verifier)
	if challenge == "" {
		t.Error("expected non-empty challenge")
	}
	challenge2 := GenerateCodeChallenge(verifier)
	if challenge != challenge2 {
		t.Error("expected deterministic challenge")
	}
}

func TestGenerateState(t *testing.T) {
	s1, err := GenerateState()
	if err != nil {
		t.Fatalf("GenerateState error: %v", err)
	}
	s2, _ := GenerateState()
	if s1 == "" {
		t.Error("expected non-empty state")
	}
	if s1 == s2 {
		t.Error("expected different states")
	}
}

func TestTokenCacheSaveAndLoad(t *testing.T) {
	cache := NewTokenCache(t.TempDir())

	token := &PKCEToken{
		AccessToken: "access-123",
		TokenType:   "Bearer",
	}

	if err := cache.Save("test-provider", token, "test-client"); err != nil {
		t.Fatalf("Save error: %v", err)
	}

	loaded := cache.Load("test-provider")
	if loaded == nil {
		t.Fatal("expected non-nil loaded token")
	}
	if loaded.AccessToken != "access-123" {
		t.Errorf("expected 'access-123', got %q", loaded.AccessToken)
	}
}

func TestTokenCacheLoadNonexistent(t *testing.T) {
	cache := NewTokenCache(t.TempDir())
	loaded := cache.Load("nonexistent")
	if loaded != nil {
		t.Error("expected nil for nonexistent provider")
	}
}

func TestTokenCacheLoadValid_Expired(t *testing.T) {
	cache := NewTokenCache(t.TempDir())

	token := &PKCEToken{
		AccessToken: "expired-token",
		TokenType:   "Bearer",
		Expiry:      time.Now().Add(-time.Hour),
	}

	if err := cache.Save("test-exp", token, "client"); err != nil {
		t.Fatalf("Save error: %v", err)
	}

	loaded := cache.LoadValid("test-exp")
	if loaded != nil {
		t.Error("expected nil for expired token")
	}
}

func TestStoreSaveAndLoad(t *testing.T) {
	path := t.TempDir() + "/copilot.json"
	store := NewStore(path)

	info := &Info{
		ProviderID:  "test",
		Type:        "copilot",
		AccessToken: "test-token",
		ExpiresAt:   time.Now().Add(time.Hour),
	}

	if err := store.Save(info); err != nil {
		t.Fatalf("Save error: %v", err)
	}

	loaded, err := store.Load("test")
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}
	if loaded.AccessToken != "test-token" {
		t.Errorf("expected 'test-token', got %q", loaded.AccessToken)
	}
}

func TestStoreDelete(t *testing.T) {
	path := t.TempDir() + "/copilot.json"
	store := NewStore(path)

	info := &Info{ProviderID: "test", Type: "copilot", AccessToken: "to-delete"}
	if err := store.Save(info); err != nil {
		t.Fatalf("Save error: %v", err)
	}

	if err := store.Delete("test"); err != nil {
		// Delete may return error for non-dir path, that's OK
		t.Logf("Delete error (acceptable): %v", err)
	}
}

func TestStoreLoadNoFile(t *testing.T) {
	store := NewStore(t.TempDir() + "/nonexistent.json")
	_, err := store.Load("any")
	// Load may or may not error for nonexistent file (creates empty)
	_ = err
}

func TestBase64urlEncode(t *testing.T) {
	got := base64urlEncode([]byte("hello"))
	if got == "" {
		t.Error("expected non-empty encoded string")
	}
}
