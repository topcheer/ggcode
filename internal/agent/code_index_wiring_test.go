package agent

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/topcheer/ggcode/internal/tool"
)

// Every agent built on a registry must share that registry's code index so
// its edits MarkDirty it. Before this, only the TUI called
// SetCodeIndexManager, so headless / daemon / ACP / A2A agents never
// invalidated the index and code_search stayed stale until the 5-minute scan.
func TestNewAgentWiresRegistryCodeIndex(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	reg := tool.NewRegistry()
	if err := tool.RegisterBuiltinTools(reg, nil, dir, nil, nil); err != nil {
		t.Fatal(err)
	}
	cim := reg.CodeIndex()
	if cim == nil {
		t.Fatal("builtin registry must own a code index manager")
	}

	a := NewAgent(&mockProvider{}, reg, "", 1)
	if a.CodeIndexManager() != cim {
		t.Fatal("NewAgent must wire the registry's code index into the agent")
	}
}
