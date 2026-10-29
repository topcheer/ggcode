package agent

// GEPA-style Policy Distillation (sa-149)
//
// Research basis:
//   - Agrawal et al. "GEPA: Reflective Prompt Evolution can Outperform
//     Reinforcement Learning." arXiv:2507.19457 (Jul 2025).
//     GEPA reflects on execution feedback and EDITS the prompt policy text
//     directly, beating DSPy MIPROv2 by up to 10% on SWE-bench and needing
//     31x fewer examples than RL approaches. The core claim: prose policy
//     compiled from measured outcomes outperforms raw feedback injection.
//   - Relaunched lineage: DSPy (Khattab et al.) / OPro (Yang et al. 2023)
//     optimize prompts from trajectory signals; GEPA generalizes to
//     free-form natural-language policy edits.
//
// Relationship to traj_intel.go (division of labor - do NOT merge):
//   - traj_intel (r457-462): extracts learnings, injects them, and keeps
//     causal counters (InjectedRuns/AfterSuccess/AfterFail) plus a holdout
//     control arm measuring what each injection actually did.
//   - policy_distill (this file): consumes ONLY the entries those counters
//     have PROVEN effective, and rewrites their statistical template prose
//     ("runs of type X took N iterations...") into imperative policy text
//     ("Batch reads for X before editing...") via one aux-tier LLM call.
//     Injection (RenderPromptSection) then prefers Refined over Insight.
//
// Gap closed: before this, even a learning measured effective 10/10 times
// was re-injected verbatim as the same template sentence it was extracted
// as - the learn -> inject loop never compiled into better policy TEXT.
// This is exactly the GEPA loop's "reflective prompt evolution" leg that
// statistical extractors lack.
//
// Safety/cost model:
//   - Throttled: at most one distillation batch per hour per workspace.
//   - Bounded: <= policyDistillBatchMax entries per batch, one LLM call,
//     15s timeout, silent fail-open (distillation is an upgrade, not a
//     dependency).
//   - Injection-hardened: refined text is sanitized (rune cap, line cap,
//     control-char strip, prompt-injection pattern blocklist) before it
//     may enter the system prompt; a rejected refinement leaves the entry
//     on its original Insight text.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/topcheer/ggcode/internal/provider"
)

const (
	// policyDistillMinInjections: an entry needs at least this many measured
	// injections before refinement is worth an LLM call (r461 counters).
	policyDistillMinInjections = 3

	// policyDistillMinSuccessRatio: refined entries must have succeeded in
	// at least this fraction of their measured injected runs.
	policyDistillMinSuccessRatio = 0.75

	// policyDistillBatchMax: upper bound of entries per distillation call.
	policyDistillBatchMax = 6

	// policyDistillThrottle: minimum wall-clock gap between distillation
	// batches for one workspace.
	policyDistillThrottle = time.Hour

	// policyDistillTimeout: hard timeout for the aux-tier LLM call.
	policyDistillTimeout = 15 * time.Second

	// policyDistillMaxRunes / policyDistillMaxLines: per-entry budget for
	// refined policy text.
	policyDistillMaxRunes = 280
	policyDistillMaxLines = 3

	// policyDistillStampFile: throttle marker inside .ggcode/.
	policyDistillStampFile = "policy-distill.stamp"
)

// distillChatFn abstracts the aux-tier call so the distillation core is
// unit-testable without a provider.
type distillChatFn func(ctx context.Context, system, user string) (string, error)

// matureForDistill reports whether r461 outcome counters have proven this
// entry effective enough to justify compiling it into policy text:
// measured >= policyDistillMinInjections injections, success ratio >=
// policyDistillMinSuccessRatio, and never refined before. Entries failing
// the effectiveness gate are never distilled (retired insights must not be
// polished into policy).
func matureForDistill(l trajectoryLearning) bool {
	if l.Refined != "" {
		return false
	}
	if l.InjectedRuns < policyDistillMinInjections {
		return false
	}
	total := l.AfterSuccess + l.AfterFail
	if total < policyDistillMinInjections {
		return false
	}
	if effectivenessGated(l) {
		return false
	}
	return float64(l.AfterSuccess)/float64(total) >= policyDistillMinSuccessRatio
}

// sanitizePolicyText enforces the injection-safety budget. It returns the
// cleaned single-paragraph text and whether the result is usable; a text
// that trips the injection blocklist is rejected outright (empty, false).
func sanitizePolicyText(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", false
	}
	// Strip control characters (keep whitespace) so refined text cannot
	// smuggle invisible steering into the system prompt.
	var b strings.Builder
	for _, r := range s {
		if r == '\n' || r == '\t' || unicode.IsPrint(r) {
			b.WriteRune(r)
		}
	}
	s = b.String()
	// Collapse to a bounded paragraph: at most N lines, each trimmed.
	lines := strings.Split(s, "\n")
	if len(lines) > policyDistillMaxLines {
		lines = lines[:policyDistillMaxLines]
	}
	for i := range lines {
		lines[i] = strings.TrimSpace(lines[i])
	}
	s = strings.TrimSpace(strings.Join(lines, "\n"))
	if s == "" {
		return "", false
	}
	s = truncateRunesUTF8(s, policyDistillMaxRunes)
	// Prompt-injection blocklist: refined text becomes system-prompt
	// content, so instructions about instructions are structurally
	// rejected rather than trusted.
	lower := strings.ToLower(s)
	for _, pat := range []string{
		"ignore previous", "ignore all previous", "disregard previous",
		"system prompt", "your instructions", "new instructions",
		"you are now", "act as", "jailbreak", "developer mode",
	} {
		if strings.Contains(lower, pat) {
			return "", false
		}
	}
	return s, true
}

// buildDistillUserPrompt renders the numbered candidate list the aux model
// compiles from. Deterministic input -> comparable output.
func buildDistillUserPrompt(entries []trajectoryLearning) string {
	var b strings.Builder
	b.WriteString("Rewrite each measured learning below as direct policy for a coding agent.\n")
	b.WriteString("Rules: at most 2 short imperative sentences per item; concrete and actionable; no meta-commentary; keep the original meaning.\n\n")
	for i, l := range entries {
		fmt.Fprintf(&b, "%d. [%s/%s] %s (measured: %d injections, %d succeeded)\n",
			i+1, l.Type, l.Category, l.Insight, l.InjectedRuns, l.AfterSuccess)
	}
	b.WriteString("\nAnswer with exactly one line per item, format: `N. policy text`. No other output.\n")
	return b.String()
}

const policyDistillSystem = "You compile measured trajectory feedback into prompt policy. You rewrite statistical observations as concise imperative guidance a coding agent can follow directly."

// parseDistillResponse extracts `N. text` lines keyed by entry number.
func parseDistillResponse(resp string) map[int]string {
	out := map[int]string{}
	for _, line := range strings.Split(resp, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// Tolerate markdown bullets before the number.
		line = strings.TrimPrefix(line, "-")
		line = strings.TrimSpace(line)
		var n int
		var text string
		if _, err := fmt.Sscanf(line, "%d.", &n); err != nil {
			continue
		}
		// Sscanf stops at the dot; take the remainder after it.
		if idx := strings.Index(line, "."); idx >= 0 {
			text = strings.TrimSpace(line[idx+1:])
		}
		if n < 1 || text == "" {
			continue
		}
		out[n] = text
	}
	return out
}

// runPolicyDistill is the pure core: distill entries via chat and return
// index -> sanitized refined text. Entries whose refinement fails sanity
// or injection checks are silently skipped (fallback stays Insight).
func runPolicyDistill(ctx context.Context, entries []trajectoryLearning, chat distillChatFn) map[int]string {
	result := map[int]string{}
	if len(entries) == 0 || chat == nil {
		return result
	}
	// Defensive cap (callers pre-trim; this guarantees the bound regardless).
	if len(entries) > policyDistillBatchMax {
		entries = entries[:policyDistillBatchMax]
	}
	resp, err := chat(ctx, policyDistillSystem, buildDistillUserPrompt(entries))
	if err != nil {
		return result
	}
	parsed := parseDistillResponse(resp)
	for n, text := range parsed {
		if n < 1 || n > len(entries) {
			continue
		}
		if clean, ok := sanitizePolicyText(text); ok {
			result[n-1] = clean
		}
	}
	return result
}

// maybeDistillPolicies is the Agent-side entry point, called from the
// post-run block after maybeExtractAndPersist. Two-phase locking: the LLM
// call happens WITHOUT holding the traj-intel lock, then the write-back
// re-locks and matches entries by dedupe key via rewriteAllLocked.
func (a *Agent) maybeDistillPolicies(workingDir string) {
	if workingDir == "" || a.trajIntel == nil {
		return
	}
	ggDir := filepath.Join(workingDir, ".ggcode")
	stampPath := filepath.Join(ggDir, policyDistillStampFile)
	if info, err := os.Stat(stampPath); err == nil && time.Since(info.ModTime()) < policyDistillThrottle {
		return
	}
	// Phase 1: collect mature candidates under lock, release before IO.
	a.trajIntel.mu.Lock()
	entries, err := a.trajIntel.loadFromFile()
	a.trajIntel.mu.Unlock()
	if err != nil {
		return
	}
	var candidates []trajectoryLearning
	var keys []string
	for _, l := range entries {
		if matureForDistill(l) {
			candidates = append(candidates, l)
			keys = append(keys, trajDedupeKey(l))
		}
	}
	if len(candidates) == 0 {
		return
	}
	if len(candidates) > policyDistillBatchMax {
		candidates = candidates[:policyDistillBatchMax]
		keys = keys[:policyDistillBatchMax]
	}
	// Phase 2: one throttled aux-tier LLM call, no lock held.
	chat := func(ctx context.Context, system, user string) (string, error) {
		if a.auxProviderFor() == nil {
			return "", fmt.Errorf("no aux provider")
		}
		resp, err := a.auxChat(ctx, []provider.Message{
			{Role: "system", Content: []provider.ContentBlock{{Type: "text", Text: system}}},
			{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: user}}},
		}, nil)
		if err != nil {
			return "", err
		}
		if resp == nil {
			return "", fmt.Errorf("empty aux response")
		}
		for _, blk := range resp.Message.Content {
			if blk.Type == "text" && strings.TrimSpace(blk.Text) != "" {
				return blk.Text, nil
			}
		}
		return "", fmt.Errorf("aux response had no text")
	}
	ctx, cancel := context.WithTimeout(context.Background(), policyDistillTimeout)
	defer cancel()
	refined := runPolicyDistill(ctx, candidates, chat)
	if len(refined) == 0 {
		// Still stamp: a failed call must not retry-loop every run.
		_ = os.MkdirAll(ggDir, 0o755)
		_ = os.WriteFile(stampPath, []byte(time.Now().UTC().Format(time.RFC3339)), 0o644)
		return
	}
	// Phase 3: write back under lock, matching by dedupe key.
	byKey := map[string]string{}
	for idx, text := range refined {
		if idx < len(keys) {
			byKey[keys[idx]] = text
		}
	}
	_ = a.trajIntel.rewriteAllLocked(func(existing []trajectoryLearning, loadErr error) ([]trajectoryLearning, error) {
		if loadErr != nil {
			return existing, loadErr
		}
		for i := range existing {
			if text, ok := byKey[trajDedupeKey(existing[i])]; ok {
				existing[i].Refined = text
			}
		}
		return existing, nil
	})
	_ = os.MkdirAll(ggDir, 0o755)
	_ = os.WriteFile(stampPath, []byte(time.Now().UTC().Format(time.RFC3339)), 0o644)
}
