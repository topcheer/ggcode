package agent

import (
	"strings"
	"testing"
)

func TestIssue543SetSessionTokenBudgetStoresValue(t *testing.T) {
	ag := NewAgent(nil, nil, "sys", 5)
	defer ag.Close()

	if got := ag.SessionTokenBudget(); got != 0 {
		t.Fatalf("initial budget: want 0, got %d", got)
	}
	ag.SetSessionTokenBudget(123456)
	if got := ag.SessionTokenBudget(); got != 123456 {
		t.Fatalf("after SetSessionTokenBudget(123456): want 123456, got %d", got)
	}

	ag.SetSessionTokenBudget(0)
	if got := ag.SessionTokenBudget(); got != 0 {
		t.Fatalf("after SetSessionTokenBudget(0): want 0, got %d", got)
	}
}

func TestIssue543RecordSessionTokenUsageThresholds(t *testing.T) {
	ag := NewAgent(nil, nil, "sys", 5)
	defer ag.Close()

	ag.SetSessionTokenBudget(1000)

	msg, stop := ag.RecordSessionTokenUsage(400, 100)
	if msg != "" || stop {
		t.Fatalf("at 50%%: want silent, got msg=%q stop=%v", msg, stop)
	}

	msg, stop = ag.RecordSessionTokenUsage(200, 100)
	if msg == "" || stop {
		t.Fatalf("at 80%%: want warning, got msg=%q stop=%v", msg, stop)
	}
	if !strings.Contains(msg, "80%") {
		t.Fatalf("80%% warning should mention the percentage: %q", msg)
	}

	msg, stop = ag.RecordSessionTokenUsage(1, 0)
	if msg != "" || stop {
		t.Fatalf("between thresholds: want silent, got msg=%q stop=%v", msg, stop)
	}

	msg, stop = ag.RecordSessionTokenUsage(50, 100)
	if msg == "" || stop {
		t.Fatalf("at 95%%: want urgent warning without stop, got msg=%q stop=%v", msg, stop)
	}
	if !strings.Contains(msg, "95%") {
		t.Fatalf("95%% warning should mention the percentage: %q", msg)
	}

	msg, stop = ag.RecordSessionTokenUsage(50, 100)
	if msg == "" || !stop {
		t.Fatalf("at 100%%+: want hard stop, got msg=%q stop=%v", msg, stop)
	}
	if !strings.Contains(msg, "exhausted") {
		t.Fatalf("hard-stop message should say exhausted: %q", msg)
	}

	ag2 := NewAgent(nil, nil, "sys", 5)
	defer ag2.Close()
	msg, stop = ag2.RecordSessionTokenUsage(1_000_000, 1_000_000)
	if msg != "" || stop {
		t.Fatalf("no budget: want silent non-stop, got msg=%q stop=%v", msg, stop)
	}
}

func TestIssue544SpecGamingGrepForSkipMarkerNotFlagged(t *testing.T) {
	ag := NewAgent(nil, nil, "sys", 5)
	defer ag.Close()

	stats := newRunStats("fix the login bug")
	stats.CommandsRun = []string{
		`grep -rn "t.Skip(" ./internal/`,
		"rg '@pytest.mark.skip' tests/",
		`git grep "xit(" -- src/`,
	}
	if msg := ag.checkSpecGaming(stats, "fix the login bug"); msg != "" {
		t.Fatalf("investigative grep for skip markers must not be flagged, got: %s", msg)
	}
}
