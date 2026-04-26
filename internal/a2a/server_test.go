package a2a

import (
	"net/http/httptest"
	"testing"
)

func TestAuthenticateMultipleAPIKeys(t *testing.T) {
	srv := &Server{
		apiKeys: []string{"key-alpha", "key-beta", "key-gamma"},
	}

	tests := []struct {
		key      string
		expected bool
	}{
		{"key-alpha", true},
		{"key-beta", true},
		{"key-gamma", true},
		{"key-delta", false},
		{"", false},
		{"key-alpha-extra", false},
	}

	for _, tt := range tests {
		r := httptest.NewRequest("POST", "/", nil)
		if tt.key != "" {
			r.Header.Set("X-API-Key", tt.key)
		}
		got := srv.authenticate(r)
		if got != tt.expected {
			t.Errorf("key=%q expected=%v got=%v", tt.key, tt.expected, got)
		}
	}
}

func TestAuthenticateMergedAPIKeyAndAPIKeys(t *testing.T) {
	// Simulate NewServer merging APIKey + APIKeys
	srv := &Server{
		apiKeys: []string{"new-key-1", "new-key-2", "legacy-key"},
	}

	// Both legacy and new keys should work
	for _, key := range []string{"legacy-key", "new-key-1", "new-key-2"} {
		r := httptest.NewRequest("POST", "/", nil)
		r.Header.Set("X-API-Key", key)
		if !srv.authenticate(r) {
			t.Errorf("key=%q should authenticate", key)
		}
	}

	// Wrong key
	r := httptest.NewRequest("POST", "/", nil)
	r.Header.Set("X-API-Key", "wrong")
	if srv.authenticate(r) {
		t.Error("wrong key should not authenticate")
	}
}

func TestAuthenticateNoKeys(t *testing.T) {
	srv := &Server{
		apiKeys: nil,
	}

	// No keys → no API key auth, but no auth at all means open access → allow
	r := httptest.NewRequest("POST", "/", nil)
	if !srv.authenticate(r) {
		t.Error("no auth configured should allow (open mode)")
	}
}

func TestAuthenticateSingleKey(t *testing.T) {
	srv := &Server{
		apiKeys: []string{"only-key"},
	}

	r := httptest.NewRequest("POST", "/", nil)
	r.Header.Set("X-API-Key", "only-key")
	if !srv.authenticate(r) {
		t.Error("single key should authenticate")
	}

	r2 := httptest.NewRequest("POST", "/", nil)
	r2.Header.Set("X-API-Key", "wrong")
	if srv.authenticate(r2) {
		t.Error("wrong key should not authenticate")
	}
}

func TestInstanceDisplayName(t *testing.T) {
	tests := []struct {
		endpoint  string
		workspace string
		expected  string
	}{
		{"127.0.0.1:12345", "/Users/dev/projects/order-service", "order-service:12345"},
		{"127.0.0.1:54321", "/Users/dev/projects/order-service", "order-service:54321"},
		{"10.0.0.1:8080", "/home/user/gateway", "gateway:8080"},
		{"invalid", "/path/to/project", "project"},
		{"", "/path/to/project", "project"},
	}

	for _, tt := range tests {
		inst := InstanceInfo{Workspace: tt.workspace, Endpoint: tt.endpoint}
		got := inst.DisplayName()
		if got != tt.expected {
			t.Errorf("DisplayName(%q, %q) = %q, want %q", tt.workspace, tt.endpoint, got, tt.expected)
		}
	}
}
