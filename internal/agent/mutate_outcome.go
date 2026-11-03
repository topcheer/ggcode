package agent

import (
	"fmt"
	"hash/fnv"
	"strings"
	"sync"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/tool"
)

type mutatingOutcomeClass int

const (
	outcomeNone mutatingOutcomeClass = iota
	outcomePreExec
	outcomeAmbiguous
)

var shellSideEffectTools = map[string]bool{
	"run_command":         true,
	"start_command":       true,
	"write_command_input": true,
	"stop_command":        true,
}

var gitStateTools = map[string]bool{
	"git_commit":   true,
	"git_add":      true,
	"git_tag":      true,
	"git_stash":    true,
	"git_checkout": true,
}

func hasNonAtomicSemantics(toolName string) bool {
	if shellSideEffectTools[toolName] || gitStateTools[toolName] {
		return true
	}
	if mutatesSourceTree(toolName) {
		return true
	}
	return false
}

var ambiguousFailurePatterns = []string{
	"timed out",
	"timeout",
	"deadline exceeded",
	"was cancelled",
	"context canceled",
	"context deadline",
	"cancelled during",
	"cancelled via context",
	"panicked",
	"interrupted",
	"killed",
}

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
	"cancelled by user",
	"declined",
	"pre-write",
	"dry-run",
}

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

type mutatingLedger struct {
	mu      sync.Mutex
	entries map[string]int
	order   []string
}

const maxMutatingLedgerEntries = 128

func newMutatingLedger() *mutatingLedger {
	return &mutatingLedger{entries: make(map[string]int)}
}

func (l *mutatingLedger) reset() {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.entries = make(map[string]int)
	l.order = l.order[:0]
}

func mutatingLedgerKey(toolName string, args []byte) string {
	h := fnv.New32a()
	h.Write(args)
	return fmt.Sprintf("%s:%08x", toolName, h.Sum32())
}

func (l *mutatingLedger) lookupAmbiguous(toolName string, args []byte) int {
	if l == nil {
		return 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.entries[mutatingLedgerKey(toolName, args)]
}

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

const nonAtomicResultTag = "[non-atomic]"

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

			if prevAmbiguous > 0 {
				a.appendGuidance(&result, nonAtomicDoubleApplyNote(toolName, prevAmbiguous))
			}
			a.appendGuidance(&result, nonAtomicPreExecNote())
		default:

			if prevAmbiguous > 0 {
				a.appendGuidance(&result, nonAtomicDoubleApplyNote(toolName, prevAmbiguous))
			}
		}
		return result
	}

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
