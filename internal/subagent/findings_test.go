package subagent

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/provider"
)

func newFindingsTestManager(t *testing.T) *Manager {
	t.Helper()
	mgr := NewManager(config.SubAgentConfig{MaxConcurrent: 2, Timeout: 5 * time.Second})
	t.Cleanup(mgr.Shutdown)
	return mgr
}

// spawnCompleted spawns an agent and immediately marks it terminal.
func spawnCompleted(t *testing.T, mgr *Manager, name, task, result string, err error) string {
	t.Helper()
	id := mgr.Spawn(name, task, task, nil, context.Background())
	mgr.Complete(id, result, err)
	return id
}

func TestCompletedFindingsNone(t *testing.T) {
	mgr := newFindingsTestManager(t)
	if got := mgr.completedFindings(""); got != "" {
		t.Fatalf("expected empty section for fresh manager, got %q", got)
	}
}

func TestCompletedFindingsCompletedOnly(t *testing.T) {
	mgr := newFindingsTestManager(t)
	spawnCompleted(t, mgr, "alpha", "explore auth", "result A body", nil)
	// Failed agents must not hand off their (possibly partial) results.
	spawnCompleted(t, mgr, "beta", "explore db", "result B body", context.DeadlineExceeded)

	got := mgr.completedFindings("")
	if !strings.Contains(got, "result A body") || !strings.Contains(got, "alpha") {
		t.Fatalf("expected completed agent's finding in section, got %q", got)
	}
	if strings.Contains(got, "beta") || strings.Contains(got, "result B body") {
		t.Fatalf("failed agent should be excluded, got %q", got)
	}
	if !strings.Contains(got, "FINDINGS FROM EARLIER SUB-AGENTS") {
		t.Fatalf("expected section header, got %q", got)
	}
}

func TestCompletedFindingsExcludesSelf(t *testing.T) {
	mgr := newFindingsTestManager(t)
	id := spawnCompleted(t, mgr, "solo", "task", "own result", nil)
	if got := mgr.completedFindings(id); got != "" {
		t.Fatalf("agent should not see its own finding, got %q", got)
	}
}

func TestCompletedFindingsNewestFirst(t *testing.T) {
	mgr := newFindingsTestManager(t)
	spawnCompleted(t, mgr, "first-agent", "task one", "older result", nil)
	time.Sleep(5 * time.Millisecond)
	spawnCompleted(t, mgr, "second-agent", "task two", "newer result", nil)

	got := mgr.completedFindings("")
	iFirst := strings.Index(got, "first-agent")
	iSecond := strings.Index(got, "second-agent")
	if iFirst < 0 || iSecond < 0 {
		t.Fatalf("both findings expected, got %q", got)
	}
	if iSecond > iFirst {
		t.Fatalf("expected newest finding first, got %q", got)
	}
}

func TestCompletedFindingsTruncatesLongResult(t *testing.T) {
	mgr := newFindingsTestManager(t)
	long := strings.Repeat("x", 3000)
	spawnCompleted(t, mgr, "big", "big task", long, nil)

	got := mgr.completedFindings("")
	if !strings.Contains(got, findingsTruncateSuffix) {
		t.Fatalf("expected truncation marker, got %q", got)
	}
	if strings.Count(got, "x") > findingsMaxResultRunes+2 { // small slack for marker
		t.Fatalf("result digest not truncated: %d x runes", strings.Count(got, "x"))
	}
}

func TestCompletedFindingsMaxEntries(t *testing.T) {
	mgr := newFindingsTestManager(t)
	for i := 0; i < 8; i++ {
		spawnCompleted(t, mgr, string(rune('a'+i)), "task", "result", nil)
		time.Sleep(2 * time.Millisecond) // deterministic EndedAt ordering
	}
	got := mgr.completedFindings("")
	if n := strings.Count(got, "\n["); n != findingsMaxEntries {
		t.Fatalf("expected %d entries, got %d", findingsMaxEntries, n)
	}
	// Oldest entries must be dropped.
	if strings.Contains(got, "a —") {
		t.Fatalf("oldest agent should be dropped, got %q", got)
	}
}

func TestCompletedFindingsTotalBudget(t *testing.T) {
	mgr := newFindingsTestManager(t)
	for i := 0; i < 6; i++ {
		spawnCompleted(t, mgr, string(rune('a'+i)), "task", strings.Repeat("y", 2000), nil)
		time.Sleep(2 * time.Millisecond)
	}
	got := mgr.completedFindings("")
	if n := strings.Count(got, "\n["); n >= findingsMaxEntries {
		t.Fatalf("total budget should drop the newest-but-overflowing entry, got %d entries", n)
	}
	if total := len([]rune(got)) - findingsSectionHeaderRunes; total > findingsMaxTotalRunes {
		t.Fatalf("section exceeds budget: %d > %d runes", total, findingsMaxTotalRunes)
	}
}

// findingsSectionHeaderRunes is the approximate rune count of the fixed
// section header + instruction lines in completedFindings.
const findingsSectionHeaderRunes = 220

type noopFindingsRunner struct{}

func (noopFindingsRunner) RunStream(ctx context.Context, prompt string, onEvent func(provider.StreamEvent)) error {
	onEvent(provider.StreamEvent{Type: provider.StreamEventDone})
	return nil
}

func TestRunInjectsFindingsIntoSystemPrompt(t *testing.T) {
	mgr := newFindingsTestManager(t)
	spawnCompleted(t, mgr, "scout", "explore repo", "EARLIER-FINDING-MARKER: auth lives in internal/auth", nil)

	second := mgr.Spawn("wave2", "use prior findings", "use prior findings", nil, context.Background())
	var captured string
	Run(context.Background(), RunnerConfig{
		Task:       "use prior findings",
		Manager:    mgr,
		SubAgentID: second,
		AgentFactory: func(prov provider.Provider, tools interface{}, systemPrompt string, maxTurns int) AgentRunner {
			captured = systemPrompt
			return noopFindingsRunner{}
		},
	})

	if !strings.Contains(captured, "EARLIER-FINDING-MARKER") {
		t.Fatalf("expected earlier finding in second agent's system prompt, got %q", captured)
	}
	if !strings.Contains(captured, "FINDINGS FROM EARLIER SUB-AGENTS") {
		t.Fatalf("expected findings section header in prompt, got %q", captured)
	}
}

func TestRunNoFindingsSectionWhenEmpty(t *testing.T) {
	mgr := newFindingsTestManager(t)
	id := mgr.Spawn("solo", "task", "task", nil, context.Background())
	var captured string
	Run(context.Background(), RunnerConfig{
		Task:       "task",
		Manager:    mgr,
		SubAgentID: id,
		AgentFactory: func(prov provider.Provider, tools interface{}, systemPrompt string, maxTurns int) AgentRunner {
			captured = systemPrompt
			return noopFindingsRunner{}
		},
	})

	if strings.Contains(captured, "FINDINGS FROM EARLIER SUB-AGENTS") {
		t.Fatalf("no prior agents: section must be absent, got %q", captured)
	}
}
