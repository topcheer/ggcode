package tui

import (
	"os"
	"strings"
	"testing"
)

func TestIssue2232WecomRollbackWired(t *testing.T) {
	b, err := os.ReadFile("wecom_panel.go")
	if err != nil {
		t.Skipf("layout changed: %v", err)
	}
	src := string(b)

	n := strings.Count(src, "rollbackWecomCreate(name")
	if n < 4 {
		t.Fatalf("expected rollback at all three failure points, found %d references", n)
	}
	if !strings.Contains(src, "RemoveIMAdapter(name)") {
		t.Fatal("rollback must remove the adapter (#1370 pattern)")
	}
	if !strings.Contains(src, "saveConfig()") || !strings.Contains(src, "(rollback failed:") {
		t.Fatal("rollback must persist and surface rollback failure")
	}

	next := src[strings.Index(src, "if err := m.ensureWeComRuntime(); err != nil {"):]
	next = next[:strings.Index(next, "added_bot")]
	if strings.Contains(next, "wecomBindResultMsg{err: err}") {
		t.Fatal("create next-flow still has a bare-error failure point")
	}
}
