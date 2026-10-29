package agent

// #4520: tool_equiv_detect warn condition used == instead of >=. The core
// scenario - N byte-identical calls (rawSeen stays true through the threshold,
// correctly yielding to the exact-match detector) followed by a diverging
// occurrence - never warned: at divergence count had already passed
// equivWarnThreshold, so `count == 3` was false forever. These tests pin the
// >= semantics plus the new per-fingerprint once-flags.
import (
	"fmt"
	"testing"
)

func TestIssue4520DivergenceAfterThresholdWarns(t *testing.T) {
	s := newToolEquivDetectState()
	raw := []byte(`{"pattern":"foo","path":"/x"}`)
	reordered := []byte(`{"path":"/x","pattern":"foo"}`)

	// 3 byte-identical calls: exact-match detector's territory - no warning.
	for i := 0; i < 3; i++ {
		if msg := s.recordCall("grep", raw, "r1"); msg != "" {
			t.Fatalf("identical call %d should not warn, got: %s", i+1, msg)
		}
	}
	// 4th call diverges (reordered keys): core scenario - must warn now.
	if msg := s.recordCall("grep", reordered, "r2"); msg == "" {
		t.Fatal("diverging occurrence after threshold must warn (#4520), got empty")
	}
	if s.warnings != 1 {
		t.Fatalf("warnings = %d, want 1", s.warnings)
	}
}

func TestIssue4520WarnsOncePerFingerprint(t *testing.T) {
	s := newToolEquivDetectState()
	// Diverging from the start: distinct raw bytes each time (different rawFp
	// per call, same normalized fingerprint).
	for i := 0; i < 6; i++ {
		msg := s.recordCall("grep", []byte(`{"path":"/x","pattern":"foo"}`), fmt.Sprintf("r%d", i))
		if i < 2 && msg != "" {
			t.Fatalf("call %d below threshold warned: %s", i+1, msg)
		}
		if i == 2 && msg == "" {
			t.Fatal("call 3 at threshold must warn")
		}
		if i != 2 && msg != "" {
			t.Fatalf("call %d re-warned despite once-flag: %s", i+1, msg)
		}
	}
	if s.warnings != 1 {
		t.Fatalf("warnings = %d, want 1 (per-fp once-flag)", s.warnings)
	}
}

func TestIssue4520SecondTierStillFiresOnce(t *testing.T) {
	s := newToolEquivDetectState()
	var warned int
	for i := 0; i < 12; i++ {
		if msg := s.recordCall("grep", []byte(`{"path":"/x","pattern":"foo"}`), fmt.Sprintf("r%d", i)); msg != "" {
			warned++
		}
	}
	if warned != 2 {
		t.Fatalf("total warnings = %d, want 2 (first tier + 3x tier, each once)", warned)
	}
}

func TestIssue4520AllIdenticalStillYields(t *testing.T) {
	s := newToolEquivDetectState()
	// Suppression contract (#494) must survive the fix: never-diverging
	// byte-identical repeats stay silent.
	for i := 0; i < 12; i++ {
		if msg := s.recordCall("grep", []byte(`{"pattern":"foo","path":"/x"}`), "r"); msg != "" {
			t.Fatalf("byte-identical call %d must not warn: %s", i+1, msg)
		}
	}
	if s.warnings != 0 {
		t.Fatalf("warnings = %d, want 0", s.warnings)
	}
}
