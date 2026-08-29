package mcp

// Regression test for GitHub issue #1276: Claude-config migration silently
// dropped ws servers (fully supported by client+install) and sse servers
// (unsupported) with no warning; even stdio/http entries with missing fields
// vanished silently.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
)

func writeClaudeConfig(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

const issue1276Config = `{
  "mcpServers": {
    "ws-server": {"type": "ws", "url": "wss://example.com/mcp"},
    "sse-server": {"type": "sse", "url": "https://example.com/sse"},
    "stdio-server": {"type": "stdio", "command": "/usr/local/bin/mcp-stdio", "args": ["--flag"]},
    "http-server": {"type": "http", "url": "https://example.com/mcp"},
    "broken-stdio": {"type": "stdio"},
    "broken-http": {"type": "http"},
    "broken-ws": {"type": "ws"}
  }
}`

func TestMigrationWSPassesSSEWarnsDropFieldsWarn(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, ".mcp.json")
	writeClaudeConfig(t, src, issue1276Config)

	servers, warnings, err := loadClaudeServers(migrationSource{Path: src, Source: "claude-project"})
	if err != nil {
		t.Fatal(err)
	}

	got := map[string]bool{}
	for _, s := range servers {
		got[s.Name] = true
	}
	// #1276: ws is first-class - must be migrated, not dropped.
	if !got["ws-server"] {
		t.Fatal("#1276: ws server must migrate (client fully supports ws)")
	}
	if !got["stdio-server"] || !got["http-server"] {
		t.Fatal("stdio/http servers must keep migrating")
	}
	// sse stays dropped (no client support) but must now warn.
	if got["sse-server"] {
		t.Fatal("sse must remain dropped until the client supports it")
	}
	for _, name := range []string{"broken-stdio", "broken-http", "broken-ws"} {
		if got[name] {
			t.Fatalf("%s must be dropped (missing required field)", name)
		}
	}

	joined := strings.Join(warnings, "\n")
	for _, want := range []string{
		`sse-server"`, "unsupported transport",
		`broken-stdio"`, "requires a command",
		`broken-http"`, "requires a URL",
		`broken-ws"`, "requires a URL",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("warnings must mention %q, got:\n%s", want, joined)
		}
	}
	if strings.Contains(joined, "ws-server") {
		t.Fatal("ws with a valid URL must not produce a warning")
	}
}

// TestMigrationWSSignatureDistinct: ws and http servers with the same URL
// must have distinct signatures (transport-prefixed), and a ws entry must
// not collide with the stdio default signature.
func TestMigrationWSSignatureDistinct(t *testing.T) {
	ws := config.MCPServerConfig{Type: "ws", URL: "wss://x/mcp"}
	httpS := config.MCPServerConfig{Type: "http", URL: "wss://x/mcp"}
	if serverSignature(ws) == serverSignature(httpS) {
		t.Fatalf("ws and http signatures must differ: %q", serverSignature(ws))
	}
	if !strings.HasPrefix(serverSignature(ws), "ws:") {
		t.Fatalf("ws signature must be ws-prefixed, got %q", serverSignature(ws))
	}
}
