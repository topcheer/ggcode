package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/cron"
	"github.com/topcheer/ggcode/internal/debug"
)

// restartResumeNudgeDelayBase is the nominal delay before the resume nudge
// fires: long enough for the new process to boot and resume the session,
// short enough that the follow-up feels immediate.
const restartResumeNudgeDelayBase = 3 * time.Minute

// restartResumeNudgeJitter spreads identical-fleet restarts (#5520): every
// instance used the same constant, so a batch restart (binary-sync
// broadcast) fired all resume nudges in lockstep. The jitter is derived
// deterministically from the pid so it is stable within one process and
// spread across a fleet.
const restartResumeNudgeJitterSpan = 120 * time.Second

func restartResumeNudgeDelay() time.Duration {
	const steps = 10
	offset := time.Duration(os.Getpid()%steps) * (restartResumeNudgeJitterSpan / steps)
	return restartResumeNudgeDelayBase - restartResumeNudgeJitterSpan/2 + offset
}

// restartResumeNudgePrompt is the prompt enqueued by the resume nudge.
const restartResumeNudgePrompt = "继续进行之前的任务"

// restartResumeNudgeID is the stable one-shot ID the resume nudge is armed
// under: re-arming (a second restart before the nudge fires) replaces the
// pending job instead of stacking a duplicate (#5425).
const restartResumeNudgeID = cron.RestartResumeNudgeID

// ResumeNudgeScheduler is the scheduler surface the restart tool needs to
// arm its post-restart resume nudge (a durable one-shot cron).
type ResumeNudgeScheduler interface {
	CreateOneShotWithID(id string, fireAt time.Time, prompt string, queueIfBusy bool) (cron.Job, error)
}

type RestartRequester interface {
	RequestRestart(reason string, debugMode bool)
	// ExecTargetPath returns the resolved path of the binary the restart will
	// exec ("" when unavailable). #5367: surfaces the exec target in the arm
	// announcement so a wrong-path exec is visible at arm time.
	ExecTargetPath() string
	// ExecTargetRevision returns the vcs revision of the exec target (""
	// when unknown).
	ExecTargetRevision() string
}

type RestartTool struct {
	Requester RestartRequester
	// Nudger, when injected, arms a durable one-shot cron firing
	// restartResumeNudgeDelay after the restart, enqueuing
	// restartResumeNudgePrompt so the resumed session continues the prior
	// task. Optional: nil disables the nudge (restart proceeds regardless).
	Nudger ResumeNudgeScheduler
}

func (t *RestartTool) Name() string { return "restart" }

func (t *RestartTool) Description() string {
	return "Restart the ggcode process itself, preserving the current session (conversation history is resumed automatically after restart). " +
		"Use ONLY when a change requires a fresh process to take effect — e.g. the ggcode binary was updated/rebuilt, or a fix to startup-time behavior (config loading, early initialization) was made. " +
		"Regular code changes in the workspace do NOT need a restart. This should typically be the last tool call in your turn."
}

func (t *RestartTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"reason": {
				"type": "string",
				"description": "REQUIRED. Why a restart is needed (e.g. 'binary updated to fix #123'). Shown to the user before the process restarts."
			},
			"debug": {
				"type": "boolean",
				"description": "Restart with GGCODE_DEBUG=1 enabled. Default false."
			}
		},
		"required": ["reason"]
	}`)
}

func (t *RestartTool) Available() bool { return t.Requester != nil }

func (t *RestartTool) Execute(ctx context.Context, input json.RawMessage) (Result, error) {
	var args struct {
		Reason string `json:"reason"`
		Debug  bool   `json:"debug"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid input: %v", err)}, nil
	}
	if t.Requester == nil {
		return Result{IsError: true, Content: "restart is not available in this host — no restart requester was injected. Ask the user to restart the process manually if a restart is truly required."}, nil
	}

	if strings.TrimSpace(args.Reason) == "" {
		return Result{IsError: true, Content: "reason is REQUIRED: explain why the restart is needed (e.g. 'binary updated to fix #123') - it is shown to the user before the process restarts."}, nil
	}
	debug.Log("restart", "restart tool invoked: reason=%q debug=%v", args.Reason, args.Debug)

	nudgeNote := ""
	if t.Nudger != nil {
		if _, err := t.Nudger.CreateOneShotWithID(restartResumeNudgeID, time.Now().Add(restartResumeNudgeDelay()), restartResumeNudgePrompt, true); err != nil {
			debug.Log("restart", "failed to arm resume nudge: %v", err)
			nudgeNote = fmt.Sprintf(" NOTE: resume nudge scheduling failed (%v); the session still resumes automatically.", err)
		} else {
			nudgeNote = fmt.Sprintf(" A resume nudge will fire %s after the restart with prompt %q.", restartResumeNudgeDelay(), restartResumeNudgePrompt)
		}
	}

	t.Requester.RequestRestart(args.Reason, args.Debug)
	// #5367: report the resolved exec target so a wrong-path exec (wrapper,
	// installed copy, PATH shadow) is visible at arm time, before the swap is
	// even attempted.
	execPath := t.Requester.ExecTargetPath()
	okNote := "OK: restart armed. The process restarts as soon as this turn finishes (with a short fallback timeout). Do NOT issue any further tool calls — end your turn now. The session resumes automatically after restart."
	if p := execPath; p != "" {
		okNote += fmt.Sprintf(" Exec target: %s (current revision %s). After restart the startup banner verifies the running image.", p, t.Requester.ExecTargetRevision())
	}
	return Result{Content: okNote + nudgeNote}, nil
}
