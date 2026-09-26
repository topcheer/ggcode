package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/chat"
)

func TestHandleDocauditCommandWritesReport(t *testing.T) {
	dir := t.TempDir()
	doc := "Use `/nosuchcmd-zzz` to deploy. See `internal/tool/missing-zzz.go`.\n"
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}

	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(oldWd) }()

	m := &Model{chatList: chat.NewList(80, 24)}
	if cmd := m.handleDocauditCommand(); cmd != nil {
		t.Fatalf("handleDocauditCommand returned non-nil cmd")
	}
	if m.chatList.Len() == 0 {
		t.Fatal("no chat output written")
	}
	item, ok := m.chatList.ItemAt(m.chatList.Len() - 1).(*chat.SystemItem)
	if !ok {
		t.Fatalf("last item is not a SystemItem")
	}
	out := item.Text()
	if !strings.Contains(out, "/docaudit: scanned") ||
		!strings.Contains(out, "stale-command") ||
		!strings.Contains(out, "/nosuchcmd-zzz") {
		t.Fatalf("report output missing findings: %q", out)
	}
}

func TestHandleDocauditCommandCleanProject(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("# clean\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(oldWd) }()

	m := &Model{chatList: chat.NewList(80, 24)}
	_ = m.handleDocauditCommand()
	item, ok := m.chatList.ItemAt(m.chatList.Len() - 1).(*chat.SystemItem)
	if !ok {
		t.Fatalf("last item is not a SystemItem")
	}
	if !strings.Contains(item.Text(), "no stale references") {
		t.Fatalf("clean report = %q", item.Text())
	}
}
