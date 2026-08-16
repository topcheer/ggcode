package wailskit

// Feature tests for GitHub issue #556 (desktop/wailskit side).
//   D: BindIMAdapter/RebindIMAdapter must reject adapters absent from config
//      (ghost bindings persisted to disk but invisible in the UI).

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// bindStub556 records calls; never returns an error so any error observed by
// the wrapper must come from the #556 config validation.
type bindStub556 struct {
	calls []string
}

func (b *bindStub556) BindAdapterToWorkspace(name, ws string) error {
	b.calls = append(b.calls, name+"@"+ws)
	return nil
}

// withTestConfig556 redirects the config loader to a temp HOME for the
// duration of the test (config.ConfigPath() = $HOME/.ggcode/ggcode.yaml;
// there is no dedicated env override for the file path itself).
func withTestConfig556(t *testing.T, yaml string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".ggcode"), 0o755); err != nil {
		t.Fatalf("mkdir test config dir: %v", err)
	}
	path := filepath.Join(dir, ".ggcode", "ggcode.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatalf("write test config: %v", err)
	}
	t.Setenv("HOME", dir)
}

func TestIssue556BindRejectsGhostAdapter(t *testing.T) {
	withTestConfig556(t, "im:\n  adapters:\n    telegram:\n      platform: telegram\n")
	stub := &bindStub556{}

	err := BindIMAdapter("slack", "/tmp/ws", stub)
	if err == nil {
		stubT := t
		stubT.Log(stub.calls)
		t.Fatal("BindIMAdapter accepted an adapter with no config entry (ghost binding)")
	}
	if !strings.Contains(err.Error(), "slack") || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("error does not identify the missing adapter: %v", err)
	}
	if len(stub.calls) != 0 {
		t.Fatalf("manager bind was called despite failed validation: %v", stub.calls)
	}

	err = RebindIMAdapter("slack", "/tmp/ws", stub)
	if err == nil {
		t.Fatal("RebindIMAdapter accepted an adapter with no config entry")
	}
	if len(stub.calls) != 0 {
		t.Fatalf("manager bind was called despite failed validation: %v", stub.calls)
	}
}

func TestIssue556BindAllowsConfiguredAdapter(t *testing.T) {
	withTestConfig556(t, "im:\n  adapters:\n    slack:\n      platform: slack\n")
	stub := &bindStub556{}

	if err := BindIMAdapter("slack", "/tmp/ws", stub); err != nil {
		t.Fatalf("BindIMAdapter rejected a configured adapter: %v", err)
	}
	if len(stub.calls) != 1 {
		t.Fatalf("expected exactly one bind call, got %v", stub.calls)
	}
}

func TestIssue556BindRejectsWhenNoAdaptersConfigured(t *testing.T) {
	withTestConfig556(t, "im:\n  enabled: true\n")
	stub := &bindStub556{}

	if err := BindIMAdapter("anything", "/tmp/ws", stub); err == nil {
		t.Fatal("BindIMAdapter accepted adapter with nil adapters map")
	}
}
