package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newTestLedger(t *testing.T) (*interventionLedger, string) {
	t.Helper()
	dir := t.TempDir()
	return newInterventionLedger(dir), dir
}

func TestInterventionRecordPersist(t *testing.T) {
	l, dir := newTestLedger(t)
	for i := 0; i < 3; i++ {
		l.record("git_commit", i+1)
	}

	l2 := newInterventionLedger(dir)
	if got := l2.recentCount("git_commit"); got != 3 {
		t.Fatalf("persisted count = %d, want 3", got)
	}
}

func TestInterventionHintThresholdAndCooldown(t *testing.T) {
	l, _ := newTestLedger(t)

	l.record("run_command", 1)
	l.record("run_command", 2)
	if h := l.hint("run_command"); h != "" {
		t.Fatalf("hint below threshold must be empty, got: %s", h)
	}

	l.record("run_command", 3)
	h := l.hint("run_command")
	if h == "" {
		t.Fatal("hint must fire at 3 recent takeovers")
	}
	if !strings.Contains(h, "run_command") || !strings.Contains(h, "state in one sentence") {
		t.Errorf("hint must name the tool and the defer ask: %s", h)
	}

	if h2 := l.hint("run_command"); h2 != "" {
		t.Fatalf("cooldown must suppress back-to-back hints, got: %s", h2)
	}

	if h3 := l.hint("read_file"); h3 != "" {
		t.Fatalf("no history for read_file, want empty, got: %s", h3)
	}
}

func TestInterventionRecencyExpiry(t *testing.T) {
	l, dir := newTestLedger(t)

	stale := time.Now().Add(-20 * 24 * time.Hour).Unix()
	l.data.Entries = append(l.data.Entries,
		interventionEntry{Tool: "git_push", Ts: stale},
		interventionEntry{Tool: "git_push", Ts: stale},
		interventionEntry{Tool: "git_push", Ts: stale},
	)
	l.saveLocked()
	l2 := newInterventionLedger(dir)
	if h := l2.hint("git_push"); h != "" {
		t.Fatalf("stale entries outside the window must not hint, got: %s", h)
	}
}

func TestInterventionClear(t *testing.T) {
	l, dir := newTestLedger(t)
	for i := 0; i < 3; i++ {
		l.record("edit_file", i+1)
	}
	l.clear()
	l2 := newInterventionLedger(dir)
	if got := l2.recentCount("edit_file"); got != 0 {
		t.Fatalf("after clear, count = %d, want 0", got)
	}
	if h := l2.hint("edit_file"); h != "" {
		t.Fatalf("after clear, hint must be empty, got: %s", h)
	}
}

func TestInterventionPrivacyAndNilSafety(t *testing.T) {
	var l *interventionLedger
	l.record("x", 1)
	if h := l.hint("x"); h != "" {
		t.Fatal("nil ledger hint must be empty")
	}
	l.clear()

	l2, _ := newTestLedger(t)
	l2.record("  Git_Commit_with_a_very_long_name_exceeding_sixtyfour_characters_here", 1)
	l2.mu.Lock()
	got := l2.data.Entries[0].Tool
	l2.mu.Unlock()
	if got != strings.ToLower(strings.TrimSpace(got)) || len(got) > 64 {
		t.Fatalf("tool not sanitized: %q", got)
	}

	st, err := os.Stat(filepath.Join(l2.workingDir, ".ggcode", "interventions.json"))
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("ledger file must exist with 0600, got %v err=%v", st, err)
	}
}
