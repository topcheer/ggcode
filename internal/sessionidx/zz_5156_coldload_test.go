package sessionidx

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// #5149 cold-load pins (user-directed design): the shared index carries
// NOTHING at rest; the first query naming a workspace loads that
// workspace's indexes; a request-free window unloads them again; any
// request resets the window.

func setupWsDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	seedIdx(t, dir, "a1", 3)
	seedIdx(t, dir, "a2", 3)
	seedIdx(t, dir, "b1", 3)
	idxJSON := `{"entries":[
		{"id":"a1","workspace":"/w/alpha"},
		{"id":"a2","workspace":"/w/alpha"},
		{"id":"b1","workspace":"/w/beta"}
	]}`
	if err := os.WriteFile(filepath.Join(dir, "index.json"), []byte(idxJSON), 0o600); err != nil {
		t.Fatalf("index.json: %v", err)
	}
	return dir
}

func TestColdLoad_AtRestIsEmpty(t *testing.T) {
	dir := setupWsDir(t)
	m := NewManager(dir, "")
	m.SetTotalBudget(1 << 20) // daemon mode: lazy
	m.scanExternal()

	if got := m.TotalDecodedBytes(); got != 0 {
		t.Fatalf("daemon must start with nothing decoded, got %d bytes", got)
	}
	if ws := m.activeWorkspaces(); len(ws) != 0 {
		t.Fatalf("no workspace may be active at rest: %v", ws)
	}
}

func TestColdLoad_FirstQueryLoadsOnlyThatWorkspace(t *testing.T) {
	dir := setupWsDir(t)
	m := NewManager(dir, "")
	m.SetTotalBudget(1 << 20)
	m.scanExternal()

	got := m.QueryScoped([]string{"token-alpha"}, "", 20, "/w/alpha")
	if len(got) == 0 {
		t.Fatal("expected hits for alpha")
	}
	for _, r := range got {
		if r.SessionID == "b1" {
			t.Fatal("beta index must not be served to an alpha query")
		}
	}
	// Only alpha's two indexes are resident.
	var wantAlpha int64
	for _, id := range []string{"a1", "a2"} {
		st, err := os.Stat(IdxPath(dir, id))
		if err != nil {
			t.Fatalf("stat %s: %v", id, err)
		}
		wantAlpha += st.Size()
	}
	if got := m.TotalDecodedBytes(); got != wantAlpha {
		t.Fatalf("decoded=%d, want exactly alpha's %d", got, wantAlpha)
	}
	ws := m.activeWorkspaces()
	if len(ws) != 1 || ws[0] != "/w/alpha" {
		t.Fatalf("active workspaces = %v, want [/w/alpha]", ws)
	}
}

func TestColdLoad_IdleUnloadFreesWorkspace(t *testing.T) {
	dir := setupWsDir(t)
	m := NewManager(dir, "")
	m.SetTotalBudget(1 << 20)
	m.SetIdleUnload(50 * time.Millisecond)
	m.scanExternal()

	if res := m.QueryScoped([]string{"token-alpha"}, "", 20, "/w/alpha"); len(res) == 0 {
		t.Fatal("alpha query should hit")
	}
	if m.TotalDecodedBytes() == 0 {
		t.Fatal("alpha should be resident right after its query")
	}

	time.Sleep(80 * time.Millisecond) // past the idle window
	m.unloadIdleWorkspaces()

	if got := m.TotalDecodedBytes(); got != 0 {
		t.Fatalf("idle workspace must be unloaded from the heap, still %d bytes", got)
	}
	if ws := m.activeWorkspaces(); len(ws) != 0 {
		t.Fatalf("active set must be empty after unload: %v", ws)
	}
	// And a later query re-activates it transparently.
	if res := m.QueryScoped([]string{"token-alpha"}, "", 20, "/w/alpha"); len(res) == 0 {
		t.Fatal("re-query after unload must still return hits")
	}
}

func TestColdLoad_RequestResetsIdleWindow(t *testing.T) {
	dir := setupWsDir(t)
	m := NewManager(dir, "")
	m.SetTotalBudget(1 << 20)
	m.SetIdleUnload(120 * time.Millisecond)
	m.scanExternal()

	if res := m.QueryScoped([]string{"token-alpha"}, "", 20, "/w/alpha"); len(res) == 0 {
		t.Fatal("first query should hit")
	}
	time.Sleep(70 * time.Millisecond) // inside the window
	if res := m.QueryScoped([]string{"token-alpha"}, "", 20, "/w/alpha"); len(res) == 0 {
		t.Fatal("second query should hit")
	}
	time.Sleep(70 * time.Millisecond) // still inside, counted from the reset
	m.unloadIdleWorkspaces()
	if m.TotalDecodedBytes() == 0 {
		t.Fatal("a request inside the window must reset it - workspace was unloaded too early")
	}

	time.Sleep(140 * time.Millisecond) // now genuinely idle
	m.unloadIdleWorkspaces()
	if got := m.TotalDecodedBytes(); got != 0 {
		t.Fatalf("after a full idle window the workspace must unload, still %d bytes", got)
	}
}
