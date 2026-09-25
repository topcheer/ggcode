package agent

import (
	"strings"
	"testing"

	ctxpkg "github.com/topcheer/ggcode/internal/context"
)

func TestDenyLedgerNilReceiverSafe(t *testing.T) {
	var l *DenyLedger
	l.Record("Bash", "blocked")
	if got := l.StickyFeedback("Bash"); got != "" {
		t.Fatalf("StickyFeedback on nil ledger = %q, want empty", got)
	}
	if got := l.PostCompactNote(); got != "" {
		t.Fatalf("PostCompactNote on nil ledger = %q, want empty", got)
	}
}

func TestDenyLedgerStickyFeedbackCountsAttempts(t *testing.T) {
	l := newDenyLedger()
	l.Record("Bash", "denied by policy hook")
	first := l.StickyFeedback("Bash")
	if !strings.Contains(first, "attempt #1") {
		t.Fatalf("first sticky feedback should say attempt #1, got %q", first)
	}
	if !strings.Contains(first, "remains binding after context compaction") {
		t.Fatalf("sticky feedback must state compaction persistence, got %q", first)
	}
	l.Record("Bash", "denied again")
	second := l.StickyFeedback("Bash")
	if !strings.Contains(second, "attempt #2") {
		t.Fatalf("second sticky feedback should say attempt #2, got %q", second)
	}
	if !strings.Contains(second, "2 hook block(s) recorded this session") {
		t.Fatalf("sticky feedback should restate session-wide count, got %q", second)
	}
	// A different tool's count is independent.
	if got := l.StickyFeedback("write_file"); !strings.Contains(got, "") || strings.Contains(got, "attempt #") {
		t.Fatalf("unrecorded tool should have no sticky feedback, got %q", got)
	}
}

func TestDenyLedgerEmptyWithoutDenials(t *testing.T) {
	l := newDenyLedger()
	if got := l.StickyFeedback("Bash"); got != "" {
		t.Fatalf("StickyFeedback with no denials = %q, want empty", got)
	}
	if got := l.PostCompactNote(); got != "" {
		t.Fatalf("PostCompactNote with no denials = %q, want empty (clean sessions must not get a note)", got)
	}
}

func TestDenyLedgerPostCompactNoteAggregates(t *testing.T) {
	l := newDenyLedger()
	l.Record("Bash", "rm blocked by hook")
	l.Record("Bash", "encoded variant blocked")
	l.Record("write_file", "outside workspace")
	note := l.PostCompactNote()
	if !strings.Contains(note, "Bash x2") || !strings.Contains(note, "write_file x1") {
		t.Fatalf("note should aggregate per-tool counts, got %q", note)
	}
	if !strings.Contains(note, "outside workspace") {
		t.Fatalf("note should include the last denial reason, got %q", note)
	}
	if !strings.Contains(note, "remain binding after compaction") {
		t.Fatalf("note must restate policy persistence, got %q", note)
	}
}

func TestDenyLedgerReasonClamped(t *testing.T) {
	l := newDenyLedger()
	long := strings.Repeat("x", 500)
	l.Record("Bash", long)
	note := l.PostCompactNote()
	if len(note) > 400 {
		t.Fatalf("note with clamped reason too long: %d chars", len(note))
	}
	if !strings.Contains(note, "...") {
		t.Fatalf("clamped reason should carry ellipsis marker, got %q", note)
	}
}

func TestDenyLedgerRingBounded(t *testing.T) {
	l := newDenyLedger()
	for i := 0; i < denyLedgerMaxEvents+10; i++ {
		l.Record("Bash", "blocked")
	}
	l.mu.Lock()
	n := len(l.events)
	l.mu.Unlock()
	if n != denyLedgerMaxEvents {
		t.Fatalf("ledger size = %d, want bounded at %d", n, denyLedgerMaxEvents)
	}
}

// hookDenyNoteRecorder is a fake context manager capturing Add-registered
// providers, used to verify sync idempotency.
type hookDenyNoteRecorder struct {
	ctxpkg.ContextManager
	added []func() string
}

func (f *hookDenyNoteRecorder) AddPostCompactNoteProvider(fn func() string) {
	f.added = append(f.added, fn)
}

func TestSyncContextManagerHookDenyNoteIdempotent(t *testing.T) {
	fake := &hookDenyNoteRecorder{}
	a := &Agent{contextManager: fake, hookDenies: newDenyLedger()}
	a.syncContextManagerHookDenyNoteLocked()
	a.syncContextManagerHookDenyNoteLocked() // same manager: must not stack
	if len(fake.added) != 1 {
		t.Fatalf("provider registered %d times, want 1", len(fake.added))
	}
	// The registered provider must surface recorded denials.
	fake.added[0]()
	l := a.hookDenies
	l.Record("Bash", "blocked")
	if got := fake.added[0](); !strings.Contains(got, "Hook policy state") {
		t.Fatalf("registered provider should render ledger note, got %q", got)
	}
}

func TestSyncContextManagerHookDenyNoteNoCapabilityNoop(t *testing.T) {
	a := &Agent{contextManager: ctxpkg.NewManager(200000), hookDenies: newDenyLedger()}
	a.syncContextManagerHookDenyNoteLocked()
	if got := a.hookDenies.PostCompactNote(); got != "" {
		t.Fatalf("unexpected note: %q", got)
	}
}

func TestSyncContextManagerHookDenyNoteNilLedgerNoop(t *testing.T) {
	fake := &hookDenyNoteRecorder{}
	a := &Agent{contextManager: fake}
	a.syncContextManagerHookDenyNoteLocked()
	if len(fake.added) != 0 {
		t.Fatalf("nil ledger must not register provider, got %d", len(fake.added))
	}
}
