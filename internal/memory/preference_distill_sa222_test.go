package memory

import (
	"strings"
	"testing"
)

// sa-222 probes: corrective-form preference capture. Users reteach the
// same lesson every session because "不对，用 pnpm 跑" / "no, use pnpm
// instead" carry no durable-intent marker (always/never/以后). A
// corrective marker alone must NOT capture (bug reports are not
// preferences) - it needs an action-word co-occurrence.

func TestCorrectivePreferenceCaptured(t *testing.T) {
	for _, in := range []string{
		"不对，用 pnpm 跑",
		"No, use pnpm instead",
		"不对, 换成 pnpm",
		"instead of npm, run pnpm install",
		"别用 npm，改用 pnpm",
	} {
		if got := DistillUserPreferences(in); len(got) != 1 {
			t.Errorf("corrective preference %q must be captured exactly once, got %v", in, got)
		}
	}
}

func TestCorrectionWithoutActionNotCaptured(t *testing.T) {
	for _, in := range []string{
		"不对，这个结果错了",
		"No, that's wrong",
		"不对",
		"this output is wrong, fix it",
	} {
		if got := DistillUserPreferences(in); len(got) != 0 {
			t.Errorf("correction without an action word must not become a preference: %q -> %v", in, got)
		}
	}
}

// Explicit markers keep working unchanged (regression guard for the
// isPreferenceSentence extension).
func TestExplicitMarkersStillCapture(t *testing.T) {
	if got := DistillUserPreferences("from now on always run tests with -p=1"); len(got) != 1 {
		t.Fatalf("explicit marker capture regressed: %v", got)
	}
	if got := DistillUserPreferences("以后都不要用 npm"); len(got) != 1 {
		t.Fatalf("explicit zh marker capture regressed: %v", got)
	}
}

// Mixed message: one corrective preference + one complaint - only the
// actionable one is captured, and the per-run cap still applies.
func TestCorrectiveMixedWithComplaint(t *testing.T) {
	in := "不对，这个结果错了。不对，用 pnpm 跑测试。"
	got := DistillUserPreferences(in)
	if len(got) != 1 || !strings.Contains(got[0], "pnpm") {
		t.Fatalf("mixed message must capture only the actionable correction: %v", got)
	}
}
