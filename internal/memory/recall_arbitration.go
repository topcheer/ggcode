package memory

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

const (
	trustBase            = 1.0
	trustPersistentBonus = 0.2
	trustEvolvingPenalty = 0.2
	trustAncientPenalty  = 0.3
	trustFreshBonus      = 0.1
	freshWindow          = 7 * 24 * time.Hour
)

const maxRecallConflicts = 5

type RecallConflict struct {
	Subject string

	WinnerKey   string
	WinnerValue string
	WinnerTrust float64

	LoserKey   string
	LoserValue string
	LoserTrust float64
}

type RecallArbitration struct {
	Conflicts []RecallConflict
}

func (ra RecallArbitration) HasConflicts() bool {
	return len(ra.Conflicts) > 0
}

const (
	trustOutcomeSuccessBonus  = 0.2
	trustOutcomeFailedPenalty = 0.4
)

const (
	recallConflictLossGap    = 0.2
	trustConflictPenaltyUnit = 0.1
	trustConflictPenaltyCap  = 3
)

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

	switch m.Outcome {
	case "success":
		score += trustOutcomeSuccessBonus
	case "failed":
		score -= trustOutcomeFailedPenalty
	}

	if m.Conflicts > 0 {
		score -= trustConflictPenaltyUnit * float64(min(m.Conflicts, trustConflictPenaltyCap))
	}
	return score
}

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

	seen := make(map[string]bool)
	for i := 0; i < len(scoredEntries); i++ {
		for j := i + 1; j < len(scoredEntries); j++ {
			a, b := scoredEntries[i], scoredEntries[j]

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
