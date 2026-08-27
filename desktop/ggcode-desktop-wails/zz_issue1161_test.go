package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/desktop/wailskit"
	"github.com/topcheer/ggcode/internal/tunnel"
)

// #1161: StartShare and tunnelSnapshot must obey the single-read snapshot
// invariant established by #457/#699: read a.chat exactly once into a local
// snapshot, then check and use the snapshot itself. Checking the field again
// (a.chat == nil / a.chat != nil) after the snapshot creates a TOCTOU window:
// a concurrent switchWorkspace can nil or replace a.chat between the snapshot
// and the re-check, so commands get bound to an already-closed bridge and all
// mobile traffic silently routes into the dead bridge.

// appMethodBody extracts the source text of one (a *App) method from app.go,
// spanning from its func line to the next top-level func declaration.
func appMethodBody(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("app.go"))
	if err != nil {
		t.Fatalf("reading app.go: %v", err)
	}
	src := string(data)
	sig := "func (a *App) " + name + "("
	start := strings.Index(src, sig)
	if start < 0 {
		t.Fatalf("method %s not found in app.go", name)
	}
	body := src[start:]
	if end := strings.Index(body[1:], "\nfunc "); end >= 0 {
		body = body[:end+1]
	}
	return body
}

// stripLineComments removes // comments so comment mentions cannot fool the
// static scan below.
func stripLineComments(s string) string {
	out := make([]string, 0, 64)
	for _, line := range strings.Split(s, "\n") {
		if idx := strings.Index(line, "//"); idx >= 0 {
			line = line[:idx]
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

// TestIssue1161StartShareChecksChatSnapshotNotField: nil chat must fail fast
// through the entry snapshot (dynamic contract).
func TestIssue1161StartShareChecksChatSnapshotNotField(t *testing.T) {
	a := NewApp()
	info, err := a.StartShare()
	if err == nil {
		t.Fatalf("StartShare with nil chat: expected error, got result %+v", info)
	}
	if !strings.Contains(err.Error(), "chat not initialized") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestIssue1161TunnelSnapshotNilChatIdleStatus: tunnelSnapshot on a nil chat
// must return the idle status snapshot and never panic.
func TestIssue1161TunnelSnapshotNilChatIdleStatus(t *testing.T) {
	a := NewApp()
	// Pre-existing code before the chat snapshot dereferences a.dc (#1161
	// regression surface is below that line, so satisfy it first).
	a.dc = wailskit.LoadDesktopConfig()
	snapshot := a.tunnelSnapshot()
	if snapshot.Status.Status != tunnel.StatusIdle {
		t.Fatalf("expected idle status for nil chat, got %q", snapshot.Status.Status)
	}
}

// TestIssue1161NoAChatRereadsAfterSnapshot: static regression guard. Within
// StartShare and tunnelSnapshot, after the single
// "chat := a.chat" snapshot line there must be no further reference to the
// struct field. Any extra occurrence reintroduces the TOCTOU window fixed by
// this issue.
func TestIssue1161NoAChatRereadsAfterSnapshot(t *testing.T) {
	for _, name := range []string{"StartShare", "tunnelSnapshot"} {
		code := stripLineComments(appMethodBody(t, name))
		first := strings.Index(code, "a.chat")
		if first < 0 {
			t.Fatalf("%s: missing snapshot assignment (chat := a.chat)", name)
		}
		rest := code[first+len("a.chat"):]
		// Allow only the binding form; any other occurrence is a re-read.
		if idx := strings.Index(rest, "a.chat"); idx >= 0 {
			ctx := rest[maxInt(0, idx-80):]
			ctx = ctx[:minInt(len(ctx), 120)]
			t.Fatalf("%s: re-reads a.chat after taking the snapshot - TOCTOU (%s):\n...%s...", name, name, ctx)
		}
	}
}

// TestIssue1161BindShareCommandsUsesEntrySnapshot verifies by inspection that
// the share-command wiring guard no longer gates on the live field: the bind
// call must be guarded against result.Broker only, because the nil-ness of
// the snapshotted chat was already established at function entry.
func TestIssue1161BindShareCommandsUsesEntrySnapshot(t *testing.T) {
	code := stripLineComments(appMethodBody(t, "StartShare"))
	if !strings.Contains(code, "if result.Broker != nil {") {
		t.Fatal("StartShare: expected the BindShareCommands guard to rely on result.Broker")
	}
	if strings.Contains(code, "if a.chat != nil &&") {
		t.Fatal("StartShare: BindShareCommands guard still re-reads the field - dead-bridge routing bug is back")
	}
}

// wailsTestDesktopConfig removed: StartShare's nil-chat path does not need
// a desktop config - LoadConfigForWorkspace tolerates an uninitialized dc.

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
