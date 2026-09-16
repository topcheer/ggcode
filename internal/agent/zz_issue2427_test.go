package agent

// #2427: the documented 10-call sliding window was dead code - ratios were
// computed from lifetime cumulative counts (categoryCounts/totalTools), so
// exploration-then-execution runs (the module's own Plan-and-Execute
// flagship scenario) never reached the 70% threshold: early lsp/glob calls
// permanently diluted the denominator and the FinOps downgrade hint was
// silently dropped. These pins hold the sliding-window semantics, the
// off-by-one fix (steady-state window is exactly 10), and the locked
// guidance accessors.

import (
	"os"
	"strings"
	"testing"
)

func TestIssue2427SlidingWindowRescuesExecutionBurst(t *testing.T) {
	state := newHeterogeneousModelState()

	// Exploration phase: 8 reasoning/other calls that, under lifetime
	// cumulative ratios, permanently poison the denominator.
	for i := 0; i < 8; i++ {
		state.recordToolCall("lsp_definition", i)
	}
	// Execution burst: 12 exec-heavy calls. Under the sliding window the
	// last 10 are pure execution (ratio 1.0 >= 0.70) and the guidance must
	// fire; under the old cumulative math it was 12/20 = 0.60 < 0.70 and
	// never fired - the exact regression this test pins against.
	for i := 0; i < 12; i++ {
		g := state.recordToolCall("edit_file", 8+i)
		if g != "" {
			if !strings.Contains(g, "execution") && !strings.Contains(g, "Execution") {
				t.Fatalf("unexpected guidance for exec burst: %q", g)
			}
			return // guidance fired during the burst: sliding window works
		}
	}
	t.Fatal("#2427 regression: pure-execution burst after exploration never fired guidance (lifetime cumulative ratios are back)")
}

func TestIssue2427SlidingWindowIgnoresStaleExploration(t *testing.T) {
	// Converse direction: reasoning-heavy guidance must also reflect the
	// window, not the lifetime. 8 lsp calls then 12 read_file calls must
	// NOT fire reasoning guidance (window is pure read/exec by the end).
	state := newHeterogeneousModelState()
	for i := 0; i < 8; i++ {
		state.recordToolCall("lsp_references", i)
	}
	// read_file is exec-class and reasoning-heavy never emits guidance by
	// design (frontier cost is justified - see recordToolCall), so the only
	// guidance that may fire here is execution-heavy - proving the stale
	// lsp phase no longer suppresses it the way lifetime ratios did.
	for i := 0; i < 12; i++ {
		if g := state.recordToolCall("read_file", 8+i); g != "" {
			if !strings.Contains(g, "execution-heavy") {
				t.Fatalf("unexpected guidance type after pure-exec window: %q", g)
			}
			return
		}
	}
	t.Fatal("pure-exec window after exploration never fired execution guidance (lifetime-ratio regression)")
}

func TestIssue2427WindowIsExactlyLookbackSize(t *testing.T) {
	state := newHeterogeneousModelState()
	for i := 0; i < 25; i++ {
		state.recordToolCall("edit_file", i)
	}
	// Off-by-one pin: the trim runs BEFORE the append with >=, so the
	// steady-state window is exactly hmLookbackWindow (10), never 11.
	state.mu.Lock()
	got := len(state.toolHistory)
	state.mu.Unlock()
	if got != hmLookbackWindow {
		t.Fatalf("#2427 off-by-one: steady-state window = %d, want exactly %d", got, hmLookbackWindow)
	}
}

func TestIssue2427GuidanceAccessorsAreLocked(t *testing.T) {
	// Structural pin: the accessors must take the state mutex - the writer
	// (recordToolCall) sets s.guidance under s.mu, so an unlocked read was a
	// data race on a multi-word string.
	b, err := os.ReadFile("heterogeneous_model_guide.go")
	if err != nil {
		t.Skipf("source not readable: %v", err)
	}
	src := string(b)
	for _, fn := range []string{
		"func (a *Agent) GetHeterogeneousModelGuidance",
		"func (a *Agent) ClearHeterogeneousModelGuidance",
	} {
		i := strings.Index(src, fn)
		if i < 0 {
			t.Fatalf("%s not found", fn)
		}
		// next 400 chars must contain the lock acquisition
		body := src[i : i+min(400, len(src)-i)]
		if !strings.Contains(body, ".mu.Lock()") {
			t.Fatalf("#2427: %s must hold heterogeneousModel.mu (writer sets guidance under it)", fn)
		}
	}
}
