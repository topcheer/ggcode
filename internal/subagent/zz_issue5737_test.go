package subagent

// #5737: the #5013 same-file reread circuit breaker killed legitimate
// read-only research sub-agent runs - reading the same file repeatedly IS
// the task shape for research seats (an incident run was scrapped after 4
// reads of one file). These tests pin the task-type-aware calibration:
// research policy (explicit or auto-detected from a read-only toolset)
// survives read-heavy shapes, warns once before acting, and terminates
// only on continued escalation; edit-capable runs keep the strict #5013
// behavior.

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/provider"
)

func TestIssue5737ResearchPolicySurvivesIncidentShape(t *testing.T) {
	b := newRereadBreakerWithPolicy(RereadPolicyResearch)
	args := readArgs(t, "internal/im/emitter.go")

	// The exact incident shape: reads of the same file well past the strict
	// limit of 3, with no edit possible. The strict breaker terminated the
	// run at 4; research calibration must not even warn until its own
	// threshold.
	for i := 1; i <= rereadResearchWarnLimit; i++ {
		_, _, action := b.recordAction("read_file", args)
		if action != rereadNone {
			t.Fatalf("read %d: action=%v, want none before the warn threshold", i, action)
		}
	}
	if b.tripped != "" {
		t.Fatalf("breaker tripped at %d reads; research runs must survive", rereadResearchWarnLimit)
	}
}

func TestIssue5737ResearchPolicyWarnsOnceThenTrips(t *testing.T) {
	b := newRereadBreakerWithPolicy(RereadPolicyResearch)
	args := readArgs(t, "big.go")

	warns := 0
	for i := 1; i < rereadResearchTripLimit; i++ {
		path, n, action := b.recordAction("read_file", args)
		switch action {
		case rereadWarn:
			warns++
			if n != rereadResearchWarnLimit+1 {
				t.Fatalf("warn at read %d, want %d", n, rereadResearchWarnLimit+1)
			}
			if path != "big.go" {
				t.Fatalf("warn path = %q, want big.go", path)
			}
			if !strings.Contains(b.warning(path, n), "terminate") {
				t.Fatalf("warning must state the termination consequence: %q", b.warning(path, n))
			}
		case rereadTrip:
			t.Fatalf("trip at read %d, want only at %d", i, rereadResearchTripLimit)
		}
	}
	if warns != 1 {
		t.Fatalf("warned %d times, want exactly one advisory", warns)
	}

	path, n, action := b.recordAction("read_file", args)
	if action != rereadTrip || path != "big.go" || n != rereadResearchTripLimit {
		t.Fatalf("read %d: action=%v path=%q, want trip at %d for big.go", n, action, path, rereadResearchTripLimit)
	}
	if !strings.Contains(b.reason(), "despite a prior warning") {
		t.Fatalf("research trip reason must mention the prior warning: %q", b.reason())
	}
	if !strings.Contains(b.reason(), "read 24 times") {
		t.Fatalf("trip reason must carry the count: %q", b.reason())
	}
	// Latched: no further reports after the trip.
	if _, _, action := b.recordAction("read_file", args); action != rereadNone {
		t.Fatal("breaker must latch after terminating")
	}
}

func TestIssue5737ResearchPolicyEditResetsWarnAndCounts(t *testing.T) {
	b := newRereadBreakerWithPolicy(RereadPolicyResearch)
	args := readArgs(t, "main.go")
	edit := "{\"path\":\"main.go\",\"old_text\":\"a\",\"new_text\":\"b\"}"

	// Reads past the trip limit, with an edit resetting the window right
	// after the warn threshold: verification reads after a mutation are
	// legitimate and must neither warn-latch nor trip.
	for i := 1; i <= rereadResearchTripLimit+2; i++ {
		if _, _, action := b.recordAction("edit_file", edit); action != rereadNone {
			t.Fatalf("edit must never trip or warn (read %d)", i)
		}
		if _, _, action := b.recordAction("read_file", args); action == rereadTrip {
			t.Fatalf("trip at read %d despite intervening edits", i)
		}
	}
	if b.tripped != "" {
		t.Fatal("intervening edits must keep resetting the window: no trip")
	}
}

func TestIssue5737DefaultPolicyForcesStrict(t *testing.T) {
	b := newRereadBreakerWithPolicy(RereadPolicyDefault)
	args := readArgs(t, "a.go")
	for i := 0; i < rereadBreakerLimit; i++ {
		if _, _, action := b.recordAction("read_file", args); action != rereadNone {
			t.Fatalf("strict breaker fired at read %d", i+1)
		}
	}
	if _, _, action := b.recordAction("read_file", args); action != rereadTrip {
		t.Fatal("strict policy must still trip after the #5013 limit")
	}
}

type issue5737ToolInfo struct{ name string }

func (ti issue5737ToolInfo) Name() string { return ti.name }

func TestIssue5737AutoPolicyResolution(t *testing.T) {
	readTool := issue5737ToolInfo{"read_file"}
	editTool := issue5737ToolInfo{"edit_file"}
	grepTool := issue5737ToolInfo{"grep"}

	cases := []struct {
		name     string
		explicit RereadPolicy
		allowed  []string
		all      []ToolInfo
		want     RereadPolicy
	}{
		{"read-only toolset gets research", RereadPolicyAuto, []string{"read_file", "grep"}, []ToolInfo{readTool, editTool, grepTool}, RereadPolicyResearch},
		{"edit-capable toolset keeps strict", RereadPolicyAuto, []string{"read_file", "edit_file"}, []ToolInfo{readTool, editTool}, RereadPolicyDefault},
		{"unrestricted with registry edits keeps strict", RereadPolicyAuto, nil, []ToolInfo{readTool, editTool}, RereadPolicyDefault},
		{"unrestricted read-only registry gets research", RereadPolicyAuto, nil, []ToolInfo{readTool, grepTool}, RereadPolicyResearch},
		{"explicit research wins over edit tools", RereadPolicyResearch, []string{"edit_file"}, []ToolInfo{editTool}, RereadPolicyResearch},
		{"explicit strict wins over read-only toolset", RereadPolicyDefault, []string{"read_file"}, []ToolInfo{readTool}, RereadPolicyDefault},
	}
	for _, c := range cases {
		if got := rereadPolicyForRun(c.explicit, c.allowed, c.all); got != c.want {
			t.Errorf("%s: policy=%v, want %v", c.name, got, c.want)
		}
	}
}

// issue5737ReadRunner reads the same path readCount times, then finishes.
type issue5737ReadRunner struct {
	path      string
	readCount int
}

func (r *issue5737ReadRunner) RunStream(ctx context.Context, prompt string, onEvent func(provider.StreamEvent), origin ...string) error {
	args, _ := json.Marshal(map[string]string{"path": r.path})
	for i := 0; i < r.readCount; i++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		onEvent(provider.StreamEvent{Type: provider.StreamEventToolCallDone,
			Tool: provider.ToolCallDelta{ID: "t", Name: "read_file", Arguments: args}})
		time.Sleep(time.Millisecond)
	}
	return nil
}

func issue5737Run(t *testing.T, mgr *Manager, allowed []string, policy RereadPolicy, readCount int) *SubAgent {
	t.Helper()
	id := mgr.Spawn("runner", "task", "task", allowed, context.Background())
	runner := &issue5737ReadRunner{path: "research.go", readCount: readCount}
	allTools := []ToolInfo{issue5737ToolInfo{"read_file"}, issue5737ToolInfo{"edit_file"}, issue5737ToolInfo{"grep"}}
	Run(context.Background(), RunnerConfig{
		Task:         "task",
		Manager:      mgr,
		SubAgentID:   id,
		AllowedTools: allowed,
		AllTools:     allTools,
		RereadPolicy: policy,
		AgentFactory: func(prov provider.Provider, tools interface{}, systemPrompt string, maxTurns int) AgentRunner {
			return runner
		},
	})
	sa, ok := mgr.Get(id)
	if !ok {
		t.Fatal("sub-agent record missing after Run")
	}
	return sa
}

// TestIssue5737RunnerAutoDetectsReadOnlyToolset is the incident regression
// pin: a run whose toolset has no edit-class tool must NOT be terminated
// for re-reading files (the #5737 sa-17 shape: 4 reads -> full-run scrap).
func TestIssue5737RunnerAutoDetectsReadOnlyToolset(t *testing.T) {
	mgr := NewManager(config.SubAgentConfig{MaxConcurrent: 2, Timeout: 10 * time.Second})

	// Read-only toolset: 6 same-file reads (past the strict limit of 3)
	// must complete cleanly.
	sa := issue5737Run(t, mgr, []string{"read_file", "grep"}, RereadPolicyAuto, 6)
	if sa.Error != nil {
		t.Fatalf("read-only run killed for re-reading (the #5737 incident): %v", sa.Error)
	}
	if sa.CurrentStatus() != StatusCompleted {
		t.Fatalf("read-only run status = %s, want completed", sa.CurrentStatus())
	}

	// Same shape but edit-capable toolset: strict #5013 breaker still trips.
	sa2 := issue5737Run(t, mgr, []string{"read_file", "edit_file"}, RereadPolicyAuto, 6)
	if sa2.Error == nil || !strings.Contains(sa2.Error.Error(), "reread breaker") {
		t.Fatalf("edit-capable run must keep the strict breaker, got: %v", sa2.Error)
	}
}

// TestIssue5737RunnerResearchWarnsViaMailboxThenTerminates pins the
// warn-and-continue behavior: the advisory reaches the run mailbox (the
// channel the agent loop drains as user-role input) and the event stream;
// only continued escalation past the trip limit terminates.
func TestIssue5737RunnerResearchWarnsViaMailboxThenTerminates(t *testing.T) {
	mgr := NewManager(config.SubAgentConfig{MaxConcurrent: 1, Timeout: 10 * time.Second})
	id := mgr.Spawn("researcher", "task", "task", nil, context.Background())
	runner := &issue5737ReadRunner{path: "big.go", readCount: 40}
	Run(context.Background(), RunnerConfig{
		Task:         "task",
		Manager:      mgr,
		SubAgentID:   id,
		RereadPolicy: RereadPolicyResearch,
		AgentFactory: func(prov provider.Provider, tools interface{}, systemPrompt string, maxTurns int) AgentRunner {
			return runner
		},
	})
	sa, ok := mgr.Get(id)
	if !ok {
		t.Fatal("sub-agent record missing after Run")
	}
	if sa.Error == nil || !strings.Contains(sa.Error.Error(), "reread breaker") {
		t.Fatalf("run past the trip limit must terminate with the breaker error, got: %v", sa.Error)
	}
	if !strings.Contains(sa.Error.Error(), "prior warning") {
		t.Fatalf("termination error must reference the prior warning: %v", sa.Error)
	}

	var sawWarnEvent bool
	for _, ev := range sa.Events() {
		if ev.Type == AgentEventText && strings.Contains(ev.Text, "[reread-breaker]") && strings.Contains(ev.Text, "warning") {
			sawWarnEvent = true
		}
	}
	if !sawWarnEvent {
		t.Fatal("event stream must carry the warn advisory before termination")
	}

	msgs := mgr.DrainMailbox(id)
	found := false
	for _, m := range msgs {
		if m.From == "reread-breaker" && strings.Contains(m.Message, "has been read 13 times") {
			found = true
		}
	}
	if !found {
		t.Fatalf("mailbox must receive the warning for the model to heed, got %d message(s)", len(msgs))
	}
}
