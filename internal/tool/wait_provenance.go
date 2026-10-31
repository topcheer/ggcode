package tool

import (
	"fmt"
	"sort"
	"strings"
)

// provenanceHeader renders the delegation-boundary provenance annotation
// (trust-cascade defense, EMNLP 2026 provenance-erasure finding: delegation
// strips provenance markers, so external content flows into the parent's
// context without attribution). Zero-inference: a deterministic aggregate of
// the sub-agent's external-source tool calls, prepended to its result.
// Empty when the sub-agent used no external sources - unchanged output for
// the common case.
func provenanceHeader(sources map[string]int) string {
	if len(sources) == 0 {
		return ""
	}
	names := make([]string, 0, len(sources))
	total := 0
	for name, n := range sources {
		names = append(names, fmt.Sprintf("%s x%d", name, n))
		total += n
	}
	sort.Strings(names)
	return fmt.Sprintf("[provenance: this result was produced with %d external-source call(s) - %s. Weigh external content accordingly.]\n\n",
		total, strings.Join(names, ", "))
}
