//go:build goolm

package agent

import (
	"fmt"
	"strings"
	"sync"
)

const (
	maxAttemptBriefFire    = 2
	maxAttemptBriefEntries = 40
	briefMinFailures       = 3
	briefMinDistinctTools  = 2
	briefCooldownIter      = 4
)

type toolOutcome struct {
	tool     string
	target   string
	success  bool
	iter     int
	errLabel string
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
	s.lastFireIter = -briefCooldownIter
	s.runIter = 0
}

func (s *attemptBriefState) recordOutcome(toolName, target string, success bool, iter int, errSnippet string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if success {
		kept := s.entries[:0]
		for _, e := range s.entries {
			if !e.success && e.tool == toolName && e.target == truncateBrief(target, 60) {
				continue
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

	failures := make([]toolOutcome, 0, 10)
	for _, e := range s.entries {

		if !e.success && e.iter >= s.lastFireIter {
			failures = append(failures, e)
		}
	}
	if len(failures) < briefMinFailures {
		return ""
	}

	distinct := make(map[string]bool)
	for _, f := range failures {
		distinct[f.tool] = true
	}
	if len(distinct) < briefMinDistinctTools {
		return ""
	}

	s.firedCount++
	s.lastFireIter = iter

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
		if gIdx >= 6 {
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
