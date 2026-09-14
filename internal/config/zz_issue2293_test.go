package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// #2293: instance keys.env must feed the resolver map, never the process
// environment. The old wholesale os.Setenv leg leaked every instance key
// to child commands (`env` dumped them) and MCP subprocesses.
func TestIssue2293InstanceKeysStayOutOfProcessEnv(t *testing.T) {
	dir := t.TempDir()
	key := "GGCODE_I_2293_PROBE"
	t.Setenv(key, "") // ensure absent-ish; we assert the real env stays unset
	os.Unsetenv(key)

	if err := os.WriteFile(filepath.Join(dir, "keys.env"), []byte(key+"=supersecret\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := LoadInstanceKeysEnv(dir); err != nil {
		t.Fatal(err)
	}
	if _, exists := os.LookupEnv(key); exists {
		t.Fatal("instance key must NOT be written to the process environment")
	}
	if p := instanceKeysEnvPointer.Load(); p == nil || (*p)[key] != "supersecret" {
		t.Fatalf("instance key must land in the resolver map, got %v", p)
	}
	// and the resolver serves it (instance wins)
	if got, ok := runtimeEnvLookup(nil)(key); !ok || got != "supersecret" {
		t.Fatalf("resolver must serve the instance key, got %q ok=%v", got, ok)
	}
}

func TestIssue2293EmptyDirNoop(t *testing.T) {
	before := 0
	if p := instanceKeysEnvPointer.Load(); p != nil {
		before = len(*p)
	}
	if err := LoadInstanceKeysEnv(""); err != nil {
		t.Fatal(err)
	}
	after := 0
	if p := instanceKeysEnvPointer.Load(); p != nil {
		after = len(*p)
	}
	if after != before {
		t.Fatal("empty dir must be a no-op")
	}
}

// keys.env parsing shape reused: export prefix tolerated
func TestIssue2293ExportPrefixTolerated(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "keys.env"), []byte("export GGCODE_I_2293_B=v2\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := LoadInstanceKeysEnv(dir); err != nil {
		t.Fatal(err)
	}
	if p := instanceKeysEnvPointer.Load(); p == nil || (*p)["GGCODE_I_2293_B"] != "v2" {
		t.Fatalf("export-prefixed key must parse, got %v", p)
	}
	_ = strings.TrimSpace("")
}
