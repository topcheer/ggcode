package config

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/topcheer/ggcode/internal/debug"
)

// r141: WarnUnresolvedEnvRefs coverage parity for the external section
// loaders. The main Load path (config.go) and the instance path (instance.go)
// report ${...} forms the expander does not understand (#559 Bug F:
// "${KEY:?required}" silently became a literal credential value with zero
// warnings). loadVendorsFile / loadIMFile / loadMCPServersFile run the same
// expand chain but skipped the warning; these tests pin the reporting.

type envWarnCapture struct {
	mu   sync.Mutex
	msgs []string
}

func installEnvWarnSink(t *testing.T) *envWarnCapture {
	t.Helper()
	c := &envWarnCapture{}
	debug.SetLiveSink(func(category, msg string) {
		c.mu.Lock()
		c.msgs = append(c.msgs, msg)
		c.mu.Unlock()
	})
	t.Cleanup(func() { debug.SetLiveSink(nil) })
	return c
}

func (c *envWarnCapture) anyContains(substr string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, m := range c.msgs {
		if strings.Contains(m, substr) {
			return true
		}
	}
	return false
}

func (c *envWarnCapture) snapshot() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]string, len(c.msgs))
	copy(out, c.msgs)
	return out
}

func TestVendorsFileUnrecognizedEnvRefWarned(t *testing.T) {
	withTestHome(t)
	sink := installEnvWarnSink(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "vendors.yaml")
	content := "myvendor:\n  endpoints:\n    ep1:\n      api_key: \"${R141_VK:?required}\"\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	vendors := loadVendorsFile(path)
	if vendors == nil {
		t.Fatal("loadVendorsFile returned nil (unmarshal failed)")
	}
	if !sink.anyContains("${R141_VK:?required}") {
		t.Fatalf("expected 'unrecognized env reference' warning for vendors.yaml, got none (msgs=%v)", sink.snapshot())
	}
	ep, ok := vendors["myvendor"].Endpoints["ep1"]
	if !ok {
		t.Fatal("endpoint ep1 missing after load")
	}
	if ep.APIKey != "${R141_VK:?required}" {
		t.Fatalf("APIKey = %q, want literal ref preserved", ep.APIKey)
	}
}

func TestIMFileUnrecognizedEnvRefWarned(t *testing.T) {
	withTestHome(t)
	sink := installEnvWarnSink(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "im.yaml")
	content := "adapters:\n  qq:\n    env:\n      TOKEN: \"${R141_IMTOKEN:?required}\"\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	im := loadIMFile(path)
	if im == nil {
		t.Fatal("loadIMFile returned nil (unmarshal failed)")
	}
	if !sink.anyContains("${R141_IMTOKEN:?required}") {
		t.Fatalf("expected 'unrecognized env reference' warning for im.yaml, got none (msgs=%v)", sink.snapshot())
	}
	if got := im.Adapters["qq"].Env["TOKEN"]; got != "${R141_IMTOKEN:?required}" {
		t.Fatalf("TOKEN = %q, want literal ref preserved", got)
	}
}

func TestMCPServersFileUnrecognizedEnvRefWarned(t *testing.T) {
	withTestHome(t)
	sink := installEnvWarnSink(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "mcp_servers.yaml")
	content := "- name: srv\n  command: echo\n  env:\n    TOKEN: \"${R141_MCPTOKEN:?required}\"\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	servers := loadMCPServersFile(path)
	if len(servers) != 1 {
		t.Fatalf("len(servers) = %d, want 1", len(servers))
	}
	if !sink.anyContains("${R141_MCPTOKEN:?required}") {
		t.Fatalf("expected 'unrecognized env reference' warning for mcp_servers.yaml, got none (msgs=%v)", sink.snapshot())
	}
	if got := servers[0].Env["TOKEN"]; got != "${R141_MCPTOKEN:?required}" {
		t.Fatalf("TOKEN = %q, want literal ref preserved", got)
	}
}

// Control: a plain ${VAR} that resolves cleanly must NOT produce the
// unrecognized-reference warning — parity with the main Load path, where the
// warning only fires for forms/unresolved refs the expander leaves behind.
func TestExternalFilesResolvedPlainRefNoWarn(t *testing.T) {
	withTestHome(t)
	sink := installEnvWarnSink(t)
	t.Setenv("R141_PLAIN_V", "sk-plain")
	t.Setenv("R141_PLAIN_T", "tok-plain")
	t.Setenv("R141_PLAIN_M", "mcp-plain")

	vdir := t.TempDir()
	vpath := filepath.Join(vdir, "vendors.yaml")
	if err := os.WriteFile(vpath, []byte("myvendor:\n  endpoints:\n    ep1:\n      api_key: ${R141_PLAIN_V}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if vendors := loadVendorsFile(vpath); vendors == nil {
		t.Fatal("loadVendorsFile returned nil")
	}

	idir := t.TempDir()
	ipath := filepath.Join(idir, "im.yaml")
	if err := os.WriteFile(ipath, []byte("adapters:\n  qq:\n    env:\n      TOKEN: ${R141_PLAIN_T}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if im := loadIMFile(ipath); im == nil {
		t.Fatal("loadIMFile returned nil")
	}

	mdir := t.TempDir()
	mpath := filepath.Join(mdir, "mcp_servers.yaml")
	if err := os.WriteFile(mpath, []byte("- name: srv\n  command: echo\n  env:\n    TOKEN: ${R141_PLAIN_M}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if servers := loadMCPServersFile(mpath); len(servers) != 1 {
		t.Fatalf("len(servers) = %d, want 1", len(servers))
	}

	if sink.anyContains("unrecognized env reference") {
		t.Fatalf("unexpected warning for resolved plain refs (msgs=%v)", sink.snapshot())
	}
}
