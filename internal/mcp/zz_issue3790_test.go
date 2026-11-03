package mcp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
)

func TestIssue3790_SSESignatureURLBased(t *testing.T) {
	a := config.MCPServerConfig{Type: "sse", URL: "https://a.example/sse"}
	b := config.MCPServerConfig{Type: "sse", URL: "https://b.example/sse"}
	sa, sb := serverSignature(a), serverSignature(b)
	if !strings.HasPrefix(sa, "sig-v2:sse:") {
		t.Fatalf("sse signature must be URL-based sse-prefixed, got %q", sa)
	}
	if sa == sb {
		t.Fatalf("different-URL sse servers must have different signatures, both %q", sa)
	}
	if sa == serverSignature(config.MCPServerConfig{Type: "stdio"}) {
		t.Fatal("sse signature must not collide with the empty-stdio signature")
	}
}

func TestIssue3790_SSESignatureCaseInsensitive(t *testing.T) {
	pairs := [][2]config.MCPServerConfig{
		{
			{Type: "SSE", URL: "https://x/sse"},
			{Type: "sse", URL: "https://x/sse"},
		},
		{
			{Type: " sse ", URL: "https://x/sse"},
			{Type: "sse", URL: "https://x/sse"},
		},
	}
	for i, p := range pairs {
		if serverSignature(p[0]) != serverSignature(p[1]) {
			t.Fatalf("pair %d: case-variant sse types must yield identical signatures: %q vs %q",
				i, serverSignature(p[0]), serverSignature(p[1]))
		}
	}
}

func TestIssue3790_TwoSSEServersBothMigrate(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	project := filepath.Join(tmp, "project")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	writeClaudeConfig(t, filepath.Join(home, ".claude.json"), `{
  "mcpServers": {
    "sse-one": {"type": "sse", "url": "https://one.example/sse"},
    "sse-two": {"type": "sse", "url": "https://two.example/sse"}
  }
}`)
	merged, _ := MergeStartupServers(project, nil)
	found := map[string]bool{}
	for _, s := range merged {
		found[strings.TrimSpace(s.URL)] = true
	}
	for _, want := range []string{"https://one.example/sse", "https://two.example/sse"} {
		if !found[want] {
			t.Errorf("sse server %s was dropped by dedup (both must migrate)", want)
		}
	}
}
