package lanchat

import (
	"strings"
	"testing"
)

// #4913: shared-workspace collision alert — the exact iron rules depend on
// collision state and VCS presence; the no-collision case must stay silent.
func TestSharedWorkspaceAlert(t *testing.T) {
	store := NewStore(t.TempDir())
	h := NewHub("self-node", "tui", "http://127.0.0.1:1", "", store, WorkspaceMeta{Workspace: "/ws"})
	addPeer := func(node, ws string, online bool) {
		h.mu.Lock()
		defer h.mu.Unlock()
		h.peers[node] = &Participant{NodeID: node, Workspace: ws, Online: online, AgentNick: node + "_agent"}
	}

	t.Run("no peers = silent", func(t *testing.T) {
		if got := SharedWorkspaceAlert(h, "self-node", "/ws", true, ""); got != "" {
			t.Fatalf("expected empty alert without peers, got:\n%s", got)
		}
	})
	t.Run("nil hub = silent", func(t *testing.T) {
		if got := SharedWorkspaceAlert(nil, "self", "/ws", true, ""); got != "" {
			t.Fatalf("expected empty alert with nil hub, got:\n%s", got)
		}
	})
	t.Run("git workspace: worktree+PR rules", func(t *testing.T) {
		addPeer("peer-a", "/ws", true)
		got := SharedWorkspaceAlert(h, "self-node", "/ws", true, "")
		for _, want := range []string{"SHARED WORKSPACE", "peer-a_agent", "worktree", "PR", "git stash"} {
			if !strings.Contains(got, want) {
				t.Fatalf("git alert missing %q, got:\n%s", want, got)
			}
		}
	})
	t.Run("non-VCS workspace: coordinate-first rules", func(t *testing.T) {
		got := SharedWorkspaceAlert(h, "self-node", "/ws", false, "")
		if !strings.Contains(got, "NOT under git/svn") || !strings.Contains(got, "cannot be reverted") {
			t.Fatalf("non-VCS alert missing coordination rules, got:\n%s", got)
		}
	})
	t.Run("chinese localization", func(t *testing.T) {
		got := SharedWorkspaceAlert(h, "self-node", "/ws", true, "zh-CN")
		if !strings.Contains(got, "共享工作区") || !strings.Contains(got, "铁律") {
			t.Fatalf("zh alert missing key terms, got:\n%s", got)
		}
	})
	t.Run("self and offline peers excluded, other workspaces excluded", func(t *testing.T) {
		addPeer("self-node", "/ws", true)        // self must not count
		addPeer("peer-off", "/ws", false)        // offline ignored
		addPeer("peer-other", "/other/ws", true) // different workspace ignored
		got := SharedWorkspaceAlert(h, "self-node", "/ws", true, "")
		if strings.Contains(got, "self-node") || strings.Contains(got, "peer-off") || strings.Contains(got, "peer-other") {
			t.Fatalf("alert must only name live same-workspace peers, got:\n%s", got)
		}
		if !strings.Contains(got, "peer-a_agent") {
			t.Fatalf("live same-workspace peer must be named, got:\n%s", got)
		}
	})
}
