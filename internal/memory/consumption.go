package memory

// Consumption-signal machinery (sa-147, LIMBO: Lifelong Inference-Time
// Memory and Budget Optimization for LLM Agents, arXiv:2609.14138,
// ICTAI'26). LIMBO's core observation: injecting past experience into the
// prompt is not free - every injected byte competes with retrieval,
// reasoning, tool use, and verification for the same budget, and fixed
// injection policies keep paying full cost even when injections never pay
// off. Its non-RL kernel, adapted here: measure whether injected memories
// are actually CONSUMED (quoted by the model after injection), and let the
// store-wide Consumed/Uses ratio scale the inline budget between a floor
// and the ceiling. Fail-open by construction: until consumption evidence
// exists (minConsumedSamples), the budget stays at the full constant.

import (
	"os"
	"path/filepath"
	"strings"
)

const (
	// budgetFloorRatio is the lowest fraction of maxTotalInlineBytes a
	// low-consumption store converges to (6000B -> 1500B). A floor, not
	// zero: memory injection has baseline value the heuristic can't see
	// (behavioral influence without verbatim quotes).
	budgetFloorRatio = 0.25

	// minBudgetUses is the minimum recorded injection exposures before the
	// consume rate is statistically meaningful. Below it the rate is noise.
	minBudgetUses = 8

	// minConsumedSamples gates budget adaptation ON: with less consumption
	// evidence than this, the store is treated as unmeasured and keeps the
	// full budget (fail-open; also the legacy-sidecar default).
	minConsumedSamples = 2

	// fingerprintMinLen is the shortest first-line that counts as a
	// consumption fingerprint. Shorter lines are too generic to be evidence.
	fingerprintMinLen = 12

	// scanKeyMinLen is the shortest key that counts as a verbatim-cite
	// match. Short keys ("build") would false-positive inside any text.
	scanKeyMinLen = 6

	// scanFingerprintMax bounds per-call file reads for fingerprints: one
	// run-end scan touches at most this many entry files.
	scanFingerprintMax = 64
)

// containsStandaloneKey reports whether key appears in text with a token
// boundary on both sides: the runes immediately before and after (if any)
// must not be alphanumeric or '-', the characters that extend a memory key
// into a longer sibling key ("release-process" vs "release-process-impl").
func containsStandaloneKey(text, key string) bool {
	for from := 0; ; {
		i := strings.Index(text[from:], key)
		if i < 0 {
			return false
		}
		i += from
		if boundaryRune(text, i-1) && boundaryRune(text, i+len(key)) {
			return true
		}
		from = i + 1
	}
}

// boundaryRune reports whether the byte at index i is a token boundary
// (start/end of text, or anything that is not a key-extending character).
func boundaryRune(text string, i int) bool {
	if i < 0 || i >= len(text) {
		return true
	}
	c := text[i]
	switch {
	case c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '-', c == '_':
		return false
	}
	return true
}

// ScanConsumption scans one run's assistant text corpus for evidence that
// injected memories were actually consumed: either the entry KEY appears
// verbatim (agents cite keys like "release-always-read-memory-first"), or
// the entry's first content line (>= fingerprintMinLen) is quoted. Matches
// are persisted via RecordConsumption (debounced). Deterministic, zero LLM
// cost; intended call site is once per run end (tui reflection), not per
// turn. Returns the number of matched keys.
func (am *AutoMemory) ScanConsumption(text string) int {
	if am == nil || len(text) < fingerprintMinLen {
		return 0
	}
	lower := strings.ToLower(text)

	am.mu.Lock()
	idx := am.loadUsage()
	keys := make([]string, 0, len(idx.Entries))
	for key, rec := range idx.Entries {
		if rec != nil && rec.Uses > 0 {
			keys = append(keys, key)
		}
	}
	am.mu.Unlock()

	var matched []string
	fpReads := 0
	for _, key := range keys {
		// #3827: word-boundary match. Bare strings.Contains made
		// "release-process" hit inside "release-process-impl" - the longer
		// sibling key's citation inflated THIS entry's Consumed count.
		if len(key) >= scanKeyMinLen && containsStandaloneKey(lower, strings.ToLower(key)) {
			matched = append(matched, key)
			continue
		}
		if fpReads >= scanFingerprintMax {
			continue
		}
		fpReads++
		if fp := am.entryFingerprint(key); fp != "" && strings.Contains(lower, fp) {
			matched = append(matched, key)
		}
	}
	am.RecordConsumption(matched)
	return len(matched)
}

// entryFingerprint returns the lowercased first meaningful line of the
// entry's content (markdown heading markers trimmed), or "" if no line is
// at least fingerprintMinLen bytes.
func (am *AutoMemory) entryFingerprint(key string) string {
	data, err := os.ReadFile(filepath.Join(am.dir, key+".md"))
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.ToLower(strings.TrimSpace(line))
		line = strings.TrimLeft(line, "#> -")
		line = strings.TrimSpace(line)
		if len(line) >= fingerprintMinLen {
			return line
		}
	}
	return ""
}

// EffectiveInlineBudget returns the current total inline budget: the
// ceiling constant scaled by the store-wide consume rate
// (sum Consumed / sum Uses), clamped to [budgetFloorRatio, 1.0]. Fail-open:
// unmeasured stores (fewer than minConsumedSamples consumptions or
// minBudgetUses injections recorded) get the full constant, so behavior is
// byte-identical to the pre-adaptive era until evidence accumulates.
func (am *AutoMemory) EffectiveInlineBudget() int {
	if am == nil {
		return maxTotalInlineBytes
	}
	am.mu.Lock()
	defer am.mu.Unlock()
	idx := am.loadUsage()
	var uses, consumed int
	for _, rec := range idx.Entries {
		if rec == nil {
			continue
		}
		uses += rec.Uses
		consumed += rec.Consumed
	}
	if consumed < minConsumedSamples || uses < minBudgetUses {
		return maxTotalInlineBytes
	}
	rate := float64(consumed) / float64(uses)
	if rate < budgetFloorRatio {
		rate = budgetFloorRatio
	}
	// #3601: Consumed and Uses are recorded under independent debounce
	// keys and consumption can be counted per-injection while uses lag,
	// so the ratio can exceed 1.0 and the budget silently grew past
	// maxTotalInlineBytes - contradicting the documented clamp window.
	// Cap it at 1.0: the adaptive budget may shrink, never inflate past
	// the ceiling.
	if rate > 1.0 {
		rate = 1.0
	}
	return int(float64(maxTotalInlineBytes) * rate)
}
