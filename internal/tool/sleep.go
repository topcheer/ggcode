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
	return "Sleep for a specified duration. Use this when waiting for a specified time to pass, " +
		"such as when checking back in an hour, or waiting for a process to finish. " +
		"Prefer this tool over using run_command to sleep."
}
func (t SleepTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"seconds": {"type": "integer", "description": "Seconds to sleep"},
			"milliseconds": {"type": "integer", "description": "Additional milliseconds to sleep (combined with seconds)"}
		},
		"required": ["seconds"]
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
