package agent

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// This file implements the observability read-path for ggcode's harness
// self-evolution layer (r76-r83): the task playbook (playbook.json) and the
// learned ratchet rules (agent-rules.json). Both stores actively shape the
// system prompt and tool hints every session, but until now neither had a
// user-facing health view: /rules listed rule text only, and playbook entries
// (Uses/SuccessRate/AvgIter) were completely invisible.
//
// FormatPlaybookDigest is a pure renderer: it takes store snapshots and a
// reference time and emits a deterministic, width-agnostic report. The TUI
// /playbook panel displays it; tests exercise it without Bubble Tea.

// Snapshot returns a copy of the playbook entries. Read-only: callers may
// sort or mutate the result without affecting the store.
func (pb *Playbook) Snapshot() []PlaybookEntry {
	pb.mu.Lock()
	defer pb.mu.Unlock()
	pb.load()
	out := make([]PlaybookEntry, len(pb.entries))
	copy(out, pb.entries)
	return out
}

// staleRuleAge reports how stale a rule is relative to now, in days.
func staleRuleAge(lastSeen, now time.Time) float64 {
	return now.Sub(lastSeen).Hours() / 24
}

// humanizeAge renders a duration as a compact human string ("just now",
// "5h ago", "41d ago").
func humanizeAge(d time.Duration) string {
	switch {
	case d < time.Hour:
		return "just now"
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

// FormatPlaybookDigest renders the combined health digest for the playbook
// and the learned rules. It never mutates its inputs. Deterministic: equal
// snapshots always render byte-identical output.
func FormatPlaybookDigest(entries []PlaybookEntry, rules []Rule, now time.Time) string {
	var b strings.Builder
	b.WriteString(formatPlaybookSection(entries, now))
	b.WriteString("\n")
	b.WriteString(formatRulesSection(rules, now))
	return b.String()
}

func formatPlaybookSection(entries []PlaybookEntry, now time.Time) string {
	var b strings.Builder
	if len(entries) == 0 {
		b.WriteString("Task strategies: no playbook data yet — patterns appear here after successful runs.")
		return b.String()
	}

	sorted := make([]PlaybookEntry, len(entries))
	copy(sorted, entries)
	// Most-used patterns first; ties broken by recency.
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].Uses != sorted[j].Uses {
			return sorted[i].Uses > sorted[j].Uses
		}
		return sorted[i].LastSeen.After(sorted[j].LastSeen)
	})

	fmt.Fprintf(&b, "Task strategies: %d pattern(s) learned from previous runs\n", len(sorted))
	const maxShown = maxShownPlaybookEntries
	shown := sorted
	if len(shown) > maxShown {
		shown = shown[:maxShown]
	}
	for _, e := range shown {
		fmt.Fprintf(&b, "  %-10s %-22s uses=%-3d success=%3.0f%% iter=%.1f dur=%.0fs last=%s %s\n",
			e.TaskType, e.ToolSequence, e.Uses, e.SuccessRate*100, e.AvgIter, e.AvgDurationS,
			humanizeAge(now.Sub(e.LastSeen)), e.FileTypes)
	}
	if len(sorted) > maxShown {
		fmt.Fprintf(&b, "  … and %d more\n", len(sorted)-maxShown)
	}
	b.WriteString("  Higher uses + success = more reliable strategy hints injected for that task type.")
	return b.String()
}

const (
	// maxShownPlaybookEntries caps how many playbook rows render before "… and N more".
	maxShownPlaybookEntries = 12
	// maxShownStaleRules caps the stalest-rules preview list.
	maxShownStaleRules = 5
	// maxRuleDigestLen truncates rule text in the digest preview.
	maxRuleDigestLen = 90
)

func formatRulesSection(rules []Rule, now time.Time) string {
	var b strings.Builder
	if len(rules) == 0 {
		b.WriteString("Learned rules: none yet — rules are extracted from failed runs and injected to prevent repeats.")
		return b.String()
	}

	// Health histogram + staleness split.
	categories := map[string]int{}
	stale := []Rule{}
	for _, r := range rules {
		categories[r.Category]++
		if now.Sub(r.LastSeen) > staleRuleThreshold {
			stale = append(stale, r)
		}
	}
	fmt.Fprintf(&b, "Learned rules: %d/%d, %d stale (>30d without a hit)\n", len(rules), defaultMaxRules, len(stale))

	catOrder := []string{"build", "test", "git", "convention", "security"}
	var catParts []string
	for _, cat := range catOrder {
		if n := categories[cat]; n > 0 {
			catParts = append(catParts, fmt.Sprintf("%s=%d", cat, n))
		}
	}
	// Categories outside the canonical order (custom/normalized ones) last,
	// sorted for determinism.
	var extra []string
	for cat := range categories {
		if !containsAnyWord(cat, catOrder...) {
			extra = append(extra, cat)
		}
	}
	sort.Strings(extra)
	for _, cat := range extra {
		catParts = append(catParts, fmt.Sprintf("%s=%d", cat, categories[cat]))
	}
	if len(catParts) > 0 {
		fmt.Fprintf(&b, "  by category: %s\n", strings.Join(catParts, " "))
	}

	if len(stale) > 0 {
		// Stalest first; ties by hit count descending.
		sort.SliceStable(stale, func(i, j int) bool {
			if staleRuleAge(stale[i].LastSeen, now) != staleRuleAge(stale[j].LastSeen, now) {
				return staleRuleAge(stale[i].LastSeen, now) > staleRuleAge(stale[j].LastSeen, now)
			}
			return stale[i].HitCount > stale[j].HitCount
		})
		b.WriteString("  stalest rules:\n")
		const maxStale = maxShownStaleRules
		shown := stale
		if len(shown) > maxStale {
			shown = shown[:maxStale]
		}
		for _, r := range shown {
			fmt.Fprintf(&b, "    [%s] %s (hits=%d, last=%s)\n",
				r.Category, truncStr(r.Rule, maxRuleDigestLen), r.HitCount, humanizeAge(now.Sub(r.LastSeen)))
		}
		b.WriteString("  Stale rules stop matching but still occupy slots; they are evicted by the store's retention policy.")
	}
	return strings.TrimRight(b.String(), "\n")
}
