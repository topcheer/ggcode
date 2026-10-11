package agent

// #3860 companions: commandMatches must preprocess compound/env-prefixed
// commands exactly like the verify_hint gate family (#2122/#3751), and
// infix *x* globs must be suffix-anchored instead of contains-anywhere.

import "testing"

func TestIssue3860_CompoundAndEnvPreprocessing(t *testing.T) {
	pats := []string{"go test*"}

	cases := []struct {
		cmd  string
		want bool
		why  string
	}{
		{"go test ./...", true, "plain"},
		{"cd internal/agent && go test ./...", true, "cd && compound (#3860 A)"},
		{"GOFLAGS=-p=1 go test ./...", true, "env prefix (#3860 A / #3751)"},
		{"GOFLAGS=-p=1 GOMEMLIMIT=2GiB go test ./...", true, "stacked env prefixes"},
		{"make lint && go test ./...", true, "trailing segment of compound"},
		{"gofmt -l .", false, "unrelated command"},
		{"cd internal/agent", false, "cd segment alone is not the verb"},
	}
	for _, c := range cases {
		if got := commandMatches(pats, c.cmd); got != c.want {
			t.Errorf("commandMatches(%q) = %v, want %v (%s)", c.cmd, got, c.want, c.why)
		}
	}
}

func TestIssue3860_InfixWordAnchored(t *testing.T) {
	pats := []string{"*push*"}

	if !commandMatches(pats, "git push") {
		t.Error("`git push` should match *push* (exact word)")
	}
	if !commandMatches(pats, "git push origin main") {
		t.Error("`git push origin main` is a real push: core is an exact word")
	}
	if commandMatches(pats, "echo push done") {
		t.Error("`echo push done` must NOT complete a *push* step (#3860 A benign verb)")
	}
	if commandMatches(pats, "pushy-comment") {
		t.Error("substring `push` inside `pushy-comment` must not match word-anchored infix glob")
	}
	// Legacy semantics preserved: `go test ./...` keeps matching `*test*`.
	if !commandMatches([]string{"*test*"}, "go test ./...") {
		t.Error("`go test ./...` must keep matching *test* (word anchor, verb=go)")
	}
}
