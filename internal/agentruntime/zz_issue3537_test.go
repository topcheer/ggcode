package agentruntime

import (
	"context"
	"sync"
	"testing"

	"github.com/topcheer/ggcode/internal/subagent"
	"github.com/topcheer/ggcode/internal/tool"
)

type cancelStuckFake struct {
	mu     sync.Mutex
	n      int
	ids    []string
	cancel context.CancelFunc
}

func (f *cancelStuckFake) Launch(_ context.Context, opts tool.LaunchOptions) (string, string, error) {
	f.mu.Lock()
	f.n++
	id := "stuck-" + opts.Name
	f.ids = append(f.ids, opts.Name)
	n := f.n
	c := f.cancel
	f.mu.Unlock()
	if n >= 2 && c != nil {
		c()
	}
	return id, "", nil
}

func (f *cancelStuckFake) Snapshot(id string) (subagent.Snapshot, bool) {
	return subagent.Snapshot{ID: id, Status: subagent.StatusRunning}, true
}

func (f *cancelStuckFake) RunningCount() int { return 0 }

func (f *cancelStuckFake) launchCount() int { f.mu.Lock(); defer f.mu.Unlock(); return f.n }

func TestIssue3537_LoopTopCancelKeepsLiveStepID(t *testing.T) {
	f := &cancelStuckFake{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f.cancel = cancel

	spec := WorkflowSpec{
		Steps:     []WorkflowStep{{ID: "s1", Task: "t"}, {ID: "s2", Task: "t"}},
		Synthesis: "fold",
	}
	rep := RunWorkflow(ctx, f, f, spec, wfPoll)

	if !rep.Partial {
		t.Fatal("cancelled run must report Partial")
	}

	if got := len(rep.LiveStepID); got != 2 {
		t.Fatalf("LiveStepID must carry both live steps, got %d: %v", got, rep.LiveStepID)
	}
	for _, step := range []string{"s1", "s2"} {
		if id, ok := rep.LiveStepID[step]; !ok || id == "" {
			t.Fatalf("step %s missing a live agent ID: %v", step, rep.LiveStepID)
		}
	}

	if got := f.launchCount(); got != 2 {
		t.Fatalf("synthesis must not launch on the cancelled exit, launches=%d", got)
	}
}
