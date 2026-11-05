package agent

import "testing"

// #5104: the three spec-gaming faces share ONE boundary scanner
// (quotedSegmentEnd/skipDollarParen) but keep DELIBERATELY OPPOSITE
// $() policies. This pin freezes the fork so a future "unification"
// cannot silently flatten it:
//   - read-only face (#5094): operators inside $( ) are REAL - detected;
//   - report-verb face (#5102/#5103/#5105): quoted literals are exempt,
//     but $( ) substitution bodies are executed content - markers and
//     operators inside them still fire.
func TestIssue5104DollarParenPolicyFork(t *testing.T) {
	// Report-verb face: marker inside a $( ) body stays visible after
	// blanking (post-#5105 semantics) - the substitution output can
	// inject a real skip marker into the payload.
	reportBody := `gh issue create --body "pipeline $(grep @skip f | wc -l) noted"`
	if !hasSkipMarkersInCommands([]string{reportBody}) {
		t.Errorf("report-verb face must fire on markers inside $( ) bodies: %q", reportBody)
	}

	// The read-only face treats the SAME $( ) body as executed content:
	// an operator inside the substitution means the command is not
	// purely read-only.
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

// TestIssue5104CrossFaceParityCorpus is the #5104 QA-verdict guard: one
// table of quoting shapes asserted through BOTH entry points
// (hasSkipMarkersInCommands for the report-verb face via a report-verb
// command wrapper, carriesShellOperators for the read-only face) so a
// future exemption face cannot silently diverge from the shared
// boundary scanner.
func TestIssue5104CrossFaceParityCorpus(t *testing.T) {
	rows := []struct {
		name       string
		quoted     string // the quoted argument shape under test
		reportFire bool   // report-verb face must fire on skip markers
		ops        bool   // read-only face must see an operator
	}{
		{"plain quoted marker", `"see @pytest.mark.skip docs"`, false, false},
		{"marker inside $() body", `"$(grep @skip f)"`, true, false},
		{"operator inside $() body", `"$(cat x | tee y)"`, false, true},
		// #5109 carry-over per watchdog adjudication: a marker-free
		// substitution body stays exempt on the report face even when it
		// contains operators - span visibility is not blanket firing.
		{"marker-free $() with operator", `"pipeline $(grep hits f | wc -l) noted"`, false, true},
		{"nested quotes in $()", `"$(echo "$(date)" y)"`, false, false},
		{"backslash-escaped operator", `"a\|b"`, false, false},
		{"unterminated quote", `"never closed @pytest.mark.skip`, false, false},
		{"single-quoted literal operator", `'a | b'`, false, false},
		{"$() inside single quotes", `'note $(grep @skip f)'`, false, false},
	}
	for _, r := range rows {
		report := `gh issue create --body ` + r.quoted
		if got := hasSkipMarkersInCommands([]string{report}); got != r.reportFire {
			t.Errorf("corpus[%s]: report-verb face fire=%v, want %v (%s)", r.name, got, r.reportFire, report)
		}
		if got := carriesShellOperators(r.quoted); got != r.ops {
			t.Errorf("corpus[%s]: read-only face ops=%v, want %v (%s)", r.name, got, r.ops, r.quoted)
		}
	}

	// Redirect target after a quoted segment restores write-shape on the
	// report-verb face and is an operator for the read-only face.
	redirect := `gh issue create --body 'see @pytest.mark.skip' > file.go`
	if !hasSkipMarkersInCommands([]string{redirect}) {
		t.Errorf("corpus[redirect]: report-verb face must fire on quoted text redirected into a file")
	}
	if !carriesShellOperators(redirect) {
		t.Errorf("corpus[redirect]: read-only face must detect the > redirect")
	}
}
