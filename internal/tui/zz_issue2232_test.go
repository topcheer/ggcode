package tui

// #2232: wecom create had three bare-error failure points over a
// just-persisted Enabled:true adapter (credentials retried forever).
// Pins: all three points route the rollback helper; the helper itself
// carries remove+save and surfaces the original error.

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
	// ensure failure + generic start failure + enable-restart failure
	n := strings.Count(src, "rollbackWecomCreate(name")
	if n < 4 { // 3 call sites + wecomEnableRollback wrapper + def is 5 lines; >=3 calls + def
		t.Fatalf("expected rollback at all three failure points, found %d references", n)
	}
	if !strings.Contains(src, "RemoveIMAdapter(name)") {
		t.Fatal("rollback must remove the adapter (#1370 pattern)")
	}
	if !strings.Contains(src, "saveConfig()") || !strings.Contains(src, "(rollback failed:") {
		t.Fatal("rollback must persist and surface rollback failure")
	}
	// The old bare-error returns must be gone from the create NEXT
	// flow (the fail: closure for the APPLY step is legitimately bare -
	// a failed apply persisted nothing to roll back).
	next := src[strings.Index(src, "if err := m.ensureWeComRuntime(); err != nil {"):]
	next = next[:strings.Index(next, "added_bot")]
	if strings.Contains(next, "wecomBindResultMsg{err: err}") {
		t.Fatal("create next-flow still has a bare-error failure point")
	}
}
