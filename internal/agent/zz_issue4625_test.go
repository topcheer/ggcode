package agent

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/topcheer/ggcode/internal/provider"
)

// #4625 companion tests: four confirmed defects in foresight_calibrate.go.
//  1. predicted-empty + actually-empty must be a CORRECT prediction (was mismatch)
//  2. the guidance threshold must only fire on an actual mismatch
//  3. the iteration fallback must be bounded (adjacent iterations only) and
//     stale predictions must expire
//  4. snippet truncation must be rune-safe

func issue4625Record(t *testing.T, s *foresightCalibrateState, text, tool string, iteration int) {
	t.Helper()
	s.recordPrediction(text, []provider.ToolCallDelta{{Name: tool}}, iteration)
}

func TestIssue4625PredictedEmptyAndActuallyEmptyIsCorrect(t *testing.T) {
	s := newForesightCalibrateState()
	issue4625Record(t, s, "The file should be empty after the cleanup runs.", "read_file", 4)
	if len(s.predictions) != 1 {
		t.Fatalf("pending predictions = %d, want 1", len(s.predictions))
	}
	if !s.predictions[0].predictedEmpty {
		t.Fatalf("predictedEmpty = false, want true (emptiness is its own polarity)")
	}
	// Result IS empty (zero bytes) - the prediction was fully correct.
	if got := s.checkCalibration("read_file", "", false, 4); got != "" {
		t.Fatalf("correct empty prediction produced guidance: %q", got)
	}
	if s.mismatches != 0 {
		t.Fatalf("mismatches = %d, want 0 (predicted empty + actually empty is correct)", s.mismatches)
	}
}

func TestIssue4625PredictedEmptyButContentIsMismatch(t *testing.T) {
	s := newForesightCalibrateState()
	issue4625Record(t, s, "The file should be empty after the cleanup runs.", "read_file", 4)
	// Result has real content - stale world model, must count.
	s.checkCalibration("read_file", "stale line one\nstale line two\n", false, 4)
	if s.mismatches != 1 {
		t.Fatalf("mismatches = %d, want 1 (predicted empty + content is a genuine mismatch)", s.mismatches)
	}
}

func TestIssue4625NoGuidanceOnCorrectPrediction(t *testing.T) {
	s := newForesightCalibrateState()
	// Threshold already reached on prior, real mismatches.
	s.mismatches = 3
	s.warnCount = 0
	issue4625Record(t, s, "This test should pass now.", "run_command", 7)
	out := s.checkCalibration("run_command", "ok\nall tests passed\n", false, 7)
	if out != "" {
		t.Fatalf("correct prediction injected mismatch-flavored guidance: %q", out)
	}
	if s.warnCount != 0 {
		t.Fatalf("warnCount = %d, want 0 (guidance must only fire on an actual mismatch)", s.warnCount)
	}
}

func TestIssue4625GuidanceStillFiresOnThirdRealMismatch(t *testing.T) {
	s := newForesightCalibrateState()
	s.mismatches = 2
	issue4625Record(t, s, "The build should pass.", "run_command", 2)
	out := s.checkCalibration("run_command", "exit status 1\ncompilation failed", true, 2)
	if out == "" {
		t.Fatal("third real mismatch must inject guidance")
	}
	if !strings.Contains(out, "predicted success but the result indicates failure") {
		t.Fatalf("guidance detail = %q, want the actual mismatch detail", out)
	}
	if s.warnCount != 1 {
		t.Fatalf("warnCount = %d, want 1", s.warnCount)
	}
}

func TestIssue4625StalePredictionNotConsumedByLaterSameNameCall(t *testing.T) {
	s := newForesightCalibrateState()
	issue4625Record(t, s, "This command should work.", "run_command", 1)
	// 9 iterations later the same tool name runs - the prediction must be
	// expired, not consumed and charged as a mismatch.
	if got := s.checkCalibration("run_command", "exit status 1", true, 10); got != "" {
		t.Fatalf("stale prediction consumed 9 iterations later: %q", got)
	}
	if s.mismatches != 0 {
		t.Fatalf("mismatches = %d, want 0 (stale prediction must never be charged)", s.mismatches)
	}
	if len(s.predictions) != 0 {
		t.Fatalf("pending predictions = %d, want 0 (expired by age)", len(s.predictions))
	}
}

func TestIssue4625AdjacentIterationFallbackStillSettles(t *testing.T) {
	s := newForesightCalibrateState()
	issue4625Record(t, s, "This command should work.", "run_command", 1)
	// Documented settle window: one iteration later.
	out := s.checkCalibration("run_command", "done ok", false, 2)
	if out != "" || s.mismatches != 0 {
		t.Fatalf("adjacent-iteration settle misjudged: out=%q mismatches=%d", out, s.mismatches)
	}
	if len(s.predictions) != 0 {
		t.Fatalf("pending predictions = %d, want 0 (consumed)", len(s.predictions))
	}
}

func TestIssue4625SnippetTruncationRuneSafe(t *testing.T) {
	// Multibyte prediction text long enough to cross the 120-byte truncation
	// boundary; the byte-117 cut would split a 3-byte CJK rune. The 79-byte
	// ASCII prefix positions the sentence extraction window so the cut point
	// lands inside the CJK run.
	text := strings.Repeat("x", 79) + " I expect this to " + strings.Repeat("预", 40)
	snippet := extractForesightSnippet(text)
	if snippet == "" {
		t.Fatal("snippet empty, want the prediction sentence")
	}
	if len(snippet) <= 120 {
		t.Fatalf("snippet not truncated (len=%d), want truncation to engage", len(snippet))
	}
	if !utf8.ValidString(snippet) {
		t.Fatalf("snippet is not valid UTF-8 after truncation: %q", snippet)
	}
}
