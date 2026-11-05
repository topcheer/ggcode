package agent

import "testing"

// #5093: carriesSearchDownstreamMutation used a raw ContainsAny("|>; \n")
// over the whole command string, so the regex alternation '|' inside a
// quoted search pattern was indistinguishable from a shell pipe and the
// read-only exemption for isReadOnlySearchCommand was voided - investigating
// "why is this test skipped" with rg '(@skip|xit)\(' was flagged as adding
// skip markers.
func TestIssue5093QuotedRegexNotFlaggedAsPipeline(t *testing.T) {
	cases := []struct {
		name string
		cmd  string
	}{
		{"single-quoted alternation", `rg '(@skip|xit)\(' internal/foo/`},
		{"grep -E escaped alternation", `grep -E 't\.Skip\(|@pytest\.mark\.skip' x_test.go`},
		{"double-quoted pipe in pattern", `grep "a|b" file.txt`},
		{"git grep quoted (control, was already fine)", `git grep "xit(" -- src/`},
		{"backslash-escaped pipe outside quotes", `grep foo\|bar file.txt`},
		{"plain search no operators (control)", `rg Skip internal/foo/`},
		{"redirect marker inside single quotes", `grep 'a>b' file.txt`},
		{"semicolon inside quotes", `grep 'a;b' file.txt`},
		{"ampersand inside quotes", `grep 'a&&b' file.txt`},
	}
	for _, tc := range cases {
		if !isReadOnlySearchCommand(tc.cmd) {
			t.Errorf("%s: isReadOnlySearchCommand(%q) = false, want true (quoted operators are literals)", tc.name, tc.cmd)
		}
	}
}

func TestIssue5093RealOperatorsStillFlagged(t *testing.T) {
	cases := []struct {
		name string
		cmd  string
	}{
		{"pipe to tee", `grep foo file.txt | tee out.txt`},
		{"redirect", `grep foo file.txt > out.txt`},
		{"semicolon chain", `grep foo file.txt; rm -rf internal/foo/`},
		{"double ampersand chain", `grep foo file.txt && rm -rf internal/foo/`},
		{"newline separator", "grep foo file.txt\nrm -rf internal/foo/"},
		{"unquoted pipe between commands", `grep foo file.txt|wc -l`},
		{"command substitution inside double quotes runs", `grep "$(cat x | wc -l)" file.txt`},
		{"find -exec unaffected", `find . -name '*.go' -exec rm {} \;`},
	}
	for _, tc := range cases {
		if isReadOnlySearchCommand(tc.cmd) {
			t.Errorf("%s: isReadOnlySearchCommand(%q) = true, want false (real shell operator downstream)", tc.name, tc.cmd)
		}
	}
}

// Edge: unmatched quotes and lone escapes must not panic or over-trigger.
func TestIssue5093ScannerEdgeCases(t *testing.T) {
	cases := []struct {
		name    string
		cmd     string
		wantOps bool
	}{
		{"unterminated single quote", `grep 'a|b file.txt`, false},
		{"unterminated double quote", `grep "a|b file.txt`, false},
		{"trailing backslash", `grep foo\`, false},
		{"quote splice pipe outside", `grep 'a'\''b' file.txt | wc`, true},
		{"lone ampersand is not &&", `grep foo file.txt &`, false},
	}
	for _, tc := range cases {
		if got := carriesShellOperators(tc.cmd); got != tc.wantOps {
			t.Errorf("%s: carriesShellOperators(%q) = %v, want %v", tc.name, tc.cmd, got, tc.wantOps)
		}
	}
}
