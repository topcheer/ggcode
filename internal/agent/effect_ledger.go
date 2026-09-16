package agent

// effect_ledger.go -- Side-Effect Duplicate Guard (Effect Ledger).
//
// This is a tool-execution-layer mechanism (sibling of commandCache), NOT a
// code-quality detector: it records what actually executed and annotates tool
// results at the execution path.
//
// Research basis:
//   - Uber LangEffect (2025): every side-effecting agent action is registered
//     in an effect log; retries after an uncertain outcome risk duplicating
//     the external effect (LIFO compensation on failure is the full saga
//     treatment — this implements the awareness layer of that design).
//   - RAC: Robust Agent Compensation (arXiv:2605.03409): log-based recovery
//     for agent frameworks; the log of executed effects is the prerequisite
//     for any compensation.
//   - "Agent Idempotency" production patterns (2026): agents retry failed
//     tool calls, and a retry of a side-effecting command after timeout or
//     mid-flight error can duplicate the external effect (git push twice,
//     npm publish twice, duplicate API writes) because the first attempt's
//     outcome is UNKNOWN — a timeout kill says nothing about whether the
//     work already landed.
//
// Gap in this codebase: commandCache only serves deterministic build/test
// commands and never caches failures (#1717). Nothing else records shell
// command outcomes, so when the agent retries the identical side-effecting
// command after a timeout/error, the retry result is presented as
// authoritative with no hint that an earlier attempt may have (partially)
// landed. The model then draws conclusions from incomplete state.
//
// Design:
//   - record() stores the outcome of every actually-executed run_command
//     that ended in failure or an UNCERTAIN state (timeout / kill / cancel).
//     Successful executions are not recorded (successful non-cacheable
//     re-runs are usually intentional; verification re-runs are already
//     covered by redundantReverify).
//   - priorHint() returns an annotation when the identical
//     (command, working_dir) has a failed/uncertain prior attempt inside
//     the retry window. The caller appends it to the retry's tool result,
//     so the model is told to verify external state before treating the
//     retry output as authoritative.
//   - Never-executed shapes (permission denial, gate block, invalid input,
//     shell resolution failure) are excluded: they leave no external effect.
//
// Protocol safety: the annotation is appended to the tool result content
// itself (same pattern as the [cached — ...] annotation in command_cache.go);
// no message is inserted between tool_calls and tool_results.

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/tool"
)

// effectOutcome classifies how a side-effecting execution ended.
type effectOutcome int

const (
	effectFailed    effectOutcome = iota // clean failure; effect most likely did not land
	effectUncertain                      // timeout/kill/cancel; effect state UNKNOWN (may have partially landed)
)

type effectRecord struct {
	command string
	workDir string
	at      time.Time
	outcome effectOutcome
}

var (
	// maxEffectRecords bounds the ledger (ring eviction of the oldest).
	maxEffectRecords = 64
	// maxEffectWarnings caps annotations per Agent to avoid nagging in a
	// tight retry loop.
	maxEffectWarnings = 5
	// effectRetryWindow bounds how old a prior attempt may be to still
	// trigger an annotation — older failures are usually deliberate re-runs
	// (e.g. re-deploying much later), not blind retries.
	effectRetryWindow = 30 * time.Minute
)

// neverExecutedMarkers are error-result shapes produced BEFORE the command
// ever ran. They leave no external effect and must not enter the ledger.
var neverExecutedMarkers = []string{
	"Permission denied for tool", // policy/user denial (agent_tool.go)
	"invalid input:",             // argument parse failure
	"blocked",                    // command gate block
	"failed to resolve shell",    // environment failure
	"command job manager not available",
	"failed to start command job", // never spawned
}

// uncertainMarkers identify outcomes where the process ran but its final
// state is unknown — the dangerous retry case.
var uncertainMarkers = []string{
	"timed out after",           // job-manager timeout (command_jobs.go)
	"context deadline exceeded", // direct-path timeout
	"signal: killed",            // OOM/kill mid-flight
	"context canceled",          // user cancel mid-flight
}

type effectLedgerState struct {
	mu      sync.Mutex
	records []effectRecord
	warned  int
	// now is swappable for tests.
	now func() time.Time
}

func newEffectLedger() *effectLedgerState {
	return &effectLedgerState{now: time.Now}
}

// classifyEffectOutcome maps a tool result to a ledger outcome.
// The second return value is false when the command never executed
// (denial/parse/gate) or succeeded — neither enters the ledger.
func classifyEffectOutcome(res tool.Result) (effectOutcome, bool) {
	if !res.IsError {
		return 0, false
	}
	c := res.Content
	for _, m := range neverExecutedMarkers {
		if strings.Contains(c, m) {
			return 0, false
		}
	}
	for _, m := range uncertainMarkers {
		if strings.Contains(c, m) {
			return effectUncertain, true
		}
	}
	return effectFailed, true
}

// effectKey normalizes a (command, workDir) identity. Leading activity
// comments are stripped the same way command_cache does (#1530) so the
// mandated '# description' line does not defeat matching.
func effectKey(command, workDir string) string {
	return strings.TrimSpace(workDir) + "\x00" + strings.TrimSpace(stripLeadingShellComment(command))
}

// priorHint returns an annotation if an identical command has a
// failed/uncertain prior attempt inside the retry window.
func (e *effectLedgerState) priorHint(command, workDir string) string {
	if e == nil || command == "" {
		return ""
	}
	key := effectKey(command, workDir)
	e.mu.Lock()
	defer e.mu.Unlock()
	now := e.now()
	for i := len(e.records) - 1; i >= 0; i-- {
		r := e.records[i]
		if effectKey(r.command, r.workDir) != key {
			continue
		}
		if now.Sub(r.at) > effectRetryWindow {
			return ""
		}
		if e.warned >= maxEffectWarnings {
			return ""
		}
		e.warned++
		return effectHintText(r, now)
	}
	return ""
}

func effectHintText(r effectRecord, now time.Time) string {
	state := "failed"
	why := ""
	if r.outcome == effectUncertain {
		state = "timed out / was interrupted"
		why = " A timed-out or interrupted command may have PARTIALLY landed"
	}
	return fmt.Sprintf(
		"\n[Effect Ledger] This exact command already ran earlier in this session — the most recent prior attempt (%s ago) %s.%s "+
			"Do not treat this run's output as proof about the earlier attempt. Verify the actual external state before "+
			"re-attempting or drawing conclusions (e.g. git log/status for pushes, check the published/deployed artifact, "+
			"re-read files the command may have touched).\n",
		now.Sub(r.at).Round(time.Second), state, why)
}

// record stores the outcome of an executed run_command attempt (failures and
// uncertain outcomes only; successes are intentionally not recorded).
func (e *effectLedgerState) record(command, workDir string, outcome effectOutcome) {
	if e == nil || command == "" {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.records = append(e.records, effectRecord{
		command: strings.TrimSpace(stripLeadingShellComment(command)),
		workDir: strings.TrimSpace(workDir),
		at:      e.now(),
		outcome: outcome,
	})
	if len(e.records) > maxEffectRecords {
		// Drop the oldest half to amortize the slice copy.
		e.records = append([]effectRecord(nil), e.records[len(e.records)/2:]...)
	}
}

// recordEffectAttempt is the agent-loop entry point: called after a real
// run_command execution. It first checks for a prior failed/uncertain
// attempt (BEFORE recording the current one, so an attempt never annotates
// itself), then records the current outcome. The returned annotation, if
// non-empty, must be appended to the tool result content.
func (a *Agent) recordEffectAttempt(name string, args []byte, res tool.Result) string {
	if name != "run_command" {
		return ""
	}
	var parsed struct {
		Command    string `json:"command"`
		WorkingDir string `json:"working_dir"`
	}
	if err := json.Unmarshal(args, &parsed); err != nil || parsed.Command == "" {
		return ""
	}
	ledger := a.effectLedger
	if ledger == nil {
		return ""
	}
	outcome, executed := classifyEffectOutcome(res)
	// Denials/parse failures never executed: nothing to record, and a prior
	// failure hint is still legitimate (the model is re-formulating a call
	// for a command that previously failed to run) — but the annotation is
	// most useful on actual executions, so only hint when executed.
	hint := ""
	if executed {
		hint = ledger.priorHint(parsed.Command, parsed.WorkingDir)
		ledger.record(parsed.Command, parsed.WorkingDir, outcome)
	}
	return hint
}

// resetEffectLedgerForTests clears ledger state between tests.
func (e *effectLedgerState) resetForTests() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.records = nil
	e.warned = 0
}
