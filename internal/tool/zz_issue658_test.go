package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"
)

func TestSleepOverflowsRejected(t *testing.T) {
	tool := SleepTool{}
	for _, sec := range []int{1801, 3600, 9223372037, 18446744074} {
		input, _ := json.Marshal(map[string]any{"seconds": sec, "description": "t"})
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		res, err := tool.Execute(ctx, input)
		cancel()
		if err != nil {
			t.Fatalf("seconds=%d: err %v", sec, err)
		}
		if !res.IsError {
			t.Fatalf("seconds=%d: expected error result, got success %q", sec, res.Content)
		}
	}
}

func TestSleepZeroStillSucceeds(t *testing.T) {
	tool := SleepTool{}
	input, _ := json.Marshal(map[string]any{"seconds": 0, "description": "t"})
	res, err := tool.Execute(context.Background(), input)
	if err != nil || res.IsError {
		t.Fatalf("zero sleep failed: res=%+v err=%v", res, err)
	}
}

func TestSleepBoundaryAccepted(t *testing.T) {
	tool := SleepTool{}
	input, _ := json.Marshal(map[string]any{"seconds": 1800, "description": "t"})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := tool.Execute(ctx, input)
	if err == nil {
		t.Fatalf("expected ctx cancellation error")
	}
}

var _ = fmt.Sprintf
