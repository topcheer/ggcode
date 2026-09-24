package agent

import (
	"math"
	"strings"
	"testing"
)

// moCycle generates n tool names cycling through the given set.
func moCycle(n int, names []string) []string {
	seq := make([]string, 0, n)
	for i := 0; i < n; i++ {
		seq = append(seq, names[i%len(names)])
	}
	return seq
}

func TestMeltdownEntropy_KnownValues(t *testing.T) {
	// Single repeated tool: zero entropy.
	bits, distinct := moEntropy([]string{"a", "a", "a", "a"})
	if bits != 0 || distinct != 1 {
		t.Fatalf("uniform single tool: got %.2f bits, %d distinct, want 0 bits, 1", bits, distinct)
	}
	// Four tools uniform: exactly 2 bits.
	bits, distinct = moEntropy([]string{"a", "b", "c", "d"})
	if math.Abs(bits-2.0) > 1e-9 || distinct != 4 {
		t.Fatalf("4 uniform tools: got %.4f bits, %d distinct, want 2 bits, 4", bits, distinct)
	}
	// Empty window: no entropy, no distinct.
	bits, distinct = moEntropy(nil)
	if bits != 0 || distinct != 0 {
		t.Fatalf("empty window: got %.2f bits, %d distinct, want 0, 0", bits, distinct)
	}
}

func TestMeltdown_QuietOnStructuredLoop(t *testing.T) {
	s := newMeltdownOnsetState()
	// Tight read/edit/verify loop: low entropy, must never fire.
	for i, name := range moCycle(60, []string{"read_file", "edit_file", "run_command"}) {
		if msg := s.recordToolCall(name, i+1); msg != "" {
			t.Fatalf("structured loop fired at call %d: %s", i+1, msg)
		}
	}
}

func TestMeltdown_QuietOnHealthyExploration(t *testing.T) {
	s := newMeltdownOnsetState()
	// Six tools in even rotation (~2.56 bits): broad but structured.
	for i, name := range moCycle(80, []string{"read_file", "grep", "glob", "run_command", "edit_file", "code_search"}) {
		if msg := s.recordToolCall(name, i+1); msg != "" {
			t.Fatalf("healthy 6-tool exploration fired at call %d: %s", i+1, msg)
		}
	}
}

func TestMeltdown_FiresOnErraticPattern(t *testing.T) {
	s := newMeltdownOnsetState()
	names := []string{"read_file", "web_search", "browser", "grep", "edit_file",
		"list_directory", "run_command", "web_fetch", "lsp_definition", "glob"}
	var fired []string
	for i, name := range moCycle(40, names) {
		if msg := s.recordToolCall(name, i+1); msg != "" {
			fired = append(fired, msg)
		}
	}
	// Persistence gate: first evaluation is at call 16 (hit #1), second at
	// call 17 (hit #2) -> fire. Later firings capped by cooldown/maxWarnings.
	if len(fired) == 0 {
		t.Fatal("erratic 10-tool pattern never fired")
	}
	if len(fired) > moMaxWarnings {
		t.Fatalf("fired %d times, want <= %d", len(fired), moMaxWarnings)
	}
	if !strings.Contains(fired[0], "Meltdown Onset") {
		t.Fatalf("unexpected warning text: %s", fired[0])
	}
	if !strings.Contains(fired[0], "arXiv:2603.29231") {
		t.Fatalf("warning should cite research basis: %s", fired[0])
	}
	// First fire must respect the persistence gate (not before call 17).
	// Re-derive: rebuild and find first firing index.
	s2 := newMeltdownOnsetState()
	firstFire := -1
	for i, name := range moCycle(40, names) {
		if msg := s2.recordToolCall(name, i+1); msg != "" {
			firstFire = i + 1
			break
		}
	}
	if firstFire < moWindow+1 {
		t.Fatalf("fired at call %d before persistence gate (window=%d)", firstFire, moWindow)
	}
}

func TestMeltdown_CooldownBetweenAlerts(t *testing.T) {
	s := newMeltdownOnsetState()
	names := []string{"read_file", "web_search", "browser", "grep", "edit_file",
		"list_directory", "run_command", "web_fetch", "lsp_definition", "glob"}
	type event struct {
		call int
		msg  string
	}
	var events []event
	for i, name := range moCycle(80, names) {
		if msg := s.recordToolCall(name, i+1); msg != "" {
			events = append(events, event{call: i + 1, msg: msg})
		}
	}
	if len(events) < 2 {
		t.Fatalf("expected at least 2 alerts over 80 erratic calls, got %d", len(events))
	}
	if events[1].call-events[0].call < moCooldownCalls {
		t.Fatalf("cooldown violated: alerts at calls %d and %d, need >= %d apart",
			events[0].call, events[1].call, moCooldownCalls)
	}
}

func TestMeltdown_Reset(t *testing.T) {
	s := newMeltdownOnsetState()
	names := []string{"read_file", "web_search", "browser", "grep", "edit_file",
		"list_directory", "run_command", "web_fetch", "lsp_definition", "glob"}
	for i, name := range moCycle(20, names) {
		s.recordToolCall(name, i+1)
	}
	s.reset()
	if len(s.window) != 0 || s.consecutiveHits != 0 || s.callsSinceAlert != 0 || s.warningCount != 0 {
		t.Fatalf("reset left state: window=%d hits=%d sinceAlert=%d warnings=%d",
			len(s.window), s.consecutiveHits, s.callsSinceAlert, s.warningCount)
	}
	// After reset the window must refill before any evaluation can fire.
	for i, name := range moCycle(moWindow-1, names) {
		if msg := s.recordToolCall(name, i+1); msg != "" {
			t.Fatalf("fired after reset before window refill (call %d): %s", i+1, msg)
		}
	}
}

func TestMeltdown_DecayOnMiss(t *testing.T) {
	s := newMeltdownOnsetState()
	// Fill the window with a low-entropy structured pattern so every
	// evaluation misses the threshold.
	for i := 0; i < moWindow; i++ {
		if msg := s.recordToolCall("read_file", i+1); msg != "" {
			t.Fatalf("structured fill must not fire: %s", msg)
		}
	}
	// White-box: simulate accumulated persistence from earlier erratic
	// stretches (odd value so a miss cannot accidentally re-pass the
	// persistence gate), then verify a miss halves it instead of
	// resetting to 0.
	s.consecutiveHits = 3
	s.recordToolCall("read_file", moWindow+1)
	if s.consecutiveHits != 1 {
		t.Fatalf("miss should halve hits 3->1, got %d", s.consecutiveHits)
	}
	// One more miss must floor it back to 0 (1->0).
	s.recordToolCall("read_file", moWindow+2)
	if s.consecutiveHits != 0 {
		t.Fatalf("repeated misses should decay hits to 0, got %d", s.consecutiveHits)
	}
}
