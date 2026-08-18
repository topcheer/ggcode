package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// SleepTool pauses execution for a specified duration.
type SleepTool struct{}

func (t SleepTool) Name() string { return "sleep" }
func (t SleepTool) Description() string {
	return "Sleep for a specified duration (max 30 minutes). Prefer wait_command when you have a background job ID."
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
			"description": "REQUIRED. Brief activity label shown in the UI. Write in the user's language (e.g. 'Searching for TODO patterns', '检查构建配置'). You MUST always provide this field."
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
	// #658: clamp before the seconds→nanoseconds multiplication (same family
	// as the #513 fix in run_command). time.Duration(x)*time.Second has no
	// overflow guard — e.g. 9223372037s wraps negative, which would slip past
	// both the d<=0 fast path (reporting a bogus "Slept for 0s" success) and
	// the 30-minute cap below. Values above the cap are rejected explicitly.
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
