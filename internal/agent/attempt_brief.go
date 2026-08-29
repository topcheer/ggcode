//go:build goolm

package agent

import (
	"fmt"
	"strings"
	"sync"
)

// attemptBriefState tracks tool-call outcomes within a single run and, when
// the agent has accumulated enough failed attempts, injects a compact brief
// summarising what was tried and why it failed - so the agent pivots rather
// than repeating the same dead-end strategy.
//
// Frontier basis: "Scaling Test-Time Compute for Agentic Coding"
// (arXiv:2604.16529, April 2026): "The main challenge is no longer generating
// more attempts, but representing prior experience in a form that can be
// effectively selected from and reused." This detector produces a lightweight,
// deterministic version of that compact trajectory representation.
const (
	maxAttemptBriefFire    = 2  // inject at most 2 briefs per run
	maxAttemptBriefEntries = 40 // track at most 40 recent tool outcomes
	briefMinFailures       = 3  // need ≥3 failures before generating a brief
	briefMinDistinctTools  = 2  // need failures across ≥2 distinct tools/operations
	briefCooldownIter      = 4  // iterations to wait between fires
)

type toolOutcome struct {
	tool     string
	target   string // file path or command (truncated)
	success  bool
	iter     int
	errLabel string // short error category (e.g. "not_unique", "exit_1")
}

type attemptBriefState struct {
	mu           sync.Mutex
	entries      []toolOutcome
	firedCount   int
	lastFireIter int
	runIter      int
}

func newAttemptBriefState() *attemptBriefState {
	return &attemptBriefState{}
}

func (s *attemptBriefState) reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries = s.entries[:0]
	s.firedCount = 0
	s.lastFireIter = -briefCooldownIter // allow first fire as soon as threshold met
	s.runIter = 0
}

// recordOutcome logs a tool call result. toolName is the tool's name; target is
// an optional short identifier (file path, command snippet, search pattern).
// errSnippet is the raw error text (if any) from which we derive a label.
func (s *attemptBriefState) recordOutcome(toolName, target string, success bool, iter int, errSnippet string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// #342: a later success on the same tool+target invalidates the earlier
	// failure (same pattern as #331's false_premise fix). fail→fix→re-run→pass
	// is the agent's core correct workflow; keeping the failure entry lets
	// stale failures assemble a "prior approaches did NOT work" narrative
	// during an unbroken success streak.
	if success {
		kept := s.entries[:0]
		for _, e := range s.entries {
			if !e.success && e.tool == toolName && e.target == truncateBrief(target, 60) {
				continue // superseded by this success
			}
			kept = append(kept, e)
		}
		s.entries = kept
	}

	if len(s.entries) >= maxAttemptBriefEntries {
		s.entries = s.entries[1:]
	}

	entry := toolOutcome{
		tool:    toolName,
		target:  truncateBrief(target, 60),
		success: success,
		iter:    iter,
	}
	if !success && errSnippet != "" {
		entry.errLabel = classifyError(errSnippet)
	}
	s.entries = append(s.entries, entry)
}

// maybeBrief inspects accumulated failures and returns a compact guidance string
// if the agent should be reminded of what's already been tried.
func (s *attemptBriefState) maybeBrief(iter int) string {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.runIter = iter
	if s.firedCount >= maxAttemptBriefFire {
		return ""
	}
	if iter-s.lastFireIter < briefCooldownIter {
		return ""
	}

	// Collect failures since last fire (#342: the "since last fire" filter
	// the comment always promised but never implemented — each fire reports
	// only NEW failures, and the same batch is never reused for a second fire).
	failures := make([]toolOutcome, 0, 10)
	for _, e := range s.entries {
		if !e.success && e.iter > s.lastFireIter {
			failures = append(failures, e)
		}
	}
	if len(failures) < briefMinFailures {
		return ""
	}

	// Check diversity: need at least 2 distinct tools or targets that failed.
	distinct := make(map[string]bool)
	for _, f := range failures {
		distinct[f.tool] = true
	}
	if len(distinct) < briefMinDistinctTools {
		return ""
	}

	s.firedCount++
	s.lastFireIter = iter

	// Build compact brief: group failures by tool + error label.
	type groupKey struct{ tool, label string }
	groups := make(map[groupKey][]string)
	for _, f := range failures {
		k := groupKey{f.tool, f.errLabel}
		groups[k] = append(groups[k], f.target)
	}

	var sb strings.Builder
	sb.WriteString("[Attempt Brief] You have had ")
	sb.WriteString(fmt.Sprintf("%d failed tool call(s) across %d distinct operations", len(failures), len(distinct)))
	sb.WriteString(". Prior approaches that did NOT work:\n")

	gIdx := 0
	for k, targets := range groups {
		if gIdx >= 6 { // cap detail
			sb.WriteString("  ...\n")
			break
		}
		label := k.label
		if label == "" {
			label = "failed"
		}
		uniq := dedupBrief(targets)
		tList := strings.Join(uniq, ", ")
		if len(tList) > 80 {
			tList = tList[:77] + "..."
		}
		sb.WriteString(fmt.Sprintf("  - %s (%s): %s\n", k.tool, label, tList))
		gIdx++
	}

	sb.WriteString("Consider a fundamentally different approach rather than retrying variations of the same failed strategy.")
	return sb.String()
}

// classifyError derives a short label from an error snippet.
func classifyError(err string) string {
	err = strings.ToLower(err)
	switch {
	case strings.Contains(err, "not unique") || strings.Contains(err, "not_found") || strings.Contains(err, "no match"):
		return "match_failed"
	case strings.Contains(err, "exit code 1") || strings.Contains(err, "exit_status_1"):
		return "exit_1"
	case strings.Contains(err, "timeout") || strings.Contains(err, "timed out"):
		return "timeout"
	case strings.Contains(err, "permission") || strings.Contains(err, "denied"):
		return "permission"
	case strings.Contains(err, "syntax") || strings.Contains(err, "parse"):
		return "syntax"
	case strings.Contains(err, "already exists") || strings.Contains(err, "conflict"):
		return "conflict"
	case strings.Contains(err, "panic") || strings.Contains(err, "nil pointer"):
		return "crash"
	default:
		return "error"
	}
}

// extractToolTarget pulls a short target identifier (file path, command,
// pattern) from raw tool arguments for attempt-brief tracking.
func extractToolTarget(_ string, rawArgs string) string {
	if rawArgs == "" {
		return ""
	}
	for _, fk := range []string{`"file_path"`, `"path"`, `"file"`, `"command"`, `"pattern"`, `"query"`, `"url"`, `"directory"`} {
		if v := extractJSONStringFieldBrief(rawArgs, fk); v != "" {
			return v
		}
	}
	return ""
}

func extractJSONStringFieldBrief(raw, key string) string {
	idx := strings.Index(raw, key)
	if idx < 0 {
		return ""
	}
	rest := raw[idx+len(key):]
	colon := strings.Index(rest, ":")
	if colon < 0 {
		return ""
	}
	rest = rest[colon+1:]
	start := strings.Index(rest, "\"")
	if start < 0 {
		return ""
	}
	rest = rest[start+1:]
	end := strings.Index(rest, "\"")
	if end < 0 {
		return ""
	}
	return rest[:end]
}

func truncateBrief(s string, max int) string {
	s = strings.TrimSpace(s)
	if max < 4 {
		// Cannot fit an ellipsis; defensive against future callers passing
		// tiny/negative budgets (s[:max-3] would slice out of range).
		return s
	}
	if len(s) > max {
		return s[:max-3] + "..."
	}
	return s
}

func dedupBrief(items []string) []string {
	seen := make(map[string]bool, len(items))
	out := make([]string, 0, len(items))
	for _, it := range items {
		if !seen[it] {
			seen[it] = true
			out = append(out, it)
		}
	}
	return out
}
