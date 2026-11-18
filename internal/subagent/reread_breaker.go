package subagent

// #5013 Ask-2: same-file repeated-read circuit breaker. Task-contract prose
// has zero enforcement power; a sub-agent that loops on reads burns tokens
// until a human cancels (sa-2: 173 reads / sa-3: 181 reads). This breaker is
// deterministic: more than rereadBreakerLimit identical-path reads with no
// intervening edit trips it, and the runner terminates the run with a partial
// result. Edits reset the window because re-reading a file you just changed
// is legitimate verification, not a loop.
//
// #5737: the strict calibration conflates "same path read again" with "loop"
// for read-only research runs, where re-reading a file across iterations IS
// the task shape (hover-check, cross-reference, verify-before-claim) and no
// edit can ever reset the window - an incident run was scrapped after 4
// reads of one file. The breaker is now policy-aware: RereadPolicyResearch
// (read-only/read-heavy runs) raises the per-path threshold, warns once
// (surfaced to the model via the run mailbox so it can course-correct), and
// terminates only on continued escalation. Runs whose effective toolset
// contains no edit-class tool get the research calibration automatically
// (RereadPolicyAuto) - a run that cannot edit can never produce the
// edit-reset signal that justifies the tight limit.

import (
	"encoding/json"
	"fmt"
	"strings"
)

const rereadBreakerLimit = 3

// #5737 research calibration: a read-heavy run may legitimately re-open the
// same file across iterations, so the warn threshold is far above the strict
// trip point and the hard stop sits beyond it, leaving room for one parallel
// batch of reads after the warning. max_tool_calls still bounds the run.
const (
	rereadResearchWarnLimit = 12
	rereadResearchTripLimit = 24
)

var rereadBreakerReadTools = map[string]bool{
	"read_file":       true,
	"multi_file_read": true,
}

var rereadBreakerEditTools = map[string]bool{
	"edit_file": true, "multi_edit_file": true, "write_file": true,
	"multi_file_edit": true, "multi_file_write": true, "file_ops": true,
	"batch_replace": true, "notebook_edit": true,
	// #5790: apply_patch is the OpenAI Responses-native patch executor
	// (internal/tool/apply_patch.go). It mutates source exactly like the
	// tools above, so it must reset the same-path reread window - without
	// it, a Responses-native run's read -> apply_patch -> verify-read
	// workflow tripped the breaker on the 4th read of the file.
	// internal/agent already counts it via mutatingSourceEdit (#5254).
	"apply_patch": true,
}

// RereadPolicy selects the #5013 same-file reread breaker calibration for a
// run (#5737). The zero value (RereadPolicyAuto) keeps the strict #5013
// behavior for every run that can edit files and grants the research
// calibration to structurally read-only runs.
type RereadPolicy int

const (
	// RereadPolicyAuto resolves from the run's effective toolset: no
	// edit-class tool available => research calibration, else strict.
	RereadPolicyAuto RereadPolicy = iota
	// RereadPolicyDefault forces the strict #5013 breaker (trip after
	// rereadBreakerLimit same-path reads with no intervening edit)
	// regardless of the toolset.
	RereadPolicyDefault
	// RereadPolicyResearch raises the per-path threshold, warns once
	// before acting, and terminates only past rereadResearchTripLimit.
	RereadPolicyResearch
)

type rereadAction int

const (
	rereadNone rereadAction = iota
	rereadWarn
	rereadTrip
)

type rereadBreaker struct {
	counts  map[string]int
	tripped string
	warned  map[string]bool
	policy  RereadPolicy
}

func newRereadBreaker() *rereadBreaker {
	return newRereadBreakerWithPolicy(RereadPolicyDefault)
}

func newRereadBreakerWithPolicy(policy RereadPolicy) *rereadBreaker {
	return &rereadBreaker{
		counts: make(map[string]int),
		warned: make(map[string]bool),
		policy: policy,
	}
}

// rereadBreakerPaths extracts the file paths a read-class call touches
// (best effort: JSON parse failures yield no paths and never trip).
func rereadBreakerPaths(toolName, rawArgs string) []string {
	if !rereadBreakerReadTools[toolName] || strings.TrimSpace(rawArgs) == "" {
		return nil
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(rawArgs), &args); err != nil {
		return nil
	}
	var paths []string
	if p, ok := args["path"].(string); ok && p != "" {
		paths = append(paths, p)
	}
	if files, ok := args["files"].([]any); ok {
		for _, f := range files {
			if fm, ok := f.(map[string]any); ok {
				if p, ok := fm["path"].(string); ok && p != "" {
					paths = append(paths, p)
				}
			}
		}
	}
	return paths
}

// record observes one tool call. It returns the tripped path and its read
// count when the breaker fires (first time only).
func (b *rereadBreaker) record(toolName, rawArgs string) (path string, count int, tripped bool) {
	p, n, action := b.recordAction(toolName, rawArgs)
	return p, n, action == rereadTrip
}

// recordAction observes one tool call and reports the breaker's verdict:
// rereadWarn (research calibration crossed its warn threshold - the run may
// continue), rereadTrip (terminate), or rereadNone.
func (b *rereadBreaker) recordAction(toolName, rawArgs string) (path string, count int, action rereadAction) {
	if b.tripped != "" {
		return "", 0, rereadNone
	}
	if rereadBreakerEditTools[toolName] {
		// A mutation makes subsequent reads of the same file legitimate
		// verification - reset the window (and any pending warning).
		b.counts = make(map[string]int)
		b.warned = make(map[string]bool)
		return "", 0, rereadNone
	}
	for _, p := range rereadBreakerPaths(toolName, rawArgs) {
		key := strings.TrimSpace(p)
		if key == "" {
			continue
		}
		b.counts[key]++
		n := b.counts[key]
		if b.policy == RereadPolicyResearch {
			if n >= rereadResearchTripLimit {
				b.tripped = key
				return key, n, rereadTrip
			}
			if n > rereadResearchWarnLimit && !b.warned[key] {
				b.warned[key] = true
				return key, n, rereadWarn
			}
			continue
		}
		if n > rereadBreakerLimit {
			b.tripped = key
			return key, n, rereadTrip
		}
	}
	return "", 0, rereadNone
}

func (b *rereadBreaker) reason() string {
	if b.tripped == "" {
		return ""
	}
	if b.policy == RereadPolicyResearch {
		return fmt.Sprintf("same-file reread loop: %q read %d times with no intervening edit despite a prior warning (#5013 reread breaker, #5737 research policy)",
			b.tripped, b.counts[b.tripped])
	}
	return fmt.Sprintf("same-file reread loop: %q read %d times with no intervening edit (#5013 reread breaker)",
		b.tripped, b.counts[b.tripped])
}

// warning is the #5737 research-calibration advisory emitted at the warn
// threshold; the runner surfaces it as an event and (best effort) injects it
// into the run's mailbox so the model can course-correct before the hard
// stop.
func (b *rereadBreaker) warning(path string, count int) string {
	return fmt.Sprintf("same-file reread warning: %q has been read %d times with no intervening edit; re-reading it further will terminate the run at %d reads - derive what you need from the reads already in your context (#5737 reread breaker, research policy)",
		path, count, rereadResearchTripLimit)
}

// ResearchRereadLimits returns the (warn, trip) same-file read counts of
// the #5737 research calibration, for disclosure surfaces (spawn results,
// docs).
func ResearchRereadLimits() (warn, trip int) {
	return rereadResearchWarnLimit, rereadResearchTripLimit
}

// rereadPolicyForRun resolves the effective breaker policy (#5737): an
// explicit policy wins; RereadPolicyAuto inspects the run's effective
// toolset - a run with no edit-class tool available can never produce the
// edit-reset signal, so it gets the research calibration.
func rereadPolicyForRun(explicit RereadPolicy, allowedTools []string, allTools []ToolInfo) RereadPolicy {
	switch explicit {
	case RereadPolicyDefault, RereadPolicyResearch:
		return explicit
	}
	if runIsReadOnly(allowedTools, allTools) {
		return RereadPolicyResearch
	}
	return RereadPolicyDefault
}

// runIsReadOnly reports whether the run's effective toolset contains no
// edit-class tool. A restricted toolset (allowedTools non-empty) is read-only
// when none of the allowed names is an edit-class tool; an unrestricted run
// inherits the parent registry minus blocked tools, so it is read-only only
// when the registry itself has no edit-class tool.
func runIsReadOnly(allowedTools []string, allTools []ToolInfo) bool {
	if len(allowedTools) > 0 {
		for _, name := range allowedTools {
			if rereadBreakerEditTools[name] {
				return false
			}
		}
		return true
	}
	for _, ti := range allTools {
		if rereadBreakerEditTools[ti.Name()] {
			return false
		}
	}
	return true
}
