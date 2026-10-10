package agent

// #3929: checkout -b/switch -c with a non-HEAD start-point rewrites tracked
// content (git branch <new> <start-point> + checkout semantics), so the #3864 B
// tree-preserving exemption must NOT apply. Both the command-line classifier
// and the git_checkout tool path must warn. The plain HEAD form (-b <new> with
// no positional start-point) keeps the exemption.

import "testing"

func TestIssue3929_CheckoutCreateWithStartPoint(t *testing.T) {
	cases := []struct {
		cmd  string
		want bool // want readOnly (exempt)?
	}{
		// HEAD forms: exemption holds (#3864 B preserved).
		{"git checkout -b fix", true},
		{"git checkout -B fix", true},
		{"git switch -c new", true},
		{"git switch -C new", true},
		{"git checkout -q -b fix", true}, // flags before -b, single positional
		// start-point forms: tracked content rewritten - must warn (#3929).
		{"git checkout -b fix origin/main", false},
		{"git checkout -B fix v1.2.3", false},
		{"git switch -c new HEAD~3", false},
		{"git switch -C new main^", false},
		{"git checkout origin/main -b fix", false}, // git accepts start-point before -b
		{"git checkout -b fix 1a2b3c4d", false},    // raw sha start-point
		// target checkout (no create flag): unchanged, stays mutating.
		{"git checkout main", false},
	}
	for _, tc := range cases {
		got, found := classifyGitCommandLine(tc.cmd)
		if !found {
			t.Errorf("%q: not classified as git checkout/switch", tc.cmd)
			continue
		}
		if got != tc.want {
			t.Errorf("%q: readOnly=%v, want %v", tc.cmd, got, tc.want)
		}
	}
}

func TestIssue3929_GitCheckoutToolStartPoint(t *testing.T) {
	cases := []struct {
		name     string
		argsJSON string
		want     bool // want readOnly (exempt)?
	}{
		// create from HEAD (no start_point): exemption holds.
		{"no_start_point", `{"branch":"fix","create":true}`, true},
		{"head_start_point", `{"branch":"fix","create":true,"start_point":"HEAD"}`, true},
		{"blank_start_point", `{"branch":"fix","create":true,"start_point":"  "}`, true},
		// explicit start_point: tracked content rewritten - must warn (#3929).
		{"origin_main", `{"branch":"fix","create":true,"start_point":"origin/main"}`, false},
		{"tag", `{"branch":"fix","create":true,"start_point":"v1.2.3"}`, false},
		{"revspec", `{"branch":"fix","create":true,"start_point":"HEAD~2"}`, false},
		// plain switch (create absent/false): unchanged, mutating.
		{"plain_switch", `{"branch":"main"}`, false},
	}
	for _, tc := range cases {
		if got := isReadOnlyGitInvocation("git_checkout", tc.argsJSON); got != tc.want {
			t.Errorf("%s: readOnly=%v, want %v", tc.name, got, tc.want)
		}
	}
}
