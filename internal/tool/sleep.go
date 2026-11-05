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
	return "Sleep a SHORT interval (15-60s), then re-check the result - never sleep the full expected duration in one call: external tasks often fail early, and a blind long sleep wastes the whole window. Hard cap 3 minutes per call. Commands expected to run over 2 minutes must use start_command (background) instead; for any wait expected to exceed 5 minutes, schedule a one-shot cron (cron_create with one_shot=true) that fires the follow-up check, and end the current turn - do not hold the session with long sleeps. Prefer wait_command when you have a job ID."
}
func (t SleepTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
	"type": "object",
	"properties": {
			"seconds": {
				"type": "integer",
				"description": "Seconds to sleep (combined with milliseconds). Hard cap 180s. Commands expected to take over 2 minutes: use start_command. Waits over 5 minutes: use a one-shot cron (one_shot=true) instead of sleeping."
			},
			"milliseconds": {
				"type": "integer",
				"description": "Additional milliseconds to sleep (combined with seconds). Total must not exceed 180s."
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

	if args.Seconds > 180 {
		return Result{IsError: true, Content: "sleep duration exceeds the 3-minute cap. Use start_command (background) for commands expected to run over 2 minutes; for waits over 5 minutes schedule a one-shot cron (cron_create one_shot=true) that fires the follow-up check instead of sleeping."}, nil
	}
	if args.Milliseconds > 180*1000 {
		return Result{IsError: true, Content: "sleep duration exceeds the 3-minute cap. For waits over 5 minutes schedule a one-shot cron (cron_create one_shot=true) instead of sleeping."}, nil
	}

	d := time.Duration(args.Seconds)*time.Second + time.Duration(args.Milliseconds)*time.Millisecond
	if d <= 0 {
		return Result{Content: "Slept for 0s"}, nil
	}
	if d > 3*time.Minute {
		return Result{IsError: true, Content: "sleep duration exceeds the 3-minute cap. For waits over 5 minutes schedule a one-shot cron (cron_create one_shot=true) instead of sleeping."}, nil
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
