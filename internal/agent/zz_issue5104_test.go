package agent

import "testing"

// #5104: the three spec-gaming faces share ONE boundary scanner
// (quotedSegmentEnd/skipDollarParen) but keep face-local policies. This
// pin freezes them so a future "unification" cannot silently flatten:
//   - read-only face (#5094): operators inside $( ) are REAL - detected;
//   - report-verb face (#5102 + #5105 residual): quoted literal text is
//     blanked, but $() substitution spans stay VERBATIM (executed
//     content - markers produced by a substitution cannot hide in the
//     filler), so a marker inside such a span still fires.
func TestIssue5104DollarParenPolicyFork(t *testing.T) {
	// #5105 semantics: the quoted literal text around the substitution is
	// blanked, but the $(grep @skip f) span is copied verbatim - the
	// marker inside it is visible to the substring checks, so the command
	// fires (write-shaped content must not hide behind the filler).
	reportBody := `gh issue create --body "pipeline $(grep @skip f | wc -l) noted"`
	if !hasSkipMarkersInCommands([]string{reportBody}) {
		t.Errorf("report-verb face must keep $( ) spans visible (marker inside fires): %q", reportBody)
	}

	// A substitution that produces NO marker stays exempt end-to-end.
	cleanBody := `gh issue create --body "pipeline $(grep hits f | wc -l) noted"`
	if hasSkipMarkersInCommands([]string{cleanBody}) {
		t.Errorf("report-verb face must exempt marker-free substitution body: %q", cleanBody)
	}

	// The read-only face treats substitution bodies as executed content:
	// operators inside them are detected.
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
