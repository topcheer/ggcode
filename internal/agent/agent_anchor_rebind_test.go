package agent

import (
	"os"
	"path/filepath"
	"testing"
)

// #5498: anchorStore must rebind to the workspace on SetWorkingDir -
// construction-time AnchorPath(".") pinned it to the process cwd, so
// ACP/A2A/daemon deployments (cwd != workspace) persisted to the startup
// dir and shared one store across workspaces.
func TestSetWorkingDirRebindsAnchorStore(t *testing.T) {
	wsA := t.TempDir()
	wsB := t.TempDir()
	for _, ws := range []string{wsA, wsB} {
		if err := os.MkdirAll(filepath.Join(ws, ".ggcode"), 0755); err != nil {
			t.Fatal(err)
		}
	}

	a := &Agent{anchorStore: LoadAnchorStore(AnchorPath("."))}
	startPath := a.anchorStore.path

	a.SetWorkingDir(wsA)
	if a.anchorStore.path != AnchorPath(wsA) {
		t.Fatalf("after SetWorkingDir(A) path = %q, want %q", a.anchorStore.path, AnchorPath(wsA))
	}
	// ObserveUserMessage is pattern-gated (anchorCnPatterns); for this
	// rebind-contract test append directly to focus on the store swap.
	a.anchorStore.Anchors = append(a.anchorStore.Anchors, ConstraintAnchor{ID: "a1", Text: "constraint-A", Source: "explicit"})

	a.SetWorkingDir(wsB)
	if a.anchorStore.path != AnchorPath(wsB) {
		t.Fatalf("after SetWorkingDir(B) path = %q, want %q", a.anchorStore.path, AnchorPath(wsB))
	}
	if len(a.anchorStore.Anchors) != 0 {
		t.Fatalf("workspace B store must start empty, got %d anchors (cross-workspace pollution)", len(a.anchorStore.Anchors))
	}

	// Same-dir re-call must not reload (would drop in-memory anchors).
	a.anchorStore.Anchors = append(a.anchorStore.Anchors, ConstraintAnchor{ID: "b1", Text: "constraint-B", Source: "explicit"})
	a.SetWorkingDir(wsB)
	if len(a.anchorStore.Anchors) != 1 || a.anchorStore.Anchors[0].Text != "constraint-B" {
		t.Fatalf("same-dir SetWorkingDir reloaded store, dropping in-memory anchors: %+v", a.anchorStore.Anchors)
	}

	_ = startPath
}
