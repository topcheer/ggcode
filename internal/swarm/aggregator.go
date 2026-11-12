package swarm

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// AggregateResults folds N teammate outputs for one task into a single
// deterministic report (MoA-style aggregation, minus the LLM layer):
//
//  1. conflict detection  - same file path referenced by multiple outputs
//  2. fusion              - labeled concatenation, conflicts annotated
//  3. traceable report    - files/teammates/conflicts + adjudication flag
//
// Deliberately inform-only and LLM-free (P0): conflicts are flagged for the
// next adjudication layer, never silently merged.
var resultFilePathRe = regexp.MustCompile(`[\w./-]+\.(?:go|ts|tsx|js|py|md|json|yaml|yml|toml|rs|java|rb|c|cpp|h|sh)`)

type AggregateReport struct {
	Fused             string
	Files             []string            // sorted union of referenced files
	Conflicts         []AggregateConflict // same path, multiple teammates
	TeammateCount     int
	NeedsAdjudication bool
}

type AggregateConflict struct {
	Path      string
	Teammates []string
}

// AggregateResults builds the report from teammate ID -> result text.
func AggregateResults(results map[string]string) *AggregateReport {
	if len(results) == 0 {
		return nil
	}
	ids := make([]string, 0, len(results))
	for id := range results {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	filesUnion := map[string]bool{}
	fileOwners := map[string][]string{}
	var b strings.Builder
	fmt.Fprintf(&b, "## Aggregated Team Results (%d teammate(s))\n", len(ids))
	for _, id := range ids {
		text := strings.TrimSpace(results[id])
		fmt.Fprintf(&b, "\n### %s\n%s\n", id, text)
		seen := map[string]bool{}
		for _, m := range resultFilePathRe.FindAllString(text, -1) {
			p := m
			filesUnion[p] = true
			if !seen[p] {
				seen[p] = true
				fileOwners[p] = append(fileOwners[p], id)
			}
		}
	}

	rep := &AggregateReport{TeammateCount: len(ids)}
	for p := range filesUnion {
		rep.Files = append(rep.Files, p)
	}
	sort.Strings(rep.Files)
	for _, p := range rep.Files {
		if owners := fileOwners[p]; len(owners) > 1 {
			rep.Conflicts = append(rep.Conflicts, AggregateConflict{Path: p, Teammates: owners})
		}
	}
	rep.NeedsAdjudication = len(rep.Conflicts) > 0

	if rep.NeedsAdjudication {
		b.WriteString("\n## Conflicts (same file referenced by multiple outputs - adjudication required)\n")
		for _, c := range rep.Conflicts {
			fmt.Fprintf(&b, "- %s: %s\n", c.Path, strings.Join(c.Teammates, ", "))
		}
		b.WriteString("\nConflicting sections are labeled above; do NOT treat overlapping edits as merged.\n")
	} else {
		b.WriteString("\nNo file-level conflicts detected.\n")
	}
	rep.Fused = b.String()
	return rep
}
