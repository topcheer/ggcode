package tmux

import (
	"path/filepath"
	"testing"
)

func TestSharedManagerReturnsSameManagerForWorkspace(t *testing.T) {
	resetSharedManagersForTest()
	workspace := t.TempDir()
	first := SharedManager(workspace)
	second := SharedManager(workspace)
	if first != second {
		t.Fatal("SharedManager should return the same manager for the same workspace")
	}
	other := SharedManager(t.TempDir())
	if other == first {
		t.Fatal("SharedManager should return different managers for different workspaces")
	}
}

func TestManagerListAndManagedPaneText(t *testing.T) {
	mgr := NewManagerWithStorePath(NewClient(), t.TempDir(), filepath.Join(t.TempDir(), "tmux-panes.json"))
	mgr.panes["%2"] = Pane{ID: "%2", Purpose: "test", Command: "go test", Alive: false}
	mgr.panes["%1"] = Pane{ID: "%1", Purpose: "shell", Command: "", Alive: true}

	panes := mgr.List()
	if len(panes) != 2 || panes[0].ID != "%1" || panes[1].ID != "%2" {
		t.Fatalf("List() = %+v, want sorted panes", panes)
	}
	text := mgr.ManagedPaneText()
	for _, want := range []string{"%1 [shell/alive]", "%2 [test/stale] go test"} {
		if !contains(text, want) {
			t.Fatalf("ManagedPaneText() = %q, want to contain %q", text, want)
		}
	}
}

func TestManagerUpdateAliveState(t *testing.T) {
	mgr := NewManagerWithStorePath(NewClient(), t.TempDir(), filepath.Join(t.TempDir(), "tmux-panes.json"))
	mgr.panes["%1"] = Pane{ID: "%1", Alive: true}
	mgr.panes["%2"] = Pane{ID: "%2", Alive: true}

	alive, stale := mgr.UpdateAliveState(map[string]struct{}{"%2": {}})
	if alive != 1 || stale != 1 {
		t.Fatalf("UpdateAliveState() = (%d, %d), want (1, 1)", alive, stale)
	}
	if mgr.panes["%1"].Alive || !mgr.panes["%2"].Alive {
		t.Fatalf("unexpected alive state: %+v", mgr.panes)
	}
}

func TestManagerPruneRemovesMatchingStalePanesOnly(t *testing.T) {
	mgr := NewManagerWithStorePath(NewClient(), t.TempDir(), filepath.Join(t.TempDir(), "tmux-panes.json"))
	mgr.panes["%1"] = Pane{ID: "%1", Purpose: "test", Alive: false}
	mgr.panes["%2"] = Pane{ID: "%2", Purpose: "dev", Alive: false}
	mgr.panes["%3"] = Pane{ID: "%3", Purpose: "test", Alive: true}

	if removed := mgr.Prune("test"); removed != 1 {
		t.Fatalf("Prune(test) removed %d, want 1", removed)
	}
	if _, ok := mgr.panes["%1"]; ok {
		t.Fatal("stale matching pane should be pruned")
	}
	if _, ok := mgr.panes["%2"]; !ok {
		t.Fatal("non-matching stale pane should remain")
	}
	if _, ok := mgr.panes["%3"]; !ok {
		t.Fatal("alive matching pane should remain")
	}
}

func TestManagerRestoreCandidatesSelectsStaleWithSelector(t *testing.T) {
	mgr := NewManagerWithStorePath(NewClient(), t.TempDir(), filepath.Join(t.TempDir(), "tmux-panes.json"))
	mgr.panes["%1"] = Pane{ID: "%1", Purpose: "test", Command: "go test", Alive: false}
	mgr.panes["%2"] = Pane{ID: "%2", Purpose: "dev", Command: "npm run dev", Alive: false}
	mgr.panes["%3"] = Pane{ID: "%3", Purpose: "test", Command: "go test", Alive: true}

	candidates := mgr.restoreCandidates("test")
	if len(candidates) != 1 || candidates[0].ID != "%1" {
		t.Fatalf("restoreCandidates(test) = %+v, want only stale test pane", candidates)
	}
}

func TestManagerSavesAndLoadsWorkspaceLayouts(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "tmux-panes.json")
	workspace := t.TempDir()
	mgr := NewManagerWithStorePath(NewClient(), workspace, storePath)
	mgr.panes["%1"] = Pane{ID: "%1", Purpose: "test", Command: "go test", Workspace: workspace, Alive: true, Horizontal: false, Size: "30%"}
	mgr.panes["%2"] = Pane{ID: "%2", Purpose: "dev", Command: "npm run dev", Workspace: workspace, Alive: true, Horizontal: true, Size: "35%"}
	if err := mgr.SaveLayout("default"); err != nil {
		t.Fatalf("SaveLayout: %v", err)
	}

	reloaded := NewManagerWithStorePath(NewClient(), workspace, storePath)
	names := reloaded.ListLayoutNames()
	if len(names) != 1 || names[0] != "default" {
		t.Fatalf("ListLayoutNames() = %v, want [default]", names)
	}
	layout := reloaded.Layout("default")
	if len(layout) != 2 {
		t.Fatalf("Layout(default) length = %d, want 2", len(layout))
	}
	if layout[0].Purpose == "" || layout[1].Purpose == "" {
		t.Fatalf("layout missing pane metadata: %+v", layout)
	}
}

func TestManagerPersistsPanesByWorkspace(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "tmux-panes.json")
	workspaceA := filepath.Join(t.TempDir(), "workspace-a")
	workspaceB := filepath.Join(t.TempDir(), "workspace-b")

	mgrA := NewManagerWithStorePath(NewClient(), workspaceA, storePath)
	mgrA.panes["%1"] = Pane{ID: "%1", Purpose: "dev", Command: "npm run dev", Workspace: workspaceA, Alive: true}
	if err := mgrA.Save(); err != nil {
		t.Fatalf("Save A: %v", err)
	}

	mgrB := NewManagerWithStorePath(NewClient(), workspaceB, storePath)
	mgrB.panes["%2"] = Pane{ID: "%2", Purpose: "test", Command: "go test ./...", Workspace: workspaceB, Alive: false}
	if err := mgrB.Save(); err != nil {
		t.Fatalf("Save B: %v", err)
	}

	reloadedA := NewManagerWithStorePath(NewClient(), workspaceA, storePath)
	panesA := reloadedA.List()
	if len(panesA) != 1 || panesA[0].ID != "%1" || panesA[0].Command != "npm run dev" {
		t.Fatalf("workspace A panes = %+v", panesA)
	}
	reloadedB := NewManagerWithStorePath(NewClient(), workspaceB, storePath)
	panesB := reloadedB.List()
	if len(panesB) != 1 || panesB[0].ID != "%2" || panesB[0].Command != "go test ./..." {
		t.Fatalf("workspace B panes = %+v", panesB)
	}
}

func contains(s, substr string) bool {
	return len(substr) == 0 || (len(s) >= len(substr) && index(s, substr) >= 0)
}

func index(s, substr string) int {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}
