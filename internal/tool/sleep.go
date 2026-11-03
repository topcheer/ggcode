package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

type SleepTool struct{}

func (t SleepTool) Name() string { return "sleep" }
func (t SleepTool) Description() string {
	return "Sleep a SHORT interval (15-60s), then re-check the result - never sleep the full expected duration in one call: external tasks often fail early, and a blind long sleep wastes the whole window. Max 30 minutes per call. Prefer wait_command when you have a job ID."
}
func (t SleepTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
	"type": "object",
	"properties": {
			"seconds": {
				"type": "integer",
				"description": "Seconds to sleep (combined with milliseconds). Total duration must not exceed 30 minutes."
			},
			"milliseconds": {
				"type": "integer",
				"description": "Additional milliseconds to sleep (combined with seconds). Total duration must not exceed 30 minutes."
			},
		"description": {
			"type": "string",
			"description": "REQUIRED. Brief activity label shown in the UI, in the user's language."
		}
	},
	"required": [
		"seconds",
		"description"
	]
}`)
}
func (t SleepTool) Execute(ctx context.Context, input json.RawMessage) (Result, error) {
	var args struct {
		Seconds      int `json:"seconds"`
		Milliseconds int `json:"milliseconds"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid input: %v", err)}, nil
	}

	if args.Seconds < 0 {
		return Result{IsError: true, Content: "seconds must be non-negative"}, nil
	}
	if args.Milliseconds < 0 {
		return Result{IsError: true, Content: "milliseconds must be non-negative"}, nil
	}

	if args.Seconds > 1800 {
		return Result{IsError: true, Content: fmt.Sprintf("sleep duration %ds exceeds maximum of 30m", args.Seconds)}, nil
	}
	if args.Milliseconds > 1800*1000 {
		return Result{IsError: true, Content: fmt.Sprintf("sleep duration %dms exceeds maximum of 30m", args.Milliseconds)}, nil
	}

	d := time.Duration(args.Seconds)*time.Second + time.Duration(args.Milliseconds)*time.Millisecond
	if d <= 0 {
		return Result{Content: "Slept for 0s"}, nil
	}
	if d > 30*time.Minute {
		return Result{IsError: true, Content: fmt.Sprintf("sleep duration %s exceeds maximum of 30m", d)}, nil
	}

	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-timer.C:
		return Result{Content: fmt.Sprintf("Sleep for %s ... Done", d)}, nil
	case <-ctx.Done():
		return Result{Content: fmt.Sprintf("Sleep interrupted after context cancellation")}, ctx.Err()
	}
}
