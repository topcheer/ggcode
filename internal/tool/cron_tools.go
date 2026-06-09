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
		"Set recurring=false for one-shot reminders. Only recurring jobs are persisted across restarts; one-shot reminders are in-memory and will be lost if the process exits before they fire."
}
func (t CronCreateTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
	"type": "object",
	"properties": {
		"cron": {
			"type": "string",
			"description": "Cron expression (e.g. '*/5 * * * *' for every 5 minutes)"
		},
		"prompt": {
			"type": "string",
			"description": "Prompt to enqueue when the job fires"
		},
		"recurring": {
			"type": "boolean",
			"description": "Whether to repeat (default true). Only recurring jobs are persisted across restarts; one-shot reminders (recurring=false) are in-memory only."
		},
		"durable": {
			"type": "boolean",
			"description": "Whether to persist across sessions (default false, V1 ignores this). Persistence is controlled by recurring=true; recurring=false jobs are never persisted."
		},
		"description": {
			"type": "string",
			"description": "REQUIRED. Brief activity label shown in the UI. Write in the user's language (e.g. 'Searching for TODO patterns', '检查构建配置'). You MUST always provide this field."
		}
	},
	"required": [
		"cron",
		"prompt",
		"description"
	]
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
	return "Delete a scheduled cron job by ID. This removes the future schedule only; it does not cancel prompts that have already been enqueued or undo work already started."
}
func (t CronDeleteTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
	"type": "object",
	"properties": {
		"jobId": {
			"type": "string",
			"description": "Job ID to delete"
		},
		"description": {
			"type": "string",
			"description": "REQUIRED. Brief activity label shown in the UI. Write in the user's language (e.g. 'Searching for TODO patterns', '检查构建配置'). You MUST always provide this field."
		}
	},
	"required": [
		"jobId",
		"description"
	]
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
	deleted, err := t.Scheduler.DeleteWithError(args.JobID)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("delete job %q: %v", args.JobID, err)}, nil
	}
	if !deleted {
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
	return "List currently loaded scheduled cron jobs for this workspace. Shows in-memory active jobs, including recurring and one-shot jobs that have not fired yet."
}
func (t CronListTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
	"type": "object",
	"properties": {
		"description": {
			"type": "string",
			"description": "REQUIRED. Brief activity label shown in the UI. Write in the user's language (e.g. 'Searching for TODO patterns', '检查构建配置'). You MUST always provide this field."
		}
	},
	"required": [
		"description"
	]
}`)
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
