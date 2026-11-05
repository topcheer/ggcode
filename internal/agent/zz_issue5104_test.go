package agent

import "testing"

// #5104: the three spec-gaming faces share ONE boundary scanner
// (quotedSegmentEnd/skipDollarParen) but keep DELIBERATELY OPPOSITE
// $() policies. This pin freezes the fork so a future "unification"
// cannot silently flatten it:
//   - read-only face (#5094): operators inside $( ) are REAL - detected;
//   - report-verb face (#5102/#5103): the whole quoted segment incl.
//     $( ) output is a reference (sent to an API) - exempt.
func TestIssue5104DollarParenPolicyFork(t *testing.T) {
	// Same substitution body, opposite verdicts by face.
	reportBody := `gh issue create --body "pipeline $(grep @skip f | wc -l) noted"`
	if hasSkipMarkersInCommands([]string{reportBody}) {
		t.Errorf("report-verb face must exempt quoted segment incl. $( ) output: %q", reportBody)
	}

	// The read-only face treats the SAME $( ) body as executed content:
	// simulate by running carriesShellOperators on a command whose
	// double-quoted segment contains a substitution pipeline.
	readOnly := `grep "note $(cat x | tee y)" file.txt`
	if !carriesShellOperators(readOnly) {
		t.Errorf("read-only face must detect operators inside $( ): %q", readOnly)
	}

	// Boundary agreement: both primitives agree on where the $( ) body
	// ends even when it contains nested parens and quotes. The '(' to
	// skip from is located by search, not a hand-counted index.
	cmd := `x "$(a "$(b)" c)"`
	open := -1
	for k := 0; k+1 < len(cmd); k++ {
		if cmd[k] == '$' && cmd[k+1] == '(' {
			open = k + 1
			break
		}
	}
	want := skipDollarParen(cmd, open)
	if got := indexAfterNestedSubstitution(cmd, open); got != want {
		t.Fatalf("nested $( ) extent mismatch: skipDollarParen=%d other=%d (open=%d)", want, got, open)
	}
}

// indexAfterNestedSubstitution is an independent re-derivation of the
// extent (depth counting from the '(' at idx) used as a cross-check.
func indexAfterNestedSubstitution(cmd string, open int) int {
	depth := 0
	for k := open; k < len(cmd); k++ {
		if cmd[k] == '(' {
			depth++
		} else if cmd[k] == ')' {
			depth--
			if depth == 0 {
				return k
			}
		}
	}
	return len(cmd)
}
