package agent

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/tool"
)

// Battery for issue #5843: post-approval execution shape. These tests use
// only pre-existing symbols so they compile at the pre-fix parent and pin
// the behavioral contract: (1) input queued while an approval gate is open
// is drained at the approval boundary, and (2) a retraction queued during
// the approval wait cancels the approved execution instead of running a
// long task the user just withdrew.

type probe5843Tool struct {
	mu       sync.Mutex
	executed int
	sleep    time.Duration
}

func (t *probe5843Tool) Name() string                { return "run_probe" }
func (t *probe5843Tool) Description() string         { return "probe tool for issue 5843 battery" }
func (t *probe5843Tool) Parameters() json.RawMessage { return json.RawMessage("{}") }
func (t *probe5843Tool) Execute(ctx context.Context, _ json.RawMessage) (tool.Result, error) {
	t.mu.Lock()
	t.executed++
	t.mu.Unlock()
	if t.sleep > 0 {
		select {
		case <-time.After(t.sleep):
		case <-ctx.Done():
		}
	}
	return tool.Result{Content: "probe ok"}, nil
}

func (t *probe5843Tool) count() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.executed
}

func new5843GateAgent(onApproval ApprovalFunc, onInterrupt interruptionHandler, probe *probe5843Tool) *Agent {
	reg := tool.NewRegistry()
	if probe != nil {
		reg.Register(probe)
	}
	a := NewAgent(&mockProvider{}, reg, "", 1)
	a.policy = &stubAskPolicy{mode: permission.AutoMode}
	a.onApproval = onApproval
	a.onInterrupt = onInterrupt
	a.askThrottle = permission.NewAskThrottle()
	a.approvalMemory = permission.NewApprovalMemory()
	return a
}

var tc5843 = provider.ToolCallDelta{Name: "run_probe", Arguments: json.RawMessage("{}")}

func allowAll5843(context.Context, string, string) permission.Decision {
	return permission.Allow
}

func TestIssue5843_ApprovalBoundaryDrainsQueuedInput(t *testing.T) {
	probe := &probe5843Tool{}
	var mu sync.Mutex
	drained := 0
	onInterrupt := func() []provider.ContentBlock {
		mu.Lock()
		defer mu.Unlock()
		drained++
		if drained == 1 {
			return []provider.ContentBlock{{Type: "text", Text: "also run the linter on the generated files"}}
		}
		return nil
	}
	a := new5843GateAgent(allowAll5843, onInterrupt, probe)

	res := a.executeToolWithPermission(context.Background(), tc5843)
	if res.IsError {
		t.Fatalf("approved probe must execute cleanly, got error: %s", res.Content)
	}
	if probe.count() != 1 {
		t.Fatalf("approved probe must execute exactly once, got %d", probe.count())
	}
	found := false
	for _, msg := range a.contextManager.Messages() {
		if msg.Role != "user" {
			continue
		}
		for _, b := range msg.Content {
			if b.Type == "text" && strings.Contains(b.Text, "also run the linter on the generated files") {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("guidance queued during the approval wait must be drained at the approval boundary and land in the context for the next turn")
	}
}

func TestIssue5843_RetractionDuringApprovalCancelsExecution(t *testing.T) {
	probe := &probe5843Tool{}
	onInterrupt := func() []provider.ContentBlock {
		return []provider.ContentBlock{{Type: "text", Text: "stop"}}
	}
	a := new5843GateAgent(allowAll5843, onInterrupt, probe)

	res := a.executeToolWithPermission(context.Background(), tc5843)
	if probe.count() != 0 {
		t.Fatalf("retraction queued during the approval wait must cancel execution, tool ran %d time(s)", probe.count())
	}
	if !res.IsError {
		t.Fatal("retraction must yield an error result so the model sees the cancellation")
	}
	if !strings.Contains(strings.ToLower(res.Content), "retract") {
		t.Fatalf("result should state the retraction, got: %s", res.Content)
	}
}
