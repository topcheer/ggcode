package session

import (
	"sort"

	"github.com/topcheer/ggcode/internal/provider"
)

// UsageBySource attributes session token consumption to the consumer that
// incurred it (UsageEntry.Source: "agent", "strategist", "verify",
// "ratchet", "subagent", "compaction"). Every usage record already carries
// this tag at write time; until now nothing aggregated it, so /cost could
// answer "how much" but not "where did it go" - the unit-economics question
// of token-consumer attribution across the whole agent loop.
//
// Returns per-source totals sorted by token total (descending, with a
// deterministic name tie-break). Entries with an empty Source (legacy
// records written before source tagging) are grouped under "unattributed".
// Sources with zero total tokens are omitted.
func UsageBySource(ses *Session) []SourceUsage {
	if ses == nil {
		return nil
	}
	agg := map[string]provider.TokenUsage{}
	for _, e := range ses.UsageHistory {
		src := e.Source
		if src == "" {
			src = "unattributed"
		}
		agg[src] = agg[src].Add(e.Usage)
	}
	out := make([]SourceUsage, 0, len(agg))
	for src, u := range agg {
		if u.Total() == 0 {
			continue
		}
		out = append(out, SourceUsage{Source: src, Usage: u})
	}
	sort.Slice(out, func(i, j int) bool {
		ti, tj := out[i].Usage.Total(), out[j].Usage.Total()
		if ti != tj {
			return ti > tj
		}
		return out[i].Source < out[j].Source
	})
	return out
}

// SourceUsage is one consumer's share of session token spend.
type SourceUsage struct {
	Source string
	Usage  provider.TokenUsage
}
