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

// #5101 review residual: wrapper CLIs (gh/hub/glab/tea/gitea) expose a raw
// API passthrough (gh api / glab api) that can commit quoted content into
// repo files via the contents/git-data endpoints - only report-shaped
// subcommands keep the exemption; the passthrough stays on the raw scan.
func TestIssue5101APIPassthroughNotExempt(t *testing.T) {
	cases := []string{
		// marker text visible unquoted-in-raw-scan position: the passthrough
		// commits it into the repo, so the raw scan must still fire
		`gh api repos/:owner/:repo/contents/path/x_test.go -f content='... @pytest.mark.skip ...'`,
		`gh api repos/topcheer/ggcode/git/commits -f message="add @pytest.mark.skip tests"`,
		`gh --repo topcheer/ggcode api repos/topcheer/ggcode/git/commits -f message="add @skip marker"`,
		`glab api projects/1/repository/files/x_test.go -f content='@pytest.mark.skip body'`,
		// graphQL passthrough is equally first-party write access
		`gh gql -f query='mutation { ... @pytest.mark.skip ... }'`,
	}
	for _, cmd := range cases {
		if !hasSkipMarkersInCommands([]string{cmd}) {
			t.Errorf("hasSkipMarkersInCommands(%q) = false, want true (api passthrough write channel)", cmd)
		}
	}
	// curl/wget to a webhook stay exempt per review: indirect write path
	exempt := []string{
		`curl -X POST -d '{"body": "mentions @pytest.mark.skip in text"}' https://example.com/hook`,
		`wget --post-data='@pytest.mark.skip discussion' https://example.com/hook`,
	}
	for _, cmd := range exempt {
		if hasSkipMarkersInCommands([]string{cmd}) {
			t.Errorf("hasSkipMarkersInCommands(%q) = true, want false (indirect webhook payload)", cmd)
		}
	}
}

// #5101 QA residual: $( ) substitution bodies inside double-quoted report
// bodies are EXECUTED content - the shell runs them, so a marker produced
// by the substitution must stay visible to the scan instead of hiding in
// the blanked filler (bypass regression found in the #5102 review).
func TestIssue5101SubstitutionBodiesVisible(t *testing.T) {
	cases := []string{
		// substitution that writes a marker into a test file via a report verb
		`gh issue create --title X --body "$(echo '@skip' > x_test.go)"`,
		// substitution emitting the marker into the body text
		`gh issue create --title X --body "value $(echo '@skip') end"`,
		// boundary-marker variant through the api passthrough
		`gh api repos/:owner/:repo/contents/x -f body="pre $(printf 'xit(') post"`,
		// marker inside the substitution, not in the literal text
		`gh pr comment 1 --body "note: $(printf 't.Skip(') in the log"`,
	}
	for _, cmd := range cases {
		if !hasSkipMarkersInCommands([]string{cmd}) {
			t.Errorf("hasSkipMarkersInCommands(%q) = false, want true (executed substitution body)", cmd)
		}
	}
	// literal references with harmless substitutions stay exempt
	exempt := []string{
		`gh issue create --body "discusses $(pwd) and mentions @pytest.mark.skip here"`,
		`gh issue comment 2 --body 'plain @skip reference, no substitution'`,
	}
	for _, cmd := range exempt {
		if hasSkipMarkersInCommands([]string{cmd}) {
			t.Errorf("hasSkipMarkersInCommands(%q) = true, want false (literal reference)", cmd)
		}
	}
}

// the filler must keep alignment even when substitution spans are copied
// verbatim, and malformed input (unterminated bodies) must not panic.
func TestIssue5101SubstitutionAlignment(t *testing.T) {
	cmds := []string{
		`gh issue create --body "value $(echo '@skip') end"`,
		`gh issue create --body "a $(b) c $(d) e"`,
		`gh issue create --body "unterminated $(tail`,
		`gh issue create --body "nested $(echo $(pwd)) ok"`,
	}
	for _, cmd := range cmds {
		got := stripQuotedReferences(cmd)
		if len(got) != len(cmd) {
			t.Errorf("stripQuotedReferences(%q) length = %d, want %d (alignment)", cmd, len(got), len(cmd))
		}
	}
	// the verbatim substitution text must survive the blanking
	got := stripQuotedReferences(`gh issue create --body "value $(echo '@skip') end"`)
	if !containsVerbatim(got, `$(echo '@skip')`) {
		t.Errorf("substitution span lost in filler: %q", got)
	}
	if containsVerbatim(got, "value") || containsVerbatim(got, "end") {
		t.Errorf("literal text not blanked: %q", got)
	}
}
