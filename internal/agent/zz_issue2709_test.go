package agent

import (
	"strings"
	"testing"
)

// zz_issue2709_test.go pins the two-tier edit-propagation warning semantics
// (#2709): the soft tier (4+ distinct files) and the escalation tier (7+)
// each consume one of the two per-run quotas, so the escalation message is
// reachable in progressive edit flows (the cap=1 residue made it dead code).

// epEdit simulates editing one distinct file via edit_file args.
func epEdit(n int) string {
	return `{"file_path":"/w/f` + epItoa(n) + `.go","old_text":"a","new_text":"b"}`
}

func epItoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// Progressive flow: 4 files -> soft warning; 5/6 silent; 7 -> escalation;
// 8+ silent (both quotas spent).
func TestIssue2709ProgressiveTwoTierFlow(t *testing.T) {
	s := newEditPropagationState()
	for i := 1; i <= 3; i++ {
		s.recordEdit("edit_file", epEdit(i))
		if got := s.maybeWarn(1); got != "" {
			t.Fatalf("below Warn1 should stay silent, got: %s", got)
		}
	}
	s.recordEdit("edit_file", epEdit(4))
	soft := s.maybeWarn(1)
	if soft == "" {
		t.Fatal("Warn1 crossing must fire the soft-tier advisory")
	}
	if strings.Contains(soft, "non-linearly") {
		t.Fatalf("4 files must produce the SOFT message, got escalation text: %s", soft)
	}
	// 5 and 6: soft quota spent, escalation not yet reachable.
	for i := 5; i <= 6; i++ {
		s.recordEdit("edit_file", epEdit(i))
		if got := s.maybeWarn(1); got != "" {
			t.Fatalf("mid-tier (5-6) must stay silent after soft fired, got: %s", got)
		}
	}
	// 7: escalation tier must now be REACHABLE - the #2709 regression was
	// exactly this transition being permanently dead code.
	s.recordEdit("edit_file", epEdit(7))
	esc := s.maybeWarn(1)
	if esc == "" {
		t.Fatal("Warn2 crossing must fire the escalation advisory (#2709: was unreachable)")
	}
	if !strings.Contains(esc, "non-linearly") || !strings.Contains(esc, "NOW") {
		t.Fatalf("7+ files must produce the ESCALATION message, got: %s", esc)
	}
	// 8: both quotas spent, silence.
	s.recordEdit("edit_file", epEdit(8))
	if got := s.maybeWarn(1); got != "" {
		t.Fatalf("after both tiers fired, further growth must stay silent, got: %s", got)
	}
}

// Batch jump: a single multi-file edit landing 7+ distinct files at once
// skips the soft tier and emits the escalation message directly.
func TestIssue2709BatchJumpStraightToEscalation(t *testing.T) {
	s := newEditPropagationState()
	var files strings.Builder
	files.WriteString(`{"path":"`)
	for i := 1; i <= 7; i++ {
		if i > 1 {
			files.WriteString(`","path":"`)
		}
		files.WriteString("/w/f" + epItoa(i) + ".go")
	}
	files.WriteString(`"}`)
	s.recordEdit("multi_file_edit", files.String())

	got := s.maybeWarn(1)
	if got == "" {
		t.Fatal("7 distinct files from one batch edit must warn")
	}
	if !strings.Contains(got, "non-linearly") {
		t.Fatalf("batch jump past Warn2 must emit the ESCALATION message directly, got: %s", got)
	}
}

// Repeated maybeWarn calls at a stable count must not double-fire either tier.
func TestIssue2709NoRepeatAtStableCount(t *testing.T) {
	s := newEditPropagationState()
	for i := 1; i <= 4; i++ {
		s.recordEdit("edit_file", epEdit(i))
	}
	if s.maybeWarn(1) == "" {
		t.Fatal("soft tier must fire at 4")
	}
	for i := 0; i < 5; i++ {
		if got := s.maybeWarn(1); got != "" {
			t.Fatalf("stable count must not re-fire the soft tier, got: %s", got)
		}
	}
	// Even growth to 5-6 stays silent; only crossing 7 re-fires.
	for i := 5; i <= 6; i++ {
		s.recordEdit("edit_file", epEdit(i))
	}
	if got := s.maybeWarn(1); got != "" {
		t.Fatalf("5-6 after soft fired must stay silent, got: %s", got)
	}
}
