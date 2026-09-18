package memory

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// Recall-time conflict arbitration ("ghost context" guard).
//
// CheckContradiction only fires on WRITE: it compares a newly saved entry
// against the entries that exist at that moment. Contradictions still reach
// the prompt when two conflicting entries were written in sessions that
// never saw each other (offline value drift), or when a conflict predates
// the write guard. LoadForPrompt then injects both entries together with no
// warning - the model reads two incompatible facts and arbitrarily trusts
// one. Production reports call this failure mode "ghost context".
//
// Research basis (2026):
//   - "Ghost Context: How Contradictory Beliefs Break Long-Running Agent
//     Memory" - persistent stores accumulate contradictory facts that get
//     retrieved together without warning; arbitration must happen at
//     retrieval time.
//   - "Beyond Memory Majority: Latent-Source Reasoning for Multi-Agent
//     Memory Arbitration" (arXiv:2608.19701) - reliable arbitration weighs
//     effective source reliability rather than raw frequency or count.
//   - "From Agent Traces to Trust: Evidence Tracing and Execution
//     Provenance" (arXiv:2606.04990) - trust functions over provenance
//     signals as the layer between store and prompt.
//
// Like the write-path guard this is deterministic and zero-LLM-cost: claims
// are extracted with the same heuristics (extractClaims/claimsConflict) and
// verdicts come from a transparent trust score computed from metadata that
// is already on disk (category, modtime). No model calls, no embeddings.
//
// Trust signals used (per-entry, additive):
//   - persistent category: architecture decisions are written to be stable
//     and survive curation -> small bonus.
//   - evolving category: superseded-by-design, always kept only as the
//     latest revision -> small penalty.
//   - ancient (older than ancientThreshold): likely stale -> penalty.
//   - fresh (touched within the last week): recently verified by a session
//     -> small bonus.
//
// When two conflicting entries tie on trust, the newer modtime wins. Both
// sides are always kept on disk and in the prompt - arbitration annotates,
// it never deletes or hides.
const (
	trustBase            = 1.0
	trustPersistentBonus = 0.2
	trustEvolvingPenalty = 0.2
	trustAncientPenalty  = 0.3
	trustFreshBonus      = 0.1
	freshWindow          = 7 * 24 * time.Hour
)

// maxRecallConflicts caps annotations per recall so a pathological store
// cannot flood the system prompt with warnings.
const maxRecallConflicts = 5

// RecallConflict is one arbitration verdict between two inline entries that
// state incompatible values for the same subject.
type RecallConflict struct {
	// Subject is the shared claim subject (e.g. "build command").
	Subject string
	// WinnerKey / WinnerValue / WinnerTrust identify the higher-trust side.
	WinnerKey   string
	WinnerValue string
	WinnerTrust float64
	// LoserKey / LoserValue / LoserTrust identify the lower-trust side; only
	// the loser's injected content is annotated.
	LoserKey   string
	LoserValue string
	LoserTrust float64
}

// RecallArbitration holds the verdicts from one recall arbitration pass.
type RecallArbitration struct {
	Conflicts []RecallConflict
}

// HasConflicts reports whether any recall conflicts were detected.
func (ra RecallArbitration) HasConflicts() bool {
	return len(ra.Conflicts) > 0
}

// memoryTrustScore computes the provenance-inspired trust score for one
// memory entry from its curation metadata. Deterministic and cheap.
func memoryTrustScore(m MemoryMeta, now time.Time) float64 {
	score := trustBase
	switch m.Category {
	case CategoryPersistent:
		score += trustPersistentBonus
	case CategoryEvolving:
		score -= trustEvolvingPenalty
	}
	age := now.Sub(m.CreatedAt)
	if age > ancientThreshold {
		score -= trustAncientPenalty
	}
	if age >= 0 && age <= freshWindow {
		score += trustFreshBonus
	}
	return score
}

// ArbitrateInline scans the entries selected for prompt injection for
// pairwise claim conflicts and returns trust-weighted verdicts. now is
// passed in for testability; callers use time.Now().
func ArbitrateInline(entries []MemoryEntry, now time.Time) RecallArbitration {
	var arb RecallArbitration
	if len(entries) < 2 {
		return arb
	}

	type scoredEntry struct {
		entry MemoryEntry
		data  claimMap
		trust float64
	}
	scoredEntries := make([]scoredEntry, 0, len(entries))
	for _, e := range entries {
		claims := extractClaims(e.Content)
		if len(claims) == 0 {
			continue
		}
		scoredEntries = append(scoredEntries, scoredEntry{
			entry: e,
			data:  claims,
			trust: memoryTrustScore(e.Meta, now),
		})
	}

	seen := make(map[string]bool) // loserKey|subject -> already reported
	for i := 0; i < len(scoredEntries); i++ {
		for j := i + 1; j < len(scoredEntries); j++ {
			a, b := scoredEntries[i], scoredEntries[j]

			// Shared subjects, sorted for deterministic reporting order.
			var subjects []string
			for s := range a.data {
				if _, ok := b.data[s]; ok {
					subjects = append(subjects, s)
				}
			}
			sort.Strings(subjects)

			for _, s := range subjects {
				if !claimsConflict(a.data[s], b.data[s]) {
					continue
				}
				winner, loser := a, b
				// Higher trust wins; ties break to the newer modtime.
				if b.trust > a.trust || (b.trust == a.trust && b.entry.Meta.CreatedAt.After(a.entry.Meta.CreatedAt)) {
					winner, loser = b, a
				}
				id := loser.entry.Key + "|" + s
				if seen[id] {
					continue
				}
				seen[id] = true
				arb.Conflicts = append(arb.Conflicts, RecallConflict{
					Subject:     s,
					WinnerKey:   winner.entry.Key,
					WinnerValue: winner.data[s],
					WinnerTrust: winner.trust,
					LoserKey:    loser.entry.Key,
					LoserValue:  loser.data[s],
					LoserTrust:  loser.trust,
				})
				if len(arb.Conflicts) >= maxRecallConflicts {
					return arb
				}
			}
		}
	}
	return arb
}

// Annotate appends a compact conflict verdict to the content of every
// lower-trust entry so the injected prompt flags the dispute and names the
// preferred source. Entries are mutated in place; winners stay clean.
func (ra RecallArbitration) Annotate(entries []MemoryEntry) {
	if !ra.HasConflicts() {
		return
	}
	for idx := range entries {
		var notes []string
		for _, c := range ra.Conflicts {
			if c.LoserKey != entries[idx].Key {
				continue
			}
			notes = append(notes, fmt.Sprintf("on %q prefer %q (trust %.2f vs %.2f; here %q vs there %q)",
				truncate(c.Subject, 40), c.WinnerKey, c.WinnerTrust, c.LoserTrust,
				truncate(c.LoserValue, 50), truncate(c.WinnerValue, 50)))
		}
		if len(notes) == 0 {
			continue
		}
		entries[idx].Content += "\n> [memory-conflict] This entry conflicts with other active memories: " +
			strings.Join(notes, "; ") +
			". Verify against the current workspace before trusting the values above."
	}
}
