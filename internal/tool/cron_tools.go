package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/cron"
)

// CronCreateTool creates a new scheduled job.
type CronCreateTool struct {
	Scheduler *cron.Scheduler
}

func (t CronCreateTool) Name() string { return "cron_create" }
func (t CronCreateTool) Description() string {
	return "Create a scheduled job that enqueues a prompt at specified intervals. " +
		"Use cron format (e.g. '*/5 * * * *' for every 5 minutes). " +
		"Set recurring=false for one-shot reminders."
}
func (t CronCreateTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"cron": {"type": "string", "description": "Cron expression (e.g. '*/5 * * * *' for every 5 minutes)"},
			"prompt": {"type": "string", "description": "Prompt to enqueue when the job fires"},
			"recurring": {"type": "boolean", "description": "Whether to repeat (default true)"},
			"durable": {"type": "boolean", "description": "Whether to persist across sessions (default false, V1 ignores this)"}
		},
		"required": ["cron", "prompt"]
	}`)
}
func (t CronCreateTool) Execute(_ context.Context, input json.RawMessage) (Result, error) {
	if t.Scheduler == nil {
		return Result{IsError: true, Content: "cron_create: scheduler not available"}, nil
	}
	var args struct {
		Cron      string `json:"cron"`
		Prompt    string `json:"prompt"`
		Recurring *bool  `json:"recurring"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid input: %v", err)}, nil
	}
	if args.Cron == "" {
		return Result{IsError: true, Content: "cron expression is required"}, nil
	}
	if args.Prompt == "" {
		return Result{IsError: true, Content: "prompt is required"}, nil
	}

	recurring := true
	if args.Recurring != nil {
		recurring = *args.Recurring
	}

	job, err := t.Scheduler.Create(args.Cron, args.Prompt, recurring)
	if err != nil {
		return Result{IsError: true, Content: err.Error()}, nil
	}
	out, _ := json.Marshal(job)
	return Result{Content: string(out) + "\n"}, nil
}

// CronDeleteTool deletes a scheduled job.
type CronDeleteTool struct {
	Scheduler *cron.Scheduler
}

func (t CronDeleteTool) Name() string { return "cron_delete" }
func (t CronDeleteTool) Description() string {
	return "Delete a scheduled job by ID."
}
func (t CronDeleteTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"jobId": {"type": "string", "description": "Job ID to delete"}
		},
		"required": ["jobId"]
	}`)
}
func (t CronDeleteTool) Execute(_ context.Context, input json.RawMessage) (Result, error) {
	if t.Scheduler == nil {
		return Result{IsError: true, Content: "cron_delete: scheduler not available"}, nil
	}
	var args struct {
		JobID string `json:"jobId"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid input: %v", err)}, nil
	}
	if !t.Scheduler.Delete(args.JobID) {
		return Result{IsError: true, Content: fmt.Sprintf("job %q not found", args.JobID)}, nil
	}
	return Result{Content: fmt.Sprintf("Job %s deleted\n", args.JobID)}, nil
}

// CronListTool lists all scheduled jobs.
type CronListTool struct {
	Scheduler *cron.Scheduler
}

func (t CronListTool) Name() string { return "cron_list" }
func (t CronListTool) Description() string {
	return "List all scheduled cron jobs."
}
func (t CronListTool) Parameters() json.RawMessage {
	return json.RawMessage(`{"type": "object", "properties": {}}`)
}
func (t CronListTool) Execute(_ context.Context, _ json.RawMessage) (Result, error) {
	if t.Scheduler == nil {
		return Result{IsError: true, Content: "cron_list: scheduler not available"}, nil
	}
	jobs := t.Scheduler.List()
	if len(jobs) == 0 {
		return Result{Content: "No scheduled jobs.\n"}, nil
	}

	sort.Slice(jobs, func(i, j int) bool { return jobs[i].ID < jobs[j].ID })

	var sb strings.Builder
	for _, job := range jobs {
		recurrent := "recurring"
		if !job.Recurring {
			recurrent = "one-shot"
		}
		fmt.Fprintf(&sb, "- %s [%s] %s next=%s\n",
			job.ID, recurrent, job.CronExpr,
			job.NextFire.Format(time.RFC3339))
	}
	return Result{Content: sb.String()}, nil
}
