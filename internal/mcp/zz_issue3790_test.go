package mcp

// #3790 companion tests: sse servers must be signed by URL like http/ws.
// Pre-fix, sse fell to the stdio default branch whose Command/Args are
// empty for sse entries, so EVERY sse server signed as `sig-v2:stdio:[""]`
// and the migration dedup silently dropped all but the first of multiple
// different-URL sse servers (deterministic data loss).

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

// Case-variant sse types (migrated lowercase vs explicit "SSE") must dedup
// against each other, same as http/ws (#1276 review follow-up parity).
func TestIssue3790_SSESignatureCaseInsensitive(t *testing.T) {
	pairs := [][2]config.MCPServerConfig{
		{
			{Type: "SSE", URL: "https://x/sse"},
			{Type: "sse", URL: "https://x/sse"},
		},
		{
			{Type: " sse ", URL: "https://x/sse"}, // transport normalizer trims
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

// End-to-end: two different-URL sse servers in ~/.claude.json must BOTH
// survive migration dedup (pre-fix the second was dropped as duplicate).
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
