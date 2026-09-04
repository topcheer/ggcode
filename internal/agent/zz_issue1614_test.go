package agent

import (
	"os"
	"path/filepath"
	"testing"
)

// TestIssue1614_NestedModuleFanInSource pins #1614-A: a nested module's
// intra-module imports must be COUNTED under its own module key - the
// #1574 query-side fix alone was a no-op because the counting loop
// classified nested-module imports as external.
func TestIssue1614_NestedModuleFanInSource(t *testing.T) {
	root := t.TempDir()
	// Nested module ggcode-relay: module github.com/topcheer/ggcode-relay.
	relay := filepath.Join(root, "ggcode-relay")
	if err := os.MkdirAll(filepath.Join(relay, "core"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(relay, "go.mod"), []byte("module github.com/topcheer/ggcode-relay\n\ngo 1.27\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(relay, "go.sum"), []byte("\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module github.com/topcheer/ggcode\n\ngo 1.27\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "go.sum"), []byte("\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Six importers of relay/core: the nested-module key must reach them.
	for i := 0; i < 6; i++ {
		dir := filepath.Join(relay, "user"+string(rune('a'+i)))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		src := "package u" + string(rune('a'+i)) + "\n\nimport _ \"github.com/topcheer/ggcode-relay/core\"\n"
		if err := os.WriteFile(filepath.Join(dir, "u.go"), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(relay, "core", "c.go"), []byte("package core\n\nfunc F() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	fanIn := hubComputeFanIn(root, "github.com/topcheer/ggcode")
	got := fanIn["github.com/topcheer/ggcode-relay/core"]
	if got < 5 {
		t.Fatalf("nested-module key must be counted at the source: got %d importers, want >=5 (map %v)", got, fanIn)
	}
}
