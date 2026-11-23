package agent

import "testing"

func Test1494SessionBudgetTiers(t *testing.T) {
	a := &Agent{}
	a.SetSessionTokenBudget(1000)
	a.resetSessionTokenUsage()
	if _, stop := a.RecordSessionTokenUsage(900, 0); stop {
		t.Fatal("80% must be guidance, not stop")
	}
	msg, stop := a.RecordSessionTokenUsage(60, 0)
	if stop || msg == "" {
		t.Fatalf("95%% must warn: stop=%v msg=%q", stop, msg)
	}
	_, stop = a.RecordSessionTokenUsage(100, 0)
	if !stop {
		t.Fatal("100%+ must stop")
	}

	a.resetSessionTokenUsage()
	if _, stop := a.RecordSessionTokenUsage(10, 0); stop {
		t.Fatal("after reset the budget must re-arm")
	}
}
