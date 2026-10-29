package agent

// #3945 probe: the stop=true return of the session token/time budget must
// actually stop the run - pinned at source level (the wiring is three
// points in a 4000-line function; a behavioral harness for the full
// budget-exhaustion path is disproportionate, same disclosure as #3778).

import (
	"os"
	"strings"
	"testing"
)

func TestIssue3945_StopGateWired(t *testing.T) {
	raw, err := os.ReadFile("agent.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(raw)
	for _, needle := range []string{
		"sessionBudgetStop := false // #3945",
		"sessionBudgetStop = true // #3945: arm the loop-top gate",
		"if sessionBudgetStop {",
	} {
		if !strings.Contains(src, needle) {
			t.Fatalf("wiring missing %q", needle)
		}
	}
	// Exactly one arming site per budget dimension (token + time + cost +
	// delegated-drain; #3959 added the cost twin and #4072 the delegated-drain
	// twin for loop-top gate parity).
	if got := strings.Count(src, "sessionBudgetStop = true"); got != 4 {
		t.Fatalf("want 4 arming sites (token + time + cost + delegated drain), got %d", got)
	}
	// The gate must precede the next LLM call: it sits at the loop top.
	gateIdx := strings.Index(src, "if sessionBudgetStop {")
	loopIdx := strings.Index(src, "for i := 0; a.maxIter <= 0 || i < a.maxIter; i++ {")
	if gateIdx < loopIdx {
		t.Fatal("gate must live inside the run loop")
	}
}
