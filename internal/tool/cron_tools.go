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
	return "Create a scheduled job that enqueues a prompt at cron-specified intervals. " +
		"Set recurring=false for one-shot reminders (in-memory only, not persisted). " +
		"queue_if_busy=false (default) skips if agent is busy; true queues the prompt for later."
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
		"queue_if_busy": {
			"type": "boolean",
			"description": "Whether to queue the prompt if the agent is busy when the job fires. Default: false (skip if busy). Set to true for important tasks that must run even if the agent is busy — the prompt will queue and execute after the current task finishes."
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
		Cron        string `json:"cron"`
		Prompt      string `json:"prompt"`
		Recurring   *bool  `json:"recurring"`
		QueueIfBusy *bool  `json:"queue_if_busy"`
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

	queueIfBusy := false
	if args.QueueIfBusy != nil {
		queueIfBusy = *args.QueueIfBusy
	}

	job, err := t.Scheduler.Create(args.Cron, args.Prompt, recurring, queueIfBusy)
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
	if args.JobID == "" {
		return Result{IsError: true, Content: "jobId is required"}, nil
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
		kind := "recurring"
		if !job.Recurring {
			kind = "one-shot"
		}
		queueMode := "skip-if-busy"
		if job.QueueIfBusy {
			queueMode = "queue-if-busy"
		}
		state := ""
		if job.Paused {
			state = "paused, "
		}
		fmt.Fprintf(&sb, "- %s [%s%s, %s] %s next=%s\n",
			job.ID, state, kind, queueMode, job.CronExpr,
			job.NextFire.Format(time.RFC3339))
	}
	return Result{Content: sb.String()}, nil
}

// CronUpdateTool updates an existing scheduled job's cron expression, prompt,
// and/or queue_if_busy without changing its ID.
type CronUpdateTool struct {
	Scheduler *cron.Scheduler
}

func (t CronUpdateTool) Name() string { return "cron_update" }
func (t CronUpdateTool) Description() string {
	return "Update an existing scheduled cron job by ID. Only provided fields change; timer auto-reschedules on cron expression change. Job ID stays the same."
}
func (t CronUpdateTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"jobId": {
				"type": "string",
				"description": "Job ID to update (e.g. 'cron-3')"
			},
			"cron": {
				"type": "string",
				"description": "New cron expression (optional, omit to keep current)"
			},
			"prompt": {
				"type": "string",
				"description": "New prompt text (optional, omit to keep current)"
			},
			"queue_if_busy": {
				"type": "boolean",
				"description": "New queue-if-busy setting (optional, omit to keep current)"
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
func (t CronUpdateTool) Execute(_ context.Context, input json.RawMessage) (Result, error) {
	if t.Scheduler == nil {
		return Result{IsError: true, Content: "cron_update: scheduler not available"}, nil
	}
	var args struct {
		JobID       string  `json:"jobId"`
		Cron        *string `json:"cron"`
		Prompt      *string `json:"prompt"`
		QueueIfBusy *bool   `json:"queue_if_busy"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid input: %v", err)}, nil
	}
	if args.JobID == "" {
		return Result{IsError: true, Content: "jobId is required"}, nil
	}
	if args.Cron == nil && args.Prompt == nil && args.QueueIfBusy == nil {
		return Result{IsError: true, Content: "at least one of cron, prompt, or queue_if_busy must be provided to update"}, nil
	}
	if args.Prompt != nil && strings.TrimSpace(*args.Prompt) == "" {
		return Result{IsError: true, Content: "prompt cannot be empty"}, nil
	}

	job, err := t.Scheduler.Update(args.JobID, args.Cron, args.Prompt, args.QueueIfBusy)
	if err != nil {
		return Result{IsError: true, Content: err.Error()}, nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("✓ Job %s updated\n", job.ID))
	sb.WriteString(fmt.Sprintf("  Schedule: %s\n", job.CronExpr))
	sb.WriteString(fmt.Sprintf("  Recurring: %v\n", job.Recurring))
	sb.WriteString(fmt.Sprintf("  Queue if busy: %v\n", job.QueueIfBusy))
	if job.Paused {
		sb.WriteString("  Status: paused\n")
	} else {
		sb.WriteString("  Status: active\n")
	}
	if !job.NextFire.IsZero() {
		sb.WriteString(fmt.Sprintf("  Next fire: %s\n", job.NextFire.Format("2006-01-02 15:04 MST")))
	}
	promptPreview := job.Prompt
	if len([]rune(promptPreview)) > 80 {
		promptPreview = string([]rune(promptPreview)[:80]) + "..."
	}
	for _, line := range strings.Split(promptPreview, "\n") {
		if strings.TrimSpace(line) != "" {
			sb.WriteString(fmt.Sprintf("  Prompt: %s\n", strings.TrimSpace(line)))
			break
		}
	}
	return Result{Content: sb.String()}, nil
}

// CronPauseTool pauses a scheduled job without deleting it.
type CronPauseTool struct {
	Scheduler *cron.Scheduler
}

func (t CronPauseTool) Name() string { return "cron_pause" }
func (t CronPauseTool) Description() string {
	return "Pause a scheduled cron job temporarily. Timer stops but configuration is preserved. Use cron_resume to reactivate."
}
func (t CronPauseTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"jobId": {
				"type": "string",
				"description": "Job ID to pause"
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
func (t CronPauseTool) Execute(_ context.Context, input json.RawMessage) (Result, error) {
	if t.Scheduler == nil {
		return Result{IsError: true, Content: "cron_pause: scheduler not available"}, nil
	}
	var args struct {
		JobID string `json:"jobId"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid input: %v", err)}, nil
	}
	if args.JobID == "" {
		return Result{IsError: true, Content: "jobId is required"}, nil
	}
	if err := t.Scheduler.Pause(args.JobID); err != nil {
		return Result{IsError: true, Content: err.Error()}, nil
	}
	return Result{Content: fmt.Sprintf("Job %s paused. Use cron_resume to reactivate.\n", args.JobID)}, nil
}

// CronResumeTool reactivates a paused scheduled job.
type CronResumeTool struct {
	Scheduler *cron.Scheduler
}

func (t CronResumeTool) Name() string { return "cron_resume" }
func (t CronResumeTool) Description() string {
	return "Resume a paused cron job. The job's NextFire time is recomputed from the current time and a new timer is scheduled. " +
		"If the job is already active, this is a no-op."
}
func (t CronResumeTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"jobId": {
				"type": "string",
				"description": "Job ID to resume"
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
func (t CronResumeTool) Execute(_ context.Context, input json.RawMessage) (Result, error) {
	if t.Scheduler == nil {
		return Result{IsError: true, Content: "cron_resume: scheduler not available"}, nil
	}
	var args struct {
		JobID string `json:"jobId"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid input: %v", err)}, nil
	}
	if args.JobID == "" {
		return Result{IsError: true, Content: "jobId is required"}, nil
	}
	if err := t.Scheduler.Resume(args.JobID); err != nil {
		return Result{IsError: true, Content: err.Error()}, nil
	}
	return Result{Content: fmt.Sprintf("Job %s resumed.\n", args.JobID)}, nil
}

// CronGetTool retrieves full details of a single scheduled job.
type CronGetTool struct {
	Scheduler *cron.Scheduler
}

func (t CronGetTool) Name() string { return "cron_get" }
func (t CronGetTool) Description() string {
	return "Get full details of a single scheduled cron job by ID, including the complete prompt text. " +
		"Use this when cron_list's summary format doesn't show enough detail."
}
func (t CronGetTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"jobId": {
				"type": "string",
				"description": "Job ID to inspect"
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
func (t CronGetTool) Execute(_ context.Context, input json.RawMessage) (Result, error) {
	if t.Scheduler == nil {
		return Result{IsError: true, Content: "cron_get: scheduler not available"}, nil
	}
	var args struct {
		JobID string `json:"jobId"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid input: %v", err)}, nil
	}
	if args.JobID == "" {
		return Result{IsError: true, Content: "jobId is required"}, nil
	}

	job, ok := t.Scheduler.Get(args.JobID)
	if !ok {
		return Result{IsError: true, Content: fmt.Sprintf("job %q not found", args.JobID)}, nil
	}

	kind := "recurring"
	if !job.Recurring {
		kind = "one-shot"
	}
	queueMode := "skip-if-busy"
	if job.QueueIfBusy {
		queueMode = "queue-if-busy"
	}
	state := "active"
	if job.Paused {
		state = "paused"
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "ID:          %s\n", job.ID)
	fmt.Fprintf(&sb, "State:       %s\n", state)
	fmt.Fprintf(&sb, "Type:        %s\n", kind)
	fmt.Fprintf(&sb, "Queue mode:  %s\n", queueMode)
	fmt.Fprintf(&sb, "Cron expr:   %s\n", job.CronExpr)
	fmt.Fprintf(&sb, "Next fire:   %s\n", job.NextFire.Format(time.RFC3339))
	fmt.Fprintf(&sb, "Created at:  %s\n", job.CreatedAt.Format(time.RFC3339))
	fmt.Fprintf(&sb, "Prompt:\n%s\n", job.Prompt)
	return Result{Content: sb.String()}, nil
}
