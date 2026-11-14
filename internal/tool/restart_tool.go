package tool

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/cron"
	"github.com/topcheer/ggcode/internal/debug"
)

// The resume nudge fires arm-time + rand(10s..30s) (#5520). The previous
// fixed 3-minute delay made every instance in a batch-restarted fleet wake
// in lockstep (synchronized LLM load spike, cron cadence alignment); the
// random window decorrelates them while staying long enough for the new
// process to boot and resume the session.
const (
	restartNudgeJitterMin = 10 * time.Second
	restartNudgeJitterMax = 30 * time.Second
)

// restartNudgeJitter draws the randomized nudge delay. Package-level var so
// tests can pin the distribution deterministically.
var restartNudgeJitter = func() time.Duration {
	return restartNudgeJitterMin + time.Duration(rand.Int64N(int64(restartNudgeJitterMax-restartNudgeJitterMin)))
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
	// Nudger, when injected, arms a durable one-shot cron firing a jittered
	// 10-30s after the restart, enqueuing restartResumeNudgePrompt so the
	// resumed session continues the prior task. Optional: nil disables the
	// nudge (restart proceeds regardless).
	Nudger ResumeNudgeScheduler
	// Smoke (#5553), when non-nil AND the resolved exec target is known,
	// must pass on the NEW binary before the restart arms: code-signature
	// verify (darwin) + a live agent-core round-trip (`selfcheck`). On
	// failure the restart is REFUSED (IsError result; the process keeps
	// serving). nil disables the gate (pre-#5553 behavior).
	Smoke RestartSmokeRunner
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

	// #5553: refuse to arm until the exec-target binary proves its
	// agent-core plumbing live. The smoke runs on the NEW binary (the
	// exec target), never on the currently running one — a --version
	// check cannot catch the #5549 zero-toolDefs class (restarted seats
	// silently paralyzed). Fresh context: the tool ctx may be near its
	// deadline and this probe gates a process swap.
	if t.Smoke != nil {
		if execPath := t.Requester.ExecTargetPath(); execPath != "" {
			smokeCtx, cancel := context.WithTimeout(context.Background(), RestartSmokeTimeout)
			err := t.Smoke(smokeCtx, execPath)
			cancel()
			if err != nil {
				debug.Log("restart", "pre-arm smoke FAILED on %s: %v", execPath, err)
				return Result{IsError: true, Content: fmt.Sprintf(
					"restart REFUSED (#5553): pre-arm smoke FAILED on exec target %s: %v. The restart was NOT armed; the current process keeps serving. Fix the build (zero-toolDefs / broken registry / exec plumbing), or on macOS re-sign (codesign --force --sign -) if the signature check failed, then retry.", execPath, err)}, nil
			}
			debug.Log("restart", "pre-arm smoke passed on %s", execPath)
		}
	}

	nudgeNote := ""
	if t.Nudger != nil {
		delay := restartNudgeJitter()
		if _, err := t.Nudger.CreateOneShotWithID(restartResumeNudgeID, time.Now().Add(delay), restartResumeNudgePrompt, true); err != nil {
			debug.Log("restart", "failed to arm resume nudge: %v", err)
			nudgeNote = fmt.Sprintf(" NOTE: resume nudge scheduling failed (%v); the session still resumes automatically.", err)
		} else {
			nudgeNote = fmt.Sprintf(" A resume nudge fires ~%s after the restart (jittered 10-30s window, decorrelated from other instances) with prompt %q.", delay.Round(time.Second), restartResumeNudgePrompt)
		}
	}

	t.Requester.RequestRestart(args.Reason, args.Debug)
	// #5367: report the resolved exec target so a wrong-path exec (wrapper,
	// installed copy, PATH shadow) is visible at arm time, before the swap is
	// even attempted.
	execPath := t.Requester.ExecTargetPath()
	okNote := "OK: restart armed. The agent loop terminates immediately after this result — no further tool calls, no further turns. The process restarts as soon as the turn unwinds (with a short fallback timeout). The session resumes automatically after restart."
	if p := execPath; p != "" {
		okNote += fmt.Sprintf(" Exec target: %s (current revision %s). After restart the startup banner verifies the running image.", p, t.Requester.ExecTargetRevision())
	}
	return Result{Content: okNote + nudgeNote, HaltAgent: true}, nil
}

// RestartSmokeTimeout bounds the #5553 pre-arm smoke (registry boot +
// one echo round-trip on a freshly built binary; typically seconds).
const RestartSmokeTimeout = 90 * time.Second

// RestartSmokeRunner runs the #5553 pre-arm smoke against the exec
// target binary. Injected for tests; production wiring uses RestartSmoke.
type RestartSmokeRunner func(ctx context.Context, execPath string) error

// RestartSmoke is the production RestartSmokeRunner: (1) on macOS it
// verifies the exec target's code signature first — an unsigned/invalid
// Mach-O gets SIGKILLed by taskgated ("Killed: 9"), which a naive probe
// would misdiagnose as a build crash; (2) it runs `<execTarget>
// selfcheck` (real registry boot + run_command echo round-trip) and
// fails on any non-zero exit.
func RestartSmoke(ctx context.Context, execPath string) error {
	abs, err := filepath.Abs(execPath)
	if err != nil {
		return fmt.Errorf("resolve exec target: %w", err)
	}
	fi, err := os.Stat(abs)
	if err != nil {
		return fmt.Errorf("stat exec target: %w", err)
	}
	if fi.IsDir() {
		return fmt.Errorf("exec target is a directory, not a binary")
	}
	if runtime.GOOS == "darwin" && looksLikeMachO(abs) {
		if out, cerr := exec.CommandContext(ctx, "codesign", "--verify", abs).CombinedOutput(); cerr != nil {
			return fmt.Errorf("codesign verification failed (unsigned/invalid binary — taskgated may SIGKILL it as 'Killed: 9'; re-sign with codesign --force --sign -): %v: %s", cerr, bytes.TrimSpace(out))
		}
	}
	out, err := exec.CommandContext(ctx, abs, "selfcheck").CombinedOutput()
	if err != nil {
		tail := string(bytes.TrimSpace(out))
		if len(tail) > 500 {
			tail = tail[len(tail)-500:]
		}
		return fmt.Errorf("selfcheck on new binary failed (%v): %s", err, tail)
	}
	return nil
}

// looksLikeMachO reports whether path starts with a Mach-O magic, so
// non-Mach-O exec targets (shell scripts, ELF on darwin CI runners)
// skip the darwin codesign step.
func looksLikeMachO(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	var magic [4]byte
	if _, err := io.ReadFull(f, magic[:]); err != nil {
		return false
	}
	return (magic[0] == 0xFE && magic[1] == 0xED) || // MH_MAGIC / MH_MAGIC_64
		(magic[0] == 0xCE && magic[1] == 0xFA && magic[2] == 0xED && magic[3] == 0xFE) || // byte-swapped
		(magic[0] == 0xCA && magic[1] == 0xFE && magic[2] == 0xBA && magic[3] == 0xBE) // fat binary
}
