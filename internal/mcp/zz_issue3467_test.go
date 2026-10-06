package mcp

// #3467 probe: the project-gate approval signature must cover EVERY field
// that changes what actually executes - stdio env (client.go flattens it
// straight into cmd.Env: NODE_OPTIONS/LD_PRELOAD/PATH are code-execution
// vectors) and http headers (really sent, incl. Authorization). A repo
// must not be able to mutate them without re-triggering the gate. The
// sig-v2 prefix forces every pre-existing grant to re-approve once.

import (
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
)

func TestIssue3467_EnvChangesSignature(t *testing.T) {
	base := config.MCPServerConfig{Command: "npx", Args: []string{"-y", "docs"}}
	mutated := base
	mutated.Env = map[string]string{"NODE_OPTIONS": "--require /tmp/x.js"}
	if serverSignature(base) == serverSignature(mutated) {
		t.Fatal("a repo adding env under an approved command+args must re-trigger the gate")
	}
	if !strings.Contains(serverSignature(mutated), "NODE_OPTIONS") {
		t.Fatalf("env must be part of the signature, got %q", serverSignature(mutated))
	}
}

func TestIssue3467_HeadersChangesHTTPSignature(t *testing.T) {
	base := config.MCPServerConfig{Type: "http", URL: "https://x/mcp"}
	mutated := base
	mutated.Headers = map[string]string{"Authorization": "Bearer other"}
	if serverSignature(base) == serverSignature(mutated) {
		t.Fatal("a repo swapping headers under an approved URL must re-trigger the gate")
	}
}

func TestIssue3467_SignatureStableAndVersioned(t *testing.T) {
	// Same env content -> same signature regardless of map order.
	a := config.MCPServerConfig{Command: "npx", Env: map[string]string{"A": "1", "B": "2"}}
	b := config.MCPServerConfig{Command: "npx", Env: map[string]string{"B": "2", "A": "1"}}
	if serverSignature(a) != serverSignature(b) {
		t.Fatalf("map iteration order must not leak into the signature: %q vs %q", serverSignature(a), serverSignature(b))
	}
	// v1 grant strings no longer match: every existing approval re-gates once.
	if strings.HasPrefix(serverSignature(a), "stdio:") {
		t.Fatal("signature must carry the sig-v2 prefix so v1 grants are invalidated")
	}
	if !strings.HasPrefix(serverSignature(a), "sig-v2:stdio:") {
		t.Fatalf("expected sig-v2:stdio: prefix, got %q", serverSignature(a))
	}
	// Bare server (no env): still bare form after the prefix.
	bare := config.MCPServerConfig{Command: "npx"}
	if got := serverSignature(bare); strings.Contains(got, "|") {
		t.Fatalf("no-env server must not carry a kv suffix: %q", got)
	}
}
