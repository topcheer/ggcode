package agent

import (
	"fmt"
	"hash/fnv"
	"strings"
	"sync"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/tool"
)

// Non-atomic tool-call outcome semantics for mutating tools.
//
// Research basis: "Verified Tool Calls Improve LLM Agent Reliability Under
// Non-Atomic Failures" (arXiv:2608.02645). The paper surveys production agent
// frameworks and finds pervasive tool-call side-effect issues: when a MUTATING
// call fails ambiguously (timeout, cancellation, or panic mid-execution), the
// side effect may be partially or fully applied, but nothing in the framework
// tells the agent that. The model sees a bare "tool error: timeout", assumes
// the call did not happen, and re-issues it -- double commits, duplicated
// file edits, half-applied batch operations. The paper's recommendation is
// that the FRAMEWORK, not the model, should carry execution semantics:
// classify outcomes and guide verification-before-retry.
//
// This is execution semantics at the tool boundary (same layer as
// transient_retry.go, which deliberately never auto-retries mutating tools).
// It is NOT a detector: no quality signal is judged, no code inspected.
//
// Three outcome classes for mutating tools:
//
//	preExec   -- the call failed BEFORE any change could be applied
//	             (validation, permission, hook block, user-declined diff).
//	             Telling the model this avoids a wasted verification cycle.
//	ambiguous -- the call failed MID-EXECUTION (timeout/cancel/panic/killed).
//	             Side effects are UNKNOWN. The result annotation instructs
//	             verify-state-then-reconcile instead of blind re-issue.
//	none      -- ordinary command failure (e.g. non-zero shell exit with
//	             visible output); the model already understands those.
//
// Additionally, a per-run ledger records ambiguous attempts keyed by
// (tool, args hash). If the model re-issues the IDENTICAL mutating call:
//   - and it succeeds -> [non-atomic] warns that the earlier ambiguous
//     attempt may ALSO have applied (duplicate-commit risk);
//   - and it fails again -> the warning is reinforced with the attempt count.
//
// All annotation is zero-LLM-token work; costs only a few appended bytes on
// the (rare) failing paths of mutating tools.

// mutatingOutcomeClass classifies how a mutating tool call terminated.
type mutatingOutcomeClass int

const (
	outcomeNone mutatingOutcomeClass = iota
	outcomePreExec
	outcomeAmbiguous
)

// shellSideEffectTools execute arbitrary shell whose failure modes are
// harness-level only (timeout/cancel/panic). A non-zero exit with visible
// output is ordinary shell semantics the model already reasons about --
// classifying those "pre-exec safe" would be WRONG (cmd1 && cmd2 failing at
// cmd2 still applied cmd1), so shell tools never receive the preExec note.
var shellSideEffectTools = map[string]bool{
	"run_command":         true,
	"start_command":       true,
	"write_command_input": true,
	"stop_command":        true,
}

// gitStateTools mutate repository state without necessarily touching the
// working tree (mutatesSourceTree only covers tree rewrites). A timed-out
// git_commit is the canonical non-atomic failure: the commit may have landed.
var gitStateTools = map[string]bool{
	"git_commit":   true,
	"git_add":      true,
	"git_tag":      true,
	"git_stash":    true,
	"git_checkout": true,
}

// hasNonAtomicSemantics reports whether a tool's failures carry side-effect
// ambiguity worth annotating: source-tree mutators, shell executors, and
// git state mutators. Read-only tools are excluded entirely.
func hasNonAtomicSemantics(toolName string) bool {
	if shellSideEffectTools[toolName] || gitStateTools[toolName] {
		return true
	}
	if mutatesSourceTree(toolName) {
		return true
	}
	return false
}

// ambiguousFailurePatterns indicate the call was interrupted mid-execution:
// the side effect state is unknown. Matched against lowercased content.
var ambiguousFailurePatterns = []string{
	"timed out",
	"timeout",
	"deadline exceeded",
	"was cancelled", // safeExecute ctx.Done race: goroutine may still finish
	"context canceled",
	"context deadline",
	"cancelled during",
	"cancelled via context",
	"panicked",
	"interrupted",
	"killed",
}

// preExecFailurePatterns indicate the call was rejected BEFORE dispatch or by
// pre-execution validation: no side effect could have been applied. Only
// matched for non-shell mutating tools (see shellSideEffectTools).
var preExecFailurePatterns = []string{
	"missing required parameter",
	"invalid argument",
	"malformed",
	"unknown tool",
	"permission denied",
	"not permitted",
	"sandbox",
	"blocked by hook",
	"protected path",
	"validation failed",
	"no such file",
	"not found",
	"cancelled by user", // diff-confirm decline: nothing was written
	"declined",
	"pre-write",
	"dry-run",
}

// classifyMutatingFailure maps an error result to an outcome class.
// Read-only tools return outcomeNone unconditionally: a timeout on `grep`
// carries no side-effect ambiguity. Ambiguous patterns are checked FIRST
// (a message containing both, e.g. "permission denied after partial apply",
// is treated as ambiguous -- the safer direction). Shell tools can only ever
// be ambiguous or none.
func classifyMutatingFailure(toolName, content string) mutatingOutcomeClass {
	if !hasNonAtomicSemantics(toolName) {
		return outcomeNone
	}
	lower := strings.ToLower(content)
	if lower == "" {
		return outcomeNone
	}
	for _, pat := range ambiguousFailurePatterns {
		if strings.Contains(lower, pat) {
			return outcomeAmbiguous
		}
	}
	if !shellSideEffectTools[toolName] {
		for _, pat := range preExecFailurePatterns {
			if strings.Contains(lower, pat) {
				return outcomePreExec
			}
		}
	}
	return outcomeNone
}

// mutatingLedger records ambiguous attempts per (tool, args) key for the
// current run, so a re-issue of the identical call can be annotated with
// double-apply risk. Bounded: entries are evicted FIFO at the cap.
type mutatingLedger struct {
	mu      sync.Mutex
	entries map[string]int // key -> ambiguous attempt count
	order   []string       // FIFO eviction order
}

const maxMutatingLedgerEntries = 128

func newMutatingLedger() *mutatingLedger {
	return &mutatingLedger{entries: make(map[string]int)}
}

// Nil-receiver safe: Agent literals in tests (&Agent{}) must not panic when
// the executeToolCall wiring probes the ledger.
func (l *mutatingLedger) reset() {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.entries = make(map[string]int)
	l.order = l.order[:0]
}

// mutatingLedgerKey builds a stable key from tool name and raw arguments.
func mutatingLedgerKey(toolName string, args []byte) string {
	h := fnv.New32a()
	h.Write(args)
	return fmt.Sprintf("%s:%08x", toolName, h.Sum32())
}

// lookupAmbiguous returns how many ambiguous attempts were recorded for this
// exact call, or 0.
func (l *mutatingLedger) lookupAmbiguous(toolName string, args []byte) int {
	if l == nil {
		return 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.entries[mutatingLedgerKey(toolName, args)]
}

// recordAmbiguous registers an ambiguous outcome for this exact call.
func (l *mutatingLedger) recordAmbiguous(toolName string, args []byte) {
	if l == nil {
		return
	}
	key := mutatingLedgerKey(toolName, args)
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, exists := l.entries[key]; !exists && len(l.order) >= maxMutatingLedgerEntries {
		evict := l.order[0]
		l.order = l.order[1:]
		delete(l.entries, evict)
	}
	if _, exists := l.entries[key]; !exists {
		l.order = append(l.order, key)
	}
	l.entries[key]++
}

// nonAtomicResultTag is the critical head tag for these notes: ambiguous
// outcomes are immediate correctness/safety guidance and must not be
// suppressed by the per-turn guidance budget.
const nonAtomicResultTag = "[non-atomic]"

// annotateMutatingOutcome inspects a finished mutating tool call and appends
// outcome semantics to the result the model will see. prevAmbiguous is the
// count of prior ambiguous attempts for this exact (tool, args) pair.
func (a *Agent) annotateMutatingOutcome(toolName string, args []byte, result tool.Result, prevAmbiguous int) tool.Result {
	if !hasNonAtomicSemantics(toolName) {
		return result
	}
	if result.IsError {
		switch cls := classifyMutatingFailure(toolName, result.Content); cls {
		case outcomeAmbiguous:
			a.mutateLedger.recordAmbiguous(toolName, args)
			a.appendGuidance(&result, nonAtomicAmbiguousNote(toolName, prevAmbiguous+1))
			debug.Log("agent", "non-atomic: %s ended AMBIGUOUS (attempt %d this run), ledger updated", toolName, prevAmbiguous+1)
		case outcomePreExec:
			// Nothing was applied; a previous ambiguous attempt for identical
			// args is still live -- keep the double-apply warning.
			if prevAmbiguous > 0 {
				a.appendGuidance(&result, nonAtomicDoubleApplyNote(toolName, prevAmbiguous))
			}
			a.appendGuidance(&result, nonAtomicPreExecNote())
		default:
			// Ordinary failure (e.g. non-zero exit): only warn if an earlier
			// ambiguous attempt for identical args is outstanding.
			if prevAmbiguous > 0 {
				a.appendGuidance(&result, nonAtomicDoubleApplyNote(toolName, prevAmbiguous))
			}
		}
		return result
	}
	// Success: did an earlier identical call also land ambiguously?
	if prevAmbiguous > 0 {
		a.appendGuidance(&result, nonAtomicDoubleApplyNote(toolName, prevAmbiguous))
	}
	return result
}

func nonAtomicAmbiguousNote(toolName string, attempt int) string {
	return fmt.Sprintf("%s The %s call was interrupted mid-execution (attempt %d this run). Its side effects are UNKNOWN -- it may have partially or fully applied. Do NOT blindly re-issue the same call: first verify actual state (git log/status for commits, read the files for edits), then reconcile only what is missing. (Research: arXiv:2608.02645, non-atomic tool failures)",
		nonAtomicResultTag, toolName, attempt)
}

func nonAtomicDoubleApplyNote(toolName string, attempts int) string {
	return fmt.Sprintf("%s WARNING: an earlier identical %s call ended in an UNKNOWN state before this one (attempt %d). The earlier attempt may ALSO have applied -- check for duplicate side effects (duplicate commit, doubled edit) and reconcile before continuing.",
		nonAtomicResultTag, toolName, attempts)
}

func nonAtomicPreExecNote() string {
	return "[no-side-effect] This call failed BEFORE any change could be applied (validation/permission/declined). Safe to fix the arguments and retry without re-verifying workspace state."
}
