package plugin

import (
	"context"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/mcp"
	"github.com/topcheer/ggcode/internal/tool"
)

// Regression test for issue #315: multiple OAuth MCP servers must be able to
// be pending simultaneously without overwriting each other.
func TestPendingOAuthMultiServerCoexistence(t *testing.T) {
	mgr := NewMCPManager(nil, tool.NewRegistry())
	a := &MCPOAuthRequiredError{ServerName: "serverA", Handler: &mcp.OAuthHandler{}}
	b := &MCPOAuthRequiredError{ServerName: "serverB", Handler: &mcp.OAuthHandler{}}

	mgr.mu.Lock()
	if mgr.pendingOAuth == nil {
		mgr.pendingOAuth = make(map[string]*MCPOAuthRequiredError)
	}
	mgr.pendingOAuth["serverA"] = a
	mgr.pendingOAuth["serverB"] = b
	mgr.mu.Unlock()

	if got := mgr.PendingOAuthByName("serverA"); got != a {
		t.Fatalf("PendingOAuthByName(serverA) = %v, want entry for serverA", got)
	}
	if got := mgr.PendingOAuthByName("serverB"); got != b {
		t.Fatalf("PendingOAuthByName(serverB) = %v, want entry for serverB", got)
	}
	if got := mgr.PendingOAuthByName("missing"); got != nil {
		t.Fatalf("PendingOAuthByName(missing) = %v, want nil", got)
	}

	// Legacy single accessor must not lose the other server.
	if got := mgr.PendingOAuth(); got == nil || (got.ServerName != "serverA" && got.ServerName != "serverB") {
		t.Fatalf("PendingOAuth() = %v, want one of the two pending servers", got)
	}

	// Snapshot flags both servers as OAuthRequired.
	servers := []config.MCPServerConfig{
		{Name: "serverA", Type: "http", URL: "http://a.invalid"},
		{Name: "serverB", Type: "http", URL: "http://b.invalid"},
	}
	for _, s := range servers {
		mgr.mu.Lock()
		mgr.plugins = append(mgr.plugins, NewMCPPlugin(s))
		mgr.mu.Unlock()
	}
	snap := mgr.Snapshot()
	for _, info := range snap {
		if !info.OAuthRequired {
			t.Errorf("snapshot: server %s should have OAuthRequired=true", info.Name)
		}
	}

	// Clearing one entry must leave the other pending (both can complete auth).
	mgr.ClearPendingOAuth("serverA")
	if got := mgr.PendingOAuthByName("serverA"); got != nil {
		t.Fatalf("after ClearPendingOAuth(serverA), entry should be nil, got %v", got)
	}
	if got := mgr.PendingOAuthByName("serverB"); got != b {
		t.Fatalf("after ClearPendingOAuth(serverA), serverB entry must survive, got %v", got)
	}

	mgr.ClearPendingOAuth("")
	if got := mgr.PendingOAuthByName("serverB"); got != b {
		t.Fatalf("ClearPendingOAuth(\"\") must be a no-op, got %v", got)
	}
}

// Regression test for issue #314: Reload removing (or changing) a server must
// clear its pending OAuth entry so a deleted server cannot complete OAuth.
func TestReloadClearsPendingOAuthForRemovedServer(t *testing.T) {
	mgr := NewMCPManager([]config.MCPServerConfig{
		{Name: "oauthA", Type: "http", URL: "http://a.invalid"},
		{Name: "oauthB", Type: "http", URL: "http://b.invalid"},
	}, tool.NewRegistry())

	pending := &MCPOAuthRequiredError{ServerName: "oauthA", Handler: &mcp.OAuthHandler{}}
	mgr.mu.Lock()
	mgr.pendingOAuth = map[string]*MCPOAuthRequiredError{"oauthA": pending, "oauthB": {ServerName: "oauthB"}}
	mgr.mu.Unlock()

	// Reload with oauthA removed, oauthB kept.
	mgr.Reload(context.Background(), []config.MCPServerConfig{
		{Name: "oauthB", Type: "http", URL: "http://b.invalid"},
	})

	if got := mgr.PendingOAuthByName("oauthA"); got != nil {
		t.Fatalf("after removing oauthA, its pending OAuth entry must be cleared, got %v", got)
	}
	if got := mgr.PendingOAuthByName("oauthB"); got == nil {
		t.Fatal("pending OAuth entry for kept server oauthB must survive reload")
	}

	// Reload with a changed config for oauthB must also clear its entry.
	mgr.Reload(context.Background(), []config.MCPServerConfig{
		{Name: "oauthB", Type: "http", URL: "http://b2.invalid"},
	})
	if got := mgr.PendingOAuthByName("oauthB"); got != nil {
		t.Fatalf("after changing oauthB config, its stale pending OAuth entry must be cleared, got %v", got)
	}
}
