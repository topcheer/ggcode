package tool

import (
	"strings"
	"testing"
)

// TestTruncateMiddleSpillHook (r382): the omitted middle of oversized
// run_command output must flow through the OmittedOutputSpiller hook so the
// agent-side offloader can persist it, and the hook's notice must be embedded
// in the truncation marker. Without the hook the behavior is byte-identical
// to the previous truncateMiddle.
func TestTruncateMiddleSpillHook(t *testing.T) {
	line := strings.Repeat("x", 200) + "\n"
	var sb strings.Builder
	for i := 0; i < 600; i++ { // ~120KB, well over the 100KB budget
		sb.WriteString(line)
	}
	big := sb.String()

	t.Run("hook receives omitted middle and notice embedded", func(t *testing.T) {
		var gotSource, gotOmitted string
		called := false
		spiller := func(source, omitted string) string {
			called = true
			gotSource, gotOmitted = source, omitted
			return "[spill: /tmp/fake-spill.txt]"
		}
		out := truncateMiddleSpill(big, maxOutputSize, "output", spiller)
		if !called {
			t.Fatal("spiller not called on truncation")
		}
		if gotSource != "output" {
			t.Fatalf("source = %q, want output", gotSource)
		}
		// head 40% + tail 50% of the budget retained -> ~10KB+ omitted here
		if len(gotOmitted) < 8*1024 {
			t.Fatalf("omitted middle too small: %d bytes", len(gotOmitted))
		}
		if !strings.Contains(out, "[spill: /tmp/fake-spill.txt]") {
			t.Fatal("notice not embedded in truncated output")
		}
		if !strings.Contains(out, "lines omitted") {
			t.Fatal("original truncation marker lost")
		}
	})

	t.Run("nil hook preserves legacy behavior", func(t *testing.T) {
		legacy := truncateMiddle(big, maxOutputSize, "output")
		noHook := truncateMiddleSpill(big, maxOutputSize, "output", nil)
		if legacy != noHook {
			t.Fatal("nil spiller must be byte-identical to truncateMiddle")
		}
		if strings.Contains(noHook, "spill") {
			t.Fatal("no spill notice expected without hook")
		}
	})

	t.Run("empty hook return adds nothing", func(t *testing.T) {
		out := truncateMiddleSpill(big, maxOutputSize, "output", func(string, string) string { return "" })
		if strings.Contains(out, "saved to") || strings.Contains(out, "[spill") {
			t.Fatal("empty notice must not alter output")
		}
	})

	t.Run("small output never calls hook", func(t *testing.T) {
		called := false
		spiller := func(string, string) string { called = true; return "x" }
		truncateMiddleSpill("tiny", maxOutputSize, "output", spiller)
		if called {
			t.Fatal("hook called although no truncation happened")
		}
	})
}
