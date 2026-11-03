package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/desktop/wailskit"
	"github.com/topcheer/ggcode/internal/tunnel"
)

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

func TestIssue1161StartShareChecksChatSnapshotNotField(t *testing.T) {

	t.Setenv("HOME", t.TempDir())
	a := NewApp()
	info, err := a.StartShare()
	if err == nil {
		t.Fatalf("StartShare with nil chat: expected error, got result %+v", info)
	}
	if !strings.Contains(err.Error(), "chat not initialized") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestIssue1161TunnelSnapshotNilChatIdleStatus(t *testing.T) {
	a := NewApp()

	a.dc = wailskit.LoadDesktopConfig()
	snapshot := a.tunnelSnapshot()
	if snapshot.Status.Status != tunnel.StatusIdle {
		t.Fatalf("expected idle status for nil chat, got %q", snapshot.Status.Status)
	}
}

func TestIssue1161NoAChatRereadsAfterSnapshot(t *testing.T) {
	for _, name := range []string{"StartShare", "tunnelSnapshot"} {
		code := stripLineComments(appMethodBody(t, name))
		first := strings.Index(code, "a.chat")
		if first < 0 {
			t.Fatalf("%s: missing snapshot assignment (chat := a.chat)", name)
		}
		rest := code[first+len("a.chat"):]

		if idx := strings.Index(rest, "a.chat"); idx >= 0 {
			ctx := rest[maxInt(0, idx-80):]
			ctx = ctx[:minInt(len(ctx), 120)]
			t.Fatalf("%s: re-reads a.chat after taking the snapshot - TOCTOU (%s):\n...%s...", name, name, ctx)
		}
	}
}

func TestIssue1161BindShareCommandsUsesEntrySnapshot(t *testing.T) {
	code := stripLineComments(appMethodBody(t, "StartShare"))
	if !strings.Contains(code, "if result.Broker != nil {") {
		t.Fatal("StartShare: expected the BindShareCommands guard to rely on result.Broker")
	}
	if strings.Contains(code, "if a.chat != nil &&") {
		t.Fatal("StartShare: BindShareCommands guard still re-reads the field - dead-bridge routing bug is back")
	}
}

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
