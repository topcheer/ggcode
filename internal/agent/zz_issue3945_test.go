package agent

import (
	"strings"
	"testing"
)

func TestIssue3945_StopGateWired(t *testing.T) {
	raw, err := readAgentAndProcessingSources()
	if err != nil {
		t.Fatal(err)
	}
	src := string(raw)
	for _, needle := range []string{
		"sessionBudgetStop         bool",
		"st.sessionBudgetStop = true",
		"if st.sessionBudgetStop {",
	} {
		if !strings.Contains(src, needle) {
			t.Fatalf("wiring missing %q", needle)
		}
	}

	if got := strings.Count(src, "st.sessionBudgetStop = true"); got != 4 {
		t.Fatalf("want 4 arming sites (token + time + cost + delegated drain), got %d", got)
	}

	gateIdx := strings.Index(src, "if st.sessionBudgetStop {")
	loopIdx := strings.Index(src, "for i := 0; a.maxIter <= 0 || i < a.maxIter; i++ {")
	if gateIdx < loopIdx {
		t.Fatal("gate must live inside the run loop")
	}
}
