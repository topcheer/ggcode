package agent

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/tool"
)

// adaptiveStubTool is a minimal tool.Tool for pre-exec outcome tests.
type adaptiveStubTool struct {
	name string
	resp tool.Result
	err  error
}

func (s *adaptiveStubTool) Name() string                { return s.name }
func (s *adaptiveStubTool) Description() string         { return "stub for adaptive width tests" }
func (s *adaptiveStubTool) Parameters() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (s *adaptiveStubTool) Execute(_ context.Context, _ json.RawMessage) (tool.Result, error) {
	return s.resp, s.err
}

// withAdaptiveEnv isolates the package-level signals and controller state
// for the duration of a test.
func withAdaptiveEnv(t *testing.T, mem func() float64, cpus func() int) {
	t.Helper()
	oldMem, oldCPU := memPressureRatio, cpuWidthCap
	memPressureRatio, cpuWidthCap = mem, cpus
	resetPreExecWidth()
	t.Cleanup(func() {
		memPressureRatio, cpuWidthCap = oldMem, oldCPU
		resetPreExecWidth()
	})
}

func TestAdaptiveWidthDefaults(t *testing.T) {
	withAdaptiveEnv(t, func() float64 { return 0 }, func() int { return 3 })
	if got := preExecWidthCtl.width(-1); got != parallelMaxConcurrent {
		t.Fatalf("clean environment: want width %d, got %d", parallelMaxConcurrent, got)
	}
}

func TestAdaptiveWidthContextFill(t *testing.T) {
	withAdaptiveEnv(t, func() float64 { return 0 }, func() int { return 3 })
	if got := preExecWidthCtl.width(0.70); got != 1 {
		t.Fatalf("context fill 0.70: want width 1, got %d", got)
	}
	if got := preExecWidthCtl.width(0.80); got != 0 {
		t.Fatalf("context fill 0.80: want skip (0), got %d", got)
	}
}

func TestAdaptiveWidthCPUCap(t *testing.T) {
	withAdaptiveEnv(t, func() float64 { return 0 }, func() int { return 1 })
	if got := preExecWidthCtl.width(-1); got != 1 {
		t.Fatalf("single-core cap: want width 1, got %d", got)
	}
}

func TestAdaptiveWidthMemoryPressure(t *testing.T) {
	withAdaptiveEnv(t, func() float64 { return 0.75 }, func() int { return 3 })
	if got := preExecWidthCtl.width(-1); got != 1 {
		t.Fatalf("heap 75%% of GOMEMLIMIT: want width 1, got %d", got)
	}
	withAdaptiveEnv(t, func() float64 { return 0.90 }, func() int { return 3 })
	if got := preExecWidthCtl.width(-1); got != 0 {
		t.Fatalf("heap 90%% of GOMEMLIMIT: want skip (0), got %d", got)
	}
}

func TestAdaptiveWidthFailureFeedback(t *testing.T) {
	withAdaptiveEnv(t, func() float64 { return 0 }, func() int { return 3 })

	// Five all-failing batches (0 ok, 2 failed) push the EWMA past the skip
	// threshold: 0.30, 0.51, 0.657, 0.76, 0.83.
	for i := 0; i < 5; i++ {
		preExecWidthCtl.recordBatch(0, 2)
	}
	if got := preExecWidthCtl.width(-1); got != 0 {
		t.Fatalf("sustained failures: want skip (0), got %d", got)
	}

	// Two clean batches recover the full width: 0.58, 0.41.
	preExecWidthCtl.recordBatch(2, 0)
	if got := preExecWidthCtl.width(-1); got != 1 {
		t.Fatalf("one clean batch: want narrowed width 1, got %d", got)
	}
	preExecWidthCtl.recordBatch(2, 0)
	if got := preExecWidthCtl.width(-1); got != parallelMaxConcurrent {
		t.Fatalf("two clean batches: want width %d, got %d", parallelMaxConcurrent, got)
	}
}

func TestAdaptiveWidthRecordIgnoresEmptyBatches(t *testing.T) {
	withAdaptiveEnv(t, func() float64 { return 0 }, func() int { return 3 })
	preExecWidthCtl.recordBatch(0, 0)
	if ema := preExecWidthCtl.ema(); ema != 0 {
		t.Fatalf("empty batch must not change EMA, got %v", ema)
	}
}

func TestPreExecOneOutcomeClassification(t *testing.T) {
	a := &Agent{tools: tool.NewRegistry()}
	if err := a.tools.Register(&adaptiveStubTool{name: "ok_tool", resp: tool.Result{Content: "fine"}}); err != nil {
		t.Fatalf("register ok tool: %v", err)
	}
	if err := a.tools.Register(&adaptiveStubTool{name: "bad_tool", err: errors.New("boom")}); err != nil {
		t.Fatalf("register bad tool: %v", err)
	}

	ctx := context.Background()
	if _, _, o := a.preExecOne(ctx, pending{name: "ok_tool"}); o != preExecOK {
		t.Errorf("successful exec: want preExecOK, got %d", o)
	}
	if _, _, o := a.preExecOne(ctx, pending{name: "bad_tool"}); o != preExecFailed {
		t.Errorf("failing exec: want preExecFailed, got %d", o)
	}
	if _, _, o := a.preExecOne(ctx, pending{name: "no_such_tool"}); o != preExecToolMissing {
		t.Errorf("missing tool: want preExecToolMissing, got %d", o)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, _, o := a.preExecOne(canceled, pending{name: "ok_tool"}); o != preExecCtxCanceled {
		t.Errorf("canceled ctx: want preExecCtxCanceled, got %d", o)
	}
}

func TestPreExecBatchFeedsFailureEWMA(t *testing.T) {
	withAdaptiveEnv(t, func() float64 { return 0 }, func() int { return 3 })

	a := &Agent{tools: tool.NewRegistry(), speculator: newSpeculator()}
	// "glob" is on the speculative safe list, so the batch is actually
	// pre-executed; the stub makes every attempt fail.
	if err := a.tools.Register(&adaptiveStubTool{name: "glob", err: errors.New("boom")}); err != nil {
		t.Fatalf("register: %v", err)
	}
	calls := []provider.ToolCallDelta{
		{ID: "1", Name: "glob", Arguments: []byte(`{"pattern":"*.go"}`)},
		{ID: "2", Name: "glob", Arguments: []byte(`{"pattern":"*.md"}`)},
	}
	if ema := preExecWidthCtl.ema(); ema != 0 {
		t.Fatalf("pre-run EMA should be 0, got %v", ema)
	}
	a.preExecuteReadOnlyTools(context.Background(), calls)
	if ema := preExecWidthCtl.ema(); ema <= 0 {
		t.Fatalf("failing batch must raise EMA, got %v", ema)
	}
}
