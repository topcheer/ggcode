package agent

import (
	"os"
	"strings"
	"testing"
)

// #3959 guard: the per-run reset block and the 100% stop branch must keep
// the USD cost ladder wired with the same discipline as its token/time
// twins. The reset call was written, tested, and documented as "per-run"
// but never invoked in production, so one stop latched stopGiven for the
// process lifetime (daemon-long guardrail blackout, #1494 case A replay).
// Source-scan guard: the wiring lives inline in the run loop, which a pure
// behavioral test cannot reach without a full provider harness.
func TestSessionCostLimitResetWiredInRunStart(t *testing.T) {
	src, err := os.ReadFile("agent.go")
	if err != nil {
		t.Fatalf("read agent.go: %v", err)
	}
	text := string(src)

	// All three siblings must sit in the per-run reset block.
	for _, call := range []string{
		"a.resetSessionTokenUsage()",
		"a.resetSessionTimeUsage()",
		"a.resetSessionCostUsage()",
	} {
		if !strings.Contains(text, call) {
			t.Errorf("per-run reset block is missing %s - tier flags latch across runs (#1494 case A)", call)
		}
	}
	// The three calls must be adjacent (same reset block), not scattered:
	// locate the #1494 anchor and require the cost twin within 800 bytes
	// after it.
	anchor := strings.Index(text, "a.resetSessionTokenUsage()")
	if anchor < 0 {
		t.Fatal("token reset anchor not found")
	}
	window := text[anchor:min(anchor+800, len(text))]
	if !strings.Contains(window, "a.resetSessionCostUsage()") {
		t.Error("a.resetSessionCostUsage() is not adjacent to the token/time reset twins - verify it sits in the same per-run reset block")
	}
}

// #3959 guard: the 100% cost stop must arm the loop-top sessionBudgetStop
// gate exactly like the token (#3945) and time (r415/#3945) twins; without
// it a fully-consumed spend limit only emits an event and the loop keeps
// burning metered tokens.
func TestSessionCostLimitStopArmsLoopGate(t *testing.T) {
	src, err := os.ReadFile("agent.go")
	if err != nil {
		t.Fatalf("read agent.go: %v", err)
	}
	text := string(src)

	anchor := strings.Index(text, "a.RecordSessionCostUsage(")
	if anchor < 0 {
		t.Fatal("RecordSessionCostUsage call site not found in run loop")
	}
	// The stop branch extends from the record call to the time-budget twin
	// further down; bound the window generously but do not cross into it.
	end := strings.Index(text[anchor:], "a.RecordSessionTimeUsage(")
	if end < 0 {
		t.Fatal("time budget twin not found after cost record site")
	}
	branch := text[anchor : anchor+end]
	if !strings.Contains(branch, "sessionBudgetStop = true") {
		t.Error("cost 100%% stop branch does not arm sessionBudgetStop - weaker stop semantics than token/time twins (#3945 parity)")
	}
}
