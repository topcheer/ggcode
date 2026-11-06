package sessionidx

import (
	"os"
	"path/filepath"
	"testing"
)

// #5149 workspace scoping pin: sessions live in ONE machine-global dir, so
// recall must not cross workspace boundaries. Attribution comes from
// <sessionsDir>/index.json; an unattributed session is excluded when a
// workspace filter is active ("only my workspace" must not silently
// degrade to "the whole machine").
func TestQueryScoped_WorkspaceIsolation(t *testing.T) {
	dir := t.TempDir()
	seedIdx(t, dir, "sessA", 3)
	seedIdx(t, dir, "sessB", 3)
	seedIdx(t, dir, "sessU", 3) // unattributed

	idxJSON := `{"entries":[
		{"id":"sessA","workspace":"/w/alpha"},
		{"id":"sessB","workspace":"/w/beta"}
	]}`
	if err := os.WriteFile(filepath.Join(dir, "index.json"), []byte(idxJSON), 0o600); err != nil {
		t.Fatalf("write index.json: %v", err)
	}

	m := NewManager(dir, "")
	m.SetTotalBudget(1 << 20)
	m.scanExternal()

	// Scoped to alpha: only sessA.
	got := m.QueryScoped([]string{"token-alpha"}, "", 10, "/w/alpha")
	if len(got) == 0 {
		t.Fatal("expected hits for the alpha workspace")
	}
	for _, r := range got {
		if r.SessionID != "sessA" {
			t.Fatalf("cross-workspace leak: got %s while scoped to /w/alpha", r.SessionID)
		}
		if r.Workspace != "/w/alpha" {
			t.Fatalf("Result.Workspace = %q, want /w/alpha", r.Workspace)
		}
	}

	// Scoped to beta: only sessB.
	gotB := m.QueryScoped([]string{"token-alpha"}, "", 10, "/w/beta")
	for _, r := range gotB {
		if r.SessionID != "sessB" {
			t.Fatalf("cross-workspace leak: got %s while scoped to /w/beta", r.SessionID)
		}
	}

	// Unscoped (empty workspace) keeps the historic all-workspaces behavior
	// and the unattributed session remains visible.
	all := m.QueryScoped([]string{"token-alpha"}, "", 20, "")
	seen := map[string]bool{}
	for _, r := range all {
		seen[r.SessionID] = true
	}
	if !seen["sessA"] || !seen["sessB"] || !seen["sessU"] {
		t.Fatalf("unscoped query must see every session: %v", seen)
	}

	// Attributed query never returns the unattributed session.
	for _, r := range got {
		if r.SessionID == "sessU" {
			t.Fatal("unattributed session must not appear under a workspace filter")
		}
	}
}

func TestWorkspaceOf(t *testing.T) {
	dir := t.TempDir()
	idxJSON := `{"entries":[{"id":"x1","workspace":"/w/gamma"}]}`
	if err := os.WriteFile(filepath.Join(dir, "index.json"), []byte(idxJSON), 0o600); err != nil {
		t.Fatalf("write index.json: %v", err)
	}
	if got := WorkspaceOf(dir, "x1"); got != "/w/gamma" {
		t.Fatalf("WorkspaceOf = %q, want /w/gamma", got)
	}
	if got := WorkspaceOf(dir, "missing"); got != "" {
		t.Fatalf("WorkspaceOf(missing) = %q, want empty", got)
	}
}
