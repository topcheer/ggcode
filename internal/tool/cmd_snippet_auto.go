package tool

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// Usage-driven tool distillation (Self-Tooling / tool synthesis).
//
// Research basis:
//   - ATLASS / ToolMaker (Wölflein et al., "LLM Agents Making Agent Tools",
//     arXiv:2502.11705, ACL 2025): agents should build up a LIBRARY of tools
//     from actual usage, with closed-loop verification, instead of relying
//     only on tools hand-registered by developers.
//   - Self-Tooling Agent (OpenReview 2025): the agent should dynamically
//     arbitrate between invoking existing tools and synthesizing new
//     specialized ones; capabilities should GROW from repeated demand.
//   - Voyager skill library (Wang et al., 2023): successful, verified
//     execution programs are distilled into a retrievable skill library that
//     compounds across tasks.
//
// Gap this closes:
//   ggcode's cmd_snippet library is MANUAL-ONLY: an entry exists only when
//   the agent explicitly calls cmd_snippet(action="save"). In practice that
//   discipline is rare, so project-specific commands (build/test/deploy
//   invocations with non-obvious flags) are re-discovered from scratch in
//   every session. The Self-Tooling insight is that the library should grow
//   from USE: when the same command pattern succeeds repeatedly, that pattern
//   IS a tool waiting to be named.
//
// Mechanism (deterministic, zero-LLM-cost, additive to the existing store):
//   1. Every SUCCESSFUL run_command execution is observed.
//   2. Trivial / destructive / secret-bearing commands are skipped.
//   3. The command is normalized (quotes and digits collapsed) and matched
//      against existing snippets and pending observations by token-set
//      Jaccard similarity (>= autoSnippetMatchThreshold).
//   4. When a pending pattern reaches autoSnippetThreshold successful uses,
//      it is PROMOTED into a real snippet (source=auto, tags=[auto]) in the
//      same persistent store, and the run_command result is annotated so the
//      agent learns the retrieval name in-band.
//
// Pending observations live in the same .ggcode/cmd-snippets.json file
// (observations key), so accumulation is CROSS-SESSION. Old files without
// the key load unchanged.

const (
	// autoSnippetThreshold is how many similar successful runs are needed
	// before a pattern is promoted into the snippet library.
	autoSnippetThreshold = 3
	// autoSnippetMatchThreshold is the token-set Jaccard similarity at or
	// above which two commands are considered the same pattern. 0.6 is low
	// enough to merge variant invocations that differ only in a path
	// argument (e.g. "go test -tags goolm ./internal/agent" vs
	// "... ./internal/tool") yet high enough to keep distinct verbs apart
	// ("go test" vs "go vet" score 0.5).
	autoSnippetMatchThreshold = 0.6
	// autoSnippetMaxObservations bounds the pending set to keep the store
	// file small (LRU by last-seen time).
	autoSnippetMaxObservations = 40
	// autoSnippetMinTokens is the smallest command worth distilling.
	autoSnippetMinTokens = 3
	// autoSnippetSource is the Source marker on distilled entries.
	autoSnippetSource = "auto"
)

// cmdObservation is a pending, not-yet-promoted command pattern.
type cmdObservation struct {
	Pattern string    `json:"pattern"` // normalized form, for matching only
	Sample  string    `json:"sample"`  // most recent concrete command
	Name    string    `json:"name"`    // candidate snippet name
	Count   int       `json:"count"`
	FirstAt time.Time `json:"first_at"`
	LastAt  time.Time `json:"last_at"`
}

// SnippetDistiller observes successful run_command executions and promotes
// recurring patterns into the CmdSnippetTool store. Nil fields disable it.
type SnippetDistiller struct {
	Store *CmdSnippetTool
}

// autoSnippetReQuote collapses quoted segments; autoSnippetReDigits collapses
// numeric runs (versions, issue numbers, timeouts) that make commands look
// more different than they are.
var (
	autoSnippetReQuote  = regexp.MustCompile(`"[^"]*"|'[^']*'`)
	autoSnippetReDigits = regexp.MustCompile(`[0-9]+`)
	autoSnippetReJunk   = regexp.MustCompile(`[^a-z0-9]+`)
)

// autoSnippetTrivialFirstTokens are one-off inspection commands never worth
// distilling.
var autoSnippetTrivialFirstTokens = map[string]bool{
	"ls": true, "cd": true, "pwd": true, "echo": true, "cat": true,
	"clear": true, "exit": true, "which": true, "man": true, "head": true,
	"tail": true, "wc": true, "date": true, "whoami": true, "env": true,
	"printenv": true, "true": true, "false": true, "history": true,
	"type": true, "alias": true, "set": true, "sleep": true,
}

// autoSnippetSkipSubstrings are lowercase markers of destructive or
// secret-bearing commands that must never be persisted for easy reuse.
var autoSnippetSkipSubstrings = []string{
	"rm -rf", "rm -fr", "git reset --hard", "git clean", "--force",
	"dd if=", "mkfs", "drop table", "truncate table", "shutdown", "reboot",
	"begin rsa private key", "begin private key", "begin openssh private key",
	"akia", "xoxb", "ghp_", "gho_", "sk-", "password=", "passwd=",
	"api_key=", "apikey=", "bearer ",
}

// autoSnippetEligible reports whether a successful command is worth tracking.
func autoSnippetEligible(cmd string) bool {
	trimmed := strings.TrimSpace(cmd)
	if len(trimmed) < 8 || len(trimmed) > cmdSnippetMaxCommand {
		return false
	}
	tokens := strings.Fields(trimmed)
	if len(tokens) < autoSnippetMinTokens {
		return false
	}
	first := strings.ToLower(strings.TrimLeft(tokens[0], "./"))
	if autoSnippetTrivialFirstTokens[first] {
		return false
	}
	lower := strings.ToLower(trimmed)
	for _, marker := range autoSnippetSkipSubstrings {
		if strings.Contains(lower, marker) {
			return false
		}
	}
	return true
}

// normalizeCommandPattern reduces a command to a comparable token string:
// quoted values and digit runs collapse, whitespace is normalized, case is
// lowered. Two commands representing the same USAGE PATTERN produce the same
// or near-identical patterns.
func normalizeCommandPattern(cmd string) string {
	s := autoSnippetReQuote.ReplaceAllString(cmd, `""`)
	s = autoSnippetReDigits.ReplaceAllString(s, "0")
	return strings.Join(strings.Fields(strings.ToLower(s)), " ")
}

// jaccardSimilarity is the token-set Jaccard coefficient of two normalized
// command patterns, in [0,1].
func tokenSetJaccard(a, b string) float64 {
	as := strings.Fields(a)
	bs := strings.Fields(b)
	if len(as) == 0 || len(bs) == 0 {
		return 0
	}
	aset := make(map[string]struct{}, len(as))
	for _, t := range as {
		aset[t] = struct{}{}
	}
	bset := make(map[string]struct{}, len(bs))
	for _, t := range bs {
		bset[t] = struct{}{}
	}
	inter := 0
	for t := range aset {
		if _, ok := bset[t]; ok {
			inter++
		}
	}
	return float64(inter) / float64(len(aset)+len(bset)-inter)
}

// deriveAutoSnippetName builds a candidate snippet name from the command's
// leading tokens, e.g. "go test -tags goolm ./..." -> "auto/go-test-tags-goolm".
func deriveAutoSnippetName(cmd string) string {
	tokens := strings.Fields(cmd)
	parts := make([]string, 0, 4)
	for _, tok := range tokens {
		if len(parts) == 4 {
			break
		}
		cleaned := autoSnippetReJunk.ReplaceAllString(strings.TrimLeft(tok, "-./"), "")
		if cleaned == "" {
			continue
		}
		parts = append(parts, cleaned)
	}
	if len(parts) == 0 {
		parts = []string{"command"}
	}
	name := strings.ToLower("auto/" + strings.Join(parts, "-"))
	const maxName = 60
	if len(name) > maxName {
		name = name[:maxName]
	}
	return name
}

// uniqueSnippetName returns name, or name-N for the first N that does not
// collide with an existing entry (case-insensitive).
func uniqueSnippetName(store *cmdSnippetStore, name string) string {
	taken := func(candidate string) bool {
		for i := range store.Entries {
			if strings.EqualFold(store.Entries[i].Name, candidate) {
				return true
			}
		}
		return false
	}
	if !taken(name) {
		return name
	}
	for i := 2; i < 100; i++ {
		candidate := fmt.Sprintf("%s-%d", name, i)
		if !taken(candidate) {
			return candidate
		}
	}
	return fmt.Sprintf("%s-%d", name, time.Now().UnixNano()%1000)
}

// Observe records one successful command execution. When the observation
// completes a recurring pattern (>= autoSnippetThreshold similar successful
// uses), the pattern is promoted into the snippet library and the new
// snippet name is returned so the caller can surface it in-band. All other
// outcomes return "".
func (d *SnippetDistiller) Observe(command string) string {
	if d == nil || d.Store == nil {
		return ""
	}
	command = strings.TrimSpace(command)
	if !autoSnippetEligible(command) {
		return ""
	}
	pattern := normalizeCommandPattern(command)
	if pattern == "" {
		return ""
	}
	promoted := ""
	err := d.Store.mutate(true, func(store *cmdSnippetStore) error {
		now := time.Now()

		// 1. Matches an existing snippet? Manual entries are already
		// curated - never duplicate them. Auto entries get their use
		// count reinforced, which keeps ranking honest.
		for i := range store.Entries {
			entryPattern := normalizeCommandPattern(store.Entries[i].Command)
			if tokenSetJaccard(entryPattern, pattern) >= autoSnippetMatchThreshold {
				if store.Entries[i].Source == autoSnippetSource {
					store.Entries[i].UseCount++
					store.Entries[i].UpdatedAt = now
				}
				return nil
			}
		}

		// 2. Matches a pending observation? Accumulate and maybe promote.
		for i := range store.Observations {
			obs := &store.Observations[i]
			if tokenSetJaccard(obs.Pattern, pattern) >= autoSnippetMatchThreshold {
				obs.Count++
				obs.LastAt = now
				if obs.Count >= autoSnippetThreshold {
					name := uniqueSnippetName(store, obs.Name)
					store.Entries = append(store.Entries, cmdSnippetEntry{
						Name:        name,
						Command:     obs.Sample,
						Description: fmt.Sprintf("auto-distilled from %d similar successful runs", obs.Count),
						Tags:        []string{"auto"},
						Source:      autoSnippetSource,
						UseCount:    obs.Count,
						CreatedAt:   now,
						UpdatedAt:   now,
					})
					store.Observations = append(store.Observations[:i], store.Observations[i+1:]...)
					promoted = name
				}
				return nil
			}
		}

		// 3. New pending observation.
		store.Observations = append(store.Observations, cmdObservation{
			Pattern: pattern,
			Sample:  command,
			Name:    deriveAutoSnippetName(command),
			Count:   1,
			FirstAt: now,
			LastAt:  now,
		})
		if len(store.Observations) > autoSnippetMaxObservations {
			oldest := 0
			for i := range store.Observations {
				if store.Observations[i].LastAt.Before(store.Observations[oldest].LastAt) {
					oldest = i
				}
			}
			store.Observations = append(store.Observations[:oldest], store.Observations[oldest+1:]...)
		}
		return nil
	})
	if err != nil {
		// Telemetry-only path: never fail the command because the
		// snippet store could not be written.
		return ""
	}
	return promoted
}
