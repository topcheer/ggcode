package agent

import "testing"

// #5669: RecordDelegatedTokenUsage folds delegated spend into st.used
// (the gate's full-truth counter) AND st.delegatedUsed (the attribution
// counter). SessionTokenUsed must return st.used ONLY - returning
// used+delegatedUsed double-counted every delegated token in the status
// face's budget_pct.
func TestIssue5669SessionTokenUsedNoDoubleCount(t *testing.T) {
	a := &Agent{}
	defer a.Close()

	setAgentSessionTokenBudget(a, 1_000_000)

	// Main loop: 500k. Delegated sub-agent: 400k (streamed live).
	if msg, stop := a.RecordSessionTokenUsage(500_000, 0); msg != "" || stop {
		t.Fatalf("500k/1M should not alert, got msg=%q stop=%v", msg, stop)
	}
	a.RecordDelegatedTokenUsage(200_000, 200_000)

	// Full-truth consumption is 900k (delegated already inside st.used).
	if got := a.SessionTokenUsed(); got != 900_000 {
		t.Fatalf("SessionTokenUsed = %d, want 900000 (no double-count of delegated spend)", got)
	}
	// Attribution counter stays independently readable.
	if got := a.DelegatedTokenUsage(); got != 400_000 {
		t.Fatalf("DelegatedTokenUsage = %d, want 400000", got)
	}
}
