package agentruntime

// #3544 probe: a finalized step must free its window slot IMMEDIATELY so a
// ready downstream step launches while the finished step's batch siblings
// are still running. The old compaction only ran when ALL in-flight steps
// had finished, so with workflowLayerCap saturated the downstream step
// starved below the cap.

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/subagent"
	"github.com/topcheer/ggcode/internal/tool"
)

type winFake struct {
	mu      sync.Mutex
	seq     int
	launchN map[string]int // launch order per name
	names   []string       // launch order
	polls   map[string]int
}

func newWinFake() *winFake {
	return &winFake{launchN: map[string]int{}, polls: map[string]int{}}
}

func (f *winFake) Launch(_ context.Context, opts tool.LaunchOptions) (string, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seq++
	f.launchN[opts.Name] = f.seq
	f.names = append(f.names, opts.Name)
	return "w-" + opts.Name, "", nil
}

// s0 completes after one poll; everyone else runs forever.
func (f *winFake) Snapshot(id string) (subagent.Snapshot, bool) {
	f.mu.Lock()
	f.polls[id]++
	n := f.polls[id]
	f.mu.Unlock()
	if id == "w-wf-s0" && n >= 2 {
		return subagent.Snapshot{ID: id, Status: subagent.StatusCompleted, Result: "s0 done"}, true
	}
	return subagent.Snapshot{ID: id, Status: subagent.StatusRunning}, true
}

func (f *winFake) RunningCount() int { return 0 }

func TestIssue3544_FinishedStepFreesWindowForDownstream(t *testing.T) {
	if workflowLayerCap < 2 {
		t.Fatalf("probe assumes cap >= 2, cap=%d", workflowLayerCap)
	}
	f := newWinFake()
	// Fill the whole window with independent slow steps, plus s0 -> s0b
	// chain: s0 finishes early, s0b becomes ready, and must launch while
	// the other cap-1 siblings are still running.
	wide := workflowLayerCap - 1 // s0 + these fill the window exactly
	spec := WorkflowSpec{
		Steps:     []WorkflowStep{{ID: "s0", Task: "t"}, {ID: "s0b", Task: "t", DependsOn: []string{"s0"}}},
		Synthesis: "fold",
	}
	for i := 0; i < wide; i++ {
		spec.Steps = append(spec.Steps, WorkflowStep{ID: fmt.Sprintf("w%d", i), Task: "t"})
	}
	// Guard the probe: the fake never completes the wide steps, so the run
	// can only end via cancellation. Cancel shortly after launch; before
	// cancelling, s0b must already have launched (it only becomes ready
	// after s0 completes, which happens while wide steps still run).
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			f.mu.Lock()
			_, launched := f.launchN["wf-s0b"]
			f.mu.Unlock()
			if launched {
				cancel()
				return
			}
			time.Sleep(time.Millisecond)
		}
		cancel()
	}()
	rep := RunWorkflow(ctx, f, f, spec, time.Millisecond)

	f.mu.Lock()
	defer f.mu.Unlock()
	order, launched := f.launchN["wf-s0b"]
	if !launched {
		t.Fatalf("downstream s0b never launched (starved below cap): partial=%v launches=%v", rep.Partial, f.names)
	}
	// s0b may only launch after its dependency s0.
	if s0, ok := f.launchN["wf-s0"]; !ok || order < s0 {
		t.Fatalf("s0b launched before dependency s0: %+v", f.launchN)
	}
}
