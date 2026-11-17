package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/cron"
)

type CronCreateTool struct {
	Scheduler *cron.Scheduler
}

func (t CronCreateTool) Name() string { return "cron_create" }
func (t CronCreateTool) Description() string {
	return "Create a scheduled job that enqueues a prompt at cron-specified intervals. " +
		"Set recurring=false for one-shot reminders (in-memory only, not persisted). " +
		"One-shot jobs whose computed next fire is more than 90 days away are rejected unless far=true - " +
		"far-future one-shot schedules are almost always a cron field parsing slip. " +
		"A time-only one-shot whose target time has already passed today rolls the fire to tomorrow (+~24h) and is rejected unless tomorrow=true; " +
		"every one-shot result echoes the computed next fire with its delta from now so schedule mismatches are visible at build time. " +
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
		"one_shot": {
			"type": "boolean",
			"description": "Create a self-deleting one-shot reminder: fires once at the next cron match, then removes itself. Preferred over recurring=false for deferred follow-ups. Guarded: the computed next fire must be within 90 days unless far=true, and must not be a silent next-day rollover (target time already passed today) unless tomorrow=true. The result echoes the computed next fire and its delta from now."
		},
		"far": {
			"type": "boolean",
			"description": "Explicitly accept a far-future one-shot: required when the computed next fire is more than 90 days away and that is intentional. The result output carries a loud far-future warning when set."
		},
		"tomorrow": {
			"type": "boolean",
			"description": "Explicitly accept the next-day rollover: required when a time-only one-shot's target time has already passed today, so the computed next fire is tomorrow (+~24h). The result output carries a loud rollover warning when set."
		},
		"queue_if_busy": {
			"type": "boolean",
			"description": "Whether to queue the prompt if the agent is busy when the job fires. Default: false (skip if busy). Set to true for important tasks that must run even if the agent is busy - the prompt will queue and execute after the current task finishes."
		},
		"durable": {
			"type": "boolean",
			"description": "Whether to persist across sessions (default false, V1 ignores this). Persistence is controlled by recurring=true; recurring=false jobs are never persisted."
		},
		"description": {
			"type": "string",
			"description": "REQUIRED. Brief activity label shown in the UI, in the user's language."
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
		OneShot     *bool  `json:"one_shot"`
		Far         *bool  `json:"far"`
		Tomorrow    *bool  `json:"tomorrow"`
		QueueIfBusy *bool  `json:"queue_if_busy"`
		FarAck      *bool  `json:"far_horizon_ack"`
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
	// one_shot is the canonical route for deferred follow-ups: the job fires
	// once and removes itself. Preferred over recurring=false for any wait
	// expected to exceed 5 minutes (sleep-discipline routing).
	if args.OneShot != nil && *args.OneShot {
		recurring = false
	}

	queueIfBusy := false
	if args.QueueIfBusy != nil {
		queueIfBusy = *args.QueueIfBusy
	}

	// #5353 far-future one-shot trap: two seats (OBS-041, OBS-049) deleted and
	// recreated one-shot crons whose NextFire landed >1 year out after an
	// HH:MM-vs-day parsing slip, because creation silently accepted the
	// far-future schedule. Reject one-shot creation beyond the 90-day horizon
	// unless explicitly acknowledged with far=true.
	// Architecture note (#5356): the tool-layer guard above is the primary
	// gate; an acknowledged far=true routes through
	// Scheduler.CreateAcknowledgedFar so the scheduler-layer horizon guard
	// (checkOneShotHorizon, the programmatic-caller backstop) also honors the
	// acknowledgement instead of re-rejecting.
	// #5359: "far" is the canonical ack param; a legacy "far_horizon_ack"
	// payload (pre-rebase schema) is still honored so old-contract calls
	// never silently lose the acknowledgement.
	far := (args.Far != nil && *args.Far) || (args.FarAck != nil && *args.FarAck)
	farFutureDays := -1
	// #5743 same-day overshoot state: a time-only one-shot scheduled for
	// "later today" whose target time has already passed resolves its next
	// match to tomorrow (+~24h) - under the 90-day far horizon, so no guard
	// fired and the create result never surfaced the delta (techwriter
	// R4-5: two evening builds silently deferred a full day, withdrawn and
	// rebuilt twice). Like far=, the next-day rollover now requires an
	// explicit "tomorrow": true acknowledgement; the accepted create
	// carries a loud warning instead of staying silent.
	rolloverAck := false
	rolloverTarget := time.Time{}
	if !recurring {
		// #5594: a stale-date one-shot (system prompt embeds yesterday's
		// date) rolls forward ~1 year - cron.NextTime finds the next
		// matching calendar occurrence, so the computed next fire lands
		// ~364 days out and far=true becomes an escape hatch for a
		// silently mis-scheduled job. A specified date/time already in the
		// past (including earlier today) is not a horizon question:
		// reject unconditionally BEFORE the far gate - far=true must not
		// bypass a past-date rejection (#5594 DoD 2).
		if msg, past := oneShotPastDateRejection(args.Cron, time.Now()); past {
			return Result{IsError: true, Content: msg}, nil
		}
		// #5743: reject the unacknowledged same-day overshoot BEFORE the
		// far gate (a +24h rollover is never a far-horizon question, and
		// far=true must not double as its acknowledgement - same
		// precedence rule as the #5594 past-date rejection).
		if target, next, rolled := oneShotDayRollover(args.Cron, time.Now()); rolled {
			if args.Tomorrow == nil || !*args.Tomorrow {
				return Result{IsError: true, Content: oneShotDayRolloverRejection(target, next, time.Now())}, nil
			}
			rolloverAck = true
			rolloverTarget = target
		}
		if next, err := cron.NextTime(args.Cron, time.Now()); err == nil {
			if days := int(time.Until(next).Hours() / 24); days > oneShotFarFutureDays {
				if !far {
					return Result{IsError: true, Content: oneShotFarFutureRejection(next, days)}, nil
				}
				farFutureDays = days
			}
		}
	}

	var job cron.Job
	var err error
	if far {
		job, err = t.Scheduler.CreateAcknowledgedFar(args.Cron, args.Prompt, recurring, queueIfBusy, true)
	} else {
		job, err = t.Scheduler.Create(args.Cron, args.Prompt, recurring, queueIfBusy)
	}
	if err != nil {
		return Result{IsError: true, Content: err.Error()}, nil
	}
	// #5748 leading-JSON contract: the human-facing one-shot notes ride as a
	// "note" field inside the job JSON instead of a raw text suffix - the
	// suffix-append shape broke 4 whole-content Unmarshal callers in the
	// #5745 cycle before the #5746 rework.
	note := ""
	if !recurring {
		note = "(one-shot: fires once and is removed automatically; not persisted across restarts)"
		// #5743 ask 1: echo the computed next fire (local time plus delta
		// vs now) on every one-shot create so an intent mismatch is
		// visible at build time instead of surfacing a day late as a
		// silently missed fire.
		if !job.NextFire.IsZero() {
			note += fmt.Sprintf("\nnext fire: %s (%s from now)", job.NextFire.Format("2006-01-02 15:04"), humanDelta(time.Until(job.NextFire)))
		}
		if rolloverAck {
			note += fmt.Sprintf("\nWARNING: same-day overshoot accepted via tomorrow=true - target time %s already passed today, so the fire rolls to tomorrow %s. Verify the next-day fire is intended before relying on it.", rolloverTarget.Format("15:04"), job.NextFire.Format("2006-01-02 15:04"))
		}
		if farFutureDays > 0 {
			note += fmt.Sprintf("\nWARNING: far-future one-shot accepted via far=true - next fire is %d days away. Far-future one-shots are usually cron field slips (minute hour day-of-month month day-of-week); verify the expression before relying on it.", farFutureDays)
		}
	}
	content, err := jsonContentWithNote(job, note)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("cron_create: %v", err)}, nil
	}
	return Result{Content: content}, nil
}

// #5743 oneShotDayRollover detects the same-day overshoot: a time-only
// one-shot expression (explicit minute and hour fields; day-of-month,
// month and day-of-week all unrestricted) whose candidate times today
// have all already passed, so cron.NextTime resolves the fire to
// tomorrow's earliest candidate (+~24h, "later today" silently becoming
// "tomorrow"). It returns today's latest (already passed) target time and
// the computed next fire. The current minute counts as passed, matching
// NextTime's search start (now truncated to the minute, plus one minute).
// Dated forms (#5594 scope: explicit day+month) and weekday-restricted
// forms are out of scope: their rollovers are not the deterministic +24h
// same-day class. Stepped or wildcard time fields are also out of scope:
// they always leave a near match, so no day-long deferral is possible.
func oneShotDayRollover(expr string, now time.Time) (targetToday, next time.Time, rolled bool) {
	fields := strings.Fields(expr)
	if len(fields) != 5 {
		return time.Time{}, time.Time{}, false
	}
	for _, f := range []string{fields[2], fields[3], fields[4]} {
		if f != "*" && f != "?" {
			return time.Time{}, time.Time{}, false
		}
	}
	minutes, minOK := parseSimpleField(fields[0])
	hours, hourOK := parseSimpleField(fields[1])
	if !minOK || !hourOK || len(minutes) == 0 || len(hours) == 0 {
		return time.Time{}, time.Time{}, false
	}
	minH, maxH := fieldBounds(hours)
	minMi, maxM := fieldBounds(minutes)
	latestToday := time.Date(now.Year(), now.Month(), now.Day(), maxH, maxM, 0, 0, now.Location())
	if latestToday.After(now.Truncate(time.Minute)) {
		return time.Time{}, time.Time{}, false // a slot is still reachable today
	}
	tomorrow := now.AddDate(0, 0, 1)
	next = time.Date(tomorrow.Year(), tomorrow.Month(), tomorrow.Day(), minH, minMi, 0, 0, now.Location())
	return latestToday, next, true
}

// fieldBounds returns the min and max value of an explicit cron field set
// (parseSimpleField output). The set is never empty on the ok path.
func fieldBounds(values map[int]bool) (int, int) {
	min, max := -1, -1
	for v := range values {
		if min < 0 || v < min {
			min = v
		}
		if v > max {
			max = v
		}
	}
	return min, max
}

func oneShotDayRolloverRejection(target, next, now time.Time) string {
	return fmt.Sprintf(
		"one-shot cron rejected: the target time %s has already passed today (now %s), so the next fire would silently roll to tomorrow %s (%s from now). "+
			"Same-day overshoots are how \"later today\" reminders turn into day-long deferrals with zero warning. "+
			"If the next-day fire is intentional, retry with \"tomorrow\": true; otherwise recompute the expression for a time still ahead today (or spell out the full date). "+
			"far=true does not acknowledge a same-day overshoot.",
		target.Format("15:04"), now.Format("15:04"), next.Format("2006-01-02 15:04"), humanDelta(next.Sub(now)),
	)
}

// humanDelta renders a duration as a compact +HhMMm / +MMm string for
// result echoes (#5743), rounded to the minute.
func humanDelta(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	d = d.Round(time.Minute)
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	switch {
	case h == 0:
		return fmt.Sprintf("+%dm", m)
	case m == 0:
		return fmt.Sprintf("+%dh", h)
	default:
		return fmt.Sprintf("+%dh%02dm", h, m)
	}
}

const oneShotFarFutureDays = 90

func oneShotFarFutureRejection(next time.Time, days int) string {
	return fmt.Sprintf(
		"one-shot cron rejected: computed next fire is %d days away (%s), beyond the %d-day horizon. "+
			"Far-future one-shot schedules are almost always a cron parsing slip (e.g. HH:MM-vs-day field confusion lands the fire in the next calendar year). "+
			"Re-check the 5-field expression (minute hour day-of-month month day-of-week). If the far-future fire is intentional, retry with \"far\": true.",
		days, next.Format(time.RFC3339), oneShotFarFutureDays,
	)
}

// #5594 oneShotPastDateRejection detects a one-shot whose explicitly
// specified date/time has already passed (including "earlier today"). The
// cron package keeps its parsed field sets unexported, so simple field
// forms are parsed locally. The check only fires when BOTH day-of-month and
// month are explicit: a stale day-of-month alone rolls at most ~31 days,
// which the regular 90-day horizon guard already scopes. If any explicit
// candidate moment is still ahead of now (including later today), the
// schedule has a legitimate upcoming fire and the check stays silent.
func oneShotPastDateRejection(expr string, now time.Time) (string, bool) {
	fields := strings.Fields(expr)
	if len(fields) != 5 {
		return "", false
	}
	minutes, minOK := parseSimpleField(fields[0])
	hours, hourOK := parseSimpleField(fields[1])
	days, dayOK := parseSimpleField(fields[2])
	months, monthOK := parseSimpleField(fields[3])
	if !minOK || !hourOK || !dayOK || !monthOK ||
		len(days) == 0 || len(months) == 0 || len(hours) == 0 || len(minutes) == 0 {
		return "", false
	}
	var latest time.Time
	found := false
	for m := range months {
		for d := range days {
			if d < 1 || d > daysInMonth(now.Year(), m) {
				continue // impossible date (e.g. Feb 30) - not evidence of intent
			}
			for h := range hours {
				for mi := range minutes {
					c := time.Date(now.Year(), time.Month(m), d, h, mi, 0, 0, now.Location())
					if !found || c.After(latest) {
						latest = c
						found = true
					}
				}
			}
		}
	}
	if !found || latest.After(now) {
		return "", false
	}
	daysAgo := int(now.Sub(latest).Hours() / 24)
	when := "earlier today"
	if daysAgo > 0 {
		when = fmt.Sprintf("%d days ago", daysAgo)
	}
	return fmt.Sprintf(
		"one-shot cron rejected: the specified date/time is in the past (%s: %s). "+
			"A stale date would silently roll the fire ~1 year forward. "+
			"Recompute the schedule from the current date (explicit day+month+HH:MM) or use a recurring expression instead. "+
			"far=true does not bypass a past-date rejection.",
		when, latest.Format("2006-01-02 15:04"),
	), true
}

// parseSimpleField parses simple cron field forms (lists, ranges, optional
// step) into a value set. It returns explicit=false for wildcards ("*",
// "*/n", "?") or unrecognized forms so the caller falls back to the regular
// horizon guard instead of guessing intent.
func parseSimpleField(field string) (map[int]bool, bool) {
	if field == "" || strings.ContainsAny(field, "*?") {
		return nil, false
	}
	values := make(map[int]bool)
	for _, part := range strings.Split(field, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			return nil, false
		}
		if i := strings.Index(part, "/"); i >= 0 {
			part = part[:i] // range with step: keep the range bounds
		}
		loHi := strings.SplitN(part, "-", 2)
		lo, err := strconv.Atoi(strings.TrimSpace(loHi[0]))
		if err != nil {
			return nil, false
		}
		hi := lo
		if len(loHi) == 2 {
			if hi, err = strconv.Atoi(strings.TrimSpace(loHi[1])); err != nil {
				return nil, false
			}
		}
		for v := lo; v <= hi; v++ {
			values[v] = true
		}
	}
	return values, true
}

func daysInMonth(year int, month int) int {
	return time.Date(year, time.Month(month)+1, 0, 0, 0, 0, 0, time.UTC).Day()
}

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
			"description": "REQUIRED. Brief activity label shown in the UI, in the user's language."
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
		// Idempotent delete (#5673): the job is already gone -- most commonly a
		// one-shot job that fired and removed itself. Tolerate instead of
		// erroring so management scripts re-cleaning expired jobs don't fail.
		return Result{Content: fmt.Sprintf("Job %s already gone (nothing to delete; a one-shot job that already fired self-deletes). No action needed.\n", args.JobID)}, nil
	}
	return Result{Content: fmt.Sprintf("Job %s deleted\n", args.JobID)}, nil
}

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
			"description": "REQUIRED. Brief activity label shown in the UI, in the user's language."
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

		next := job.NextFire.Format(time.RFC3339)
		if job.NextFire.IsZero() {
			next = "none (paused)"
		}
		fmt.Fprintf(&sb, "- %s [%s%s, %s] %s next=%s\n",
			job.ID, state, kind, queueMode, job.CronExpr,
			next)
	}
	return Result{Content: sb.String()}, nil
}

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
		"far": {
			"type": "boolean",
			"description": "Explicitly accept a far-future schedule: required when the new cron expression moves a one-shot job's next fire more than 90 days out and that is intentional."
		},
		"tomorrow": {
			"type": "boolean",
			"description": "Explicitly accept the next-day rollover: required when the new cron expression is time-only and its target time has already passed today, moving a one-shot job's next fire to tomorrow (+~24h)."
		},
			"description": {
				"type": "string",
				"description": "REQUIRED. Brief activity label shown in the UI, in the user's language."
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
		Far         *bool   `json:"far"`
		Tomorrow    *bool   `json:"tomorrow"`
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

	// #5353: the far-future trap applies on update too - a seat re-specifying
	// the schedule of an existing one-shot can introduce the same >1 year
	// NextFire slip. Guard only one-shot targets; recurring jobs legitimately
	// have distant first fires only when the expression is legitimately rare.
	// #5743: the same-day overshoot guard applies on reschedule too -
	// re-pointing an existing one-shot at a time-only expression whose
	// target time has passed silently defers the fire a full day. Like the
	// create path, it needs "tomorrow": true and far=true does not
	// acknowledge it.
	rolloverAck := false
	if args.Cron != nil && strings.TrimSpace(*args.Cron) != "" {
		if job, ok := t.Scheduler.Get(args.JobID); ok && !job.Recurring {
			if target, next, rolled := oneShotDayRollover(*args.Cron, time.Now()); rolled {
				if args.Tomorrow == nil || !*args.Tomorrow {
					return Result{IsError: true, Content: oneShotDayRolloverRejection(target, next, time.Now())}, nil
				}
				rolloverAck = true
			}
			if args.Far == nil || !*args.Far {
				if next, err := cron.NextTime(*args.Cron, time.Now()); err == nil {
					if days := int(time.Until(next).Hours() / 24); days > oneShotFarFutureDays {
						return Result{IsError: true, Content: oneShotFarFutureRejection(next, days)}, nil
					}
				}
			}
		}
	}

	job, err := t.Scheduler.Update(args.JobID, args.Cron, args.Prompt, args.QueueIfBusy)
	if err != nil {
		return Result{IsError: true, Content: err.Error()}, nil
	}

	farAccepted := args.Far != nil && *args.Far

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
		sb.WriteString(fmt.Sprintf("  Next fire: %s (%s from now)\n", job.NextFire.Format("2006-01-02 15:04 MST"), humanDelta(time.Until(job.NextFire))))
		if farAccepted && !job.Recurring && time.Until(job.NextFire).Hours()/24 > oneShotFarFutureDays {
			sb.WriteString(fmt.Sprintf("  WARNING: far-future one-shot accepted via far=true - next fire is %d days away; verify the expression before relying on it.\n", int(time.Until(job.NextFire).Hours()/24)))
		}
		if rolloverAck {
			sb.WriteString("  WARNING: same-day overshoot accepted via tomorrow=true - the target time has already passed today, so the fire rolls to tomorrow; verify the next-day fire is intended.\n")
		}
	}
	if preview := cronPromptPreviewLine(job.Prompt); preview != "" {
		sb.WriteString(fmt.Sprintf("  Prompt: %s\n", preview))
	}
	return Result{Content: sb.String()}, nil
}

// cronPromptPreviewLine renders a single-line preview of a job prompt for cron
// tool results. Whenever any part of the prompt is not rendered (clamped
// within the first line, or dropped trailing lines), an explicit marker with
// the dropped-rune count is appended, so a rendered preview is always
// distinguishable from the complete prompt (#5661: marker-less truncation
// poisoned agents' verify loops).
func cronPromptPreviewLine(prompt string) string {
	lines := strings.Split(prompt, "\n")
	first := ""
	firstIdx := -1
	for i, ln := range lines {
		if strings.TrimSpace(ln) != "" {
			first = ln
			firstIdx = i
			break
		}
	}
	if first == "" {
		return ""
	}
	const maxPreview = 80
	runes := []rune(first)
	dropped := 0
	if len(runes) > maxPreview {
		dropped += len(runes) - maxPreview
		first = string(runes[:maxPreview]) + "..."
	}
	for _, ln := range lines[firstIdx+1:] {
		dropped += len([]rune(ln))
	}
	if dropped > 0 {
		first = fmt.Sprintf("%s [+%d chars truncated]", first, dropped)
	}
	return first
}

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
				"description": "REQUIRED. Brief activity label shown in the UI, in the user's language."
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
				"description": "REQUIRED. Brief activity label shown in the UI, in the user's language."
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
				"description": "REQUIRED. Brief activity label shown in the UI, in the user's language."
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

	nextFire := job.NextFire.Format(time.RFC3339)
	if job.NextFire.IsZero() {
		nextFire = "none (paused)"
	}
	fmt.Fprintf(&sb, "Next fire:   %s\n", nextFire)
	fmt.Fprintf(&sb, "Created at:  %s\n", job.CreatedAt.Format(time.RFC3339))
	fmt.Fprintf(&sb, "Prompt:\n%s\n", job.Prompt)
	return Result{Content: sb.String()}, nil
}
