package agent

// zz_issue4605_test.go pins the #4605 fix: MatchingRulesForResult must not
// gate pattern matching behind a hardcoded list of English error-marker
// substrings. The learning path (ratchet LLM prompt) generates match_pattern
// for ANY error output shape, so reactive injection must try every pattern
// regardless of markers. Each case below is a real marker-free error shape
// that the old gate silently dropped.

import "testing"

func TestIssue4605_MarkerFreeErrorShapesStillMatch(t *testing.T) {
	cases := []struct {
		name    string
		pattern string
		result  string
	}{
		{"signal killed", `signal: killed`, "go test ./...: signal: killed"},
		{"no such file", `no such file or directory`, "cat: /tmp/missing.conf: no such file or directory"},
		{"exit status", `exit status \d+`, "go build: exit status 137"},
		{"deadline exceeded", `context deadline exceeded`, "probe: context deadline exceeded"},
		{"unexpected EOF", `unexpected EOF`, "read: unexpected EOF"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			rs := NewRuleStore(dir)
			rs.AddRule(Rule{
				Category:     "build",
				Rule:         "narrow parallelism and retry",
				MatchPattern: tc.pattern,
				FixHint:      "GOFLAGS=-p=1 retry",
			})
			matched := rs.MatchingRulesForResult(tc.result, "")
			if len(matched) != 1 {
				t.Fatalf("expected 1 reactive match for marker-free error %q, got %d", tc.result, len(matched))
			}
			if matched[0].FixHint != "GOFLAGS=-p=1 retry" {
				t.Errorf("unexpected FixHint: %s", matched[0].FixHint)
			}
		})
	}
}

func TestIssue4605_NonMatchingSuccessStillQuiet(t *testing.T) {
	dir := t.TempDir()
	rs := NewRuleStore(dir)
	rs.AddRule(Rule{
		Category:     "build",
		Rule:         "use -tags goolm",
		MatchPattern: `signal: killed`,
		FixHint:      "retry with GOFLAGS=-p=1",
	})
	// Successful output must still not trigger injection.
	if got := rs.MatchingRulesForResult("ok  all tests passed", ""); len(got) != 0 {
		t.Fatalf("expected 0 matches for success output, got %d", len(got))
	}
}
