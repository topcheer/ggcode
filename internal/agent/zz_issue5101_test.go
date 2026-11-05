package agent

import "testing"

// #5101: hasSkipMarkersInCommands substring-scans the raw command string,
// so markers merely QUOTED into report bodies (gh issue create --body
// "... @pytest.mark.skip ...") fire the spec-gaming warning - live misfires
// on the session that filed #5093/#5101 itself. Quoted segments are
// references, not additions; strip them before the checks unless they are
// redirect targets (the shell writes that text into a file) or $( )
// substitution bodies (executed content).
func TestIssue5101QuotedMarkerReferencesNotFlagged(t *testing.T) {
	cases := []string{
		`gh issue create --repo topcheer/ggcode --title "x" --body "the detector flags @pytest.mark.skip quoted text"`,
		`gh pr create --title "fix" --body "the test calls t.Skip( when empty"`,
		`gh issue comment 5101 --body "[done] no more it.skip( references here"`,
		// unterminated quote: the rest is literal -> no filtering target
		`gh issue create --body "quoted body mentions @pytest.mark.skip`,
	}
	for _, cmd := range cases {
		if hasSkipMarkersInCommands([]string{cmd}) {
			t.Errorf("hasSkipMarkersInCommands(%q) = true, want false (quoted reference)", cmd)
		}
	}
}

func TestIssue5101RealInjectionsStillFlagged(t *testing.T) {
	cases := []string{
		// redirect-target segments are write-shaped
		`echo '@pytest.mark.skip' > internal/foo/x_test.go`,
		`echo "@skip" >> b_test.go`,
		`printf '%s\n' 't.Skip(' > f.go`,
		// substitution bodies are executed content - stay visible
		`echo "$(printf '@skip')" > f.go`,
		// unquoted markers anywhere still fire
		`echo @skip`,
		`sed s/foo/@skip/ x_test.go`,
		// non-report verbs keep the legacy raw scan (write-shaped tools can
		// introduce real markers with quoted text via sed -i / echo | tee)
		`git commit -m "remove @skip from docs"`,
		`echo 'mentioning @skip in a report' | wc -l`,
		`sed -i 's/placeholder/@pytest.mark.skip/' x_test.go`,
	}
	for _, cmd := range cases {
		if !hasSkipMarkersInCommands([]string{cmd}) {
			t.Errorf("hasSkipMarkersInCommands(%q) = false, want true (write-shaped or unquoted marker)", cmd)
		}
	}
}

// stripQuotedReferences keeps alignment: the filler must preserve length so
// downstream position-sensitive logic is unaffected.
func TestIssue5101StripQuotedReferencesAlignment(t *testing.T) {
	cmd := `gh pr create --body "quoted @skip text" --title 't.Skip( ref' plain`
	got := stripQuotedReferences(cmd)
	if len(got) != len(cmd) {
		t.Fatalf("stripQuotedReferences length = %d, want %d (alignment)", len(got), len(cmd))
	}
	for _, quoted := range []string{"quoted @skip text", "t.Skip( ref"} {
		if contains := len(quoted); contains > 0 {
			// the quoted content itself must be gone
			idx := indexOfQuotedSpan(cmd, quoted)
			if idx >= 0 && got[idx:idx+len(quoted)] == quoted {
				t.Errorf("quoted span %q survived filtering", quoted)
			}
		}
	}
	if !containsVerbatim(got, "plain") {
		t.Errorf("outside-quote text %q lost", "plain")
	}
}

func indexOfQuotedSpan(cmd, span string) int {
	for i := 0; i+len(span) <= len(cmd); i++ {
		if cmd[i:i+len(span)] == span {
			return i
		}
	}
	return -1
}

func containsVerbatim(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}
