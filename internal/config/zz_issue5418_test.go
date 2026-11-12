package config

import "testing"

// TestIssue5418CommandAndTimeoutPrefix pins the #5418 residual fixes: the
// `command` wrapper joins the skip-list, and `timeout` consumes its duration
// argument (bare and suffixed forms) so the unit heads with the real gh
// invocation. Flag-led timeout and sudo-with-argument stay conservative
// (known-limit class shared with agent-side #5342, pinned here to prevent
// re-discovery).
func TestIssue5418CommandAndTimeoutPrefix(t *testing.T) {
	def := DefaultMergeGateCommands
	cases := []struct {
		cmd     string
		wantPR  int
		wantHit bool
	}{
		// #5418 QA probe table residuals -> gated
		{"command gh pr merge 5356", 5356, true},
		{"timeout 30 gh pr merge 5356", 5356, true},
		// duration suffixes (QA hardening note 1: 30s/2m/1h must not re-open the hole)
		{"timeout 30s gh pr merge 5356", 5356, true},
		{"timeout 2m gh pr merge 5356", 5356, true},
		{"timeout 1h gh pr close 42", 42, true},
		{"timeout 1.5s gh pr merge 5356", 5356, true},
		// wrappers compose
		{"sudo timeout 30 gh pr merge 5356", 5356, true},
		{"command sudo gh pr merge 5356", 5356, true},
		{"env T=1 timeout 30 gh pr merge 5356", 5356, true},
		// conservative side: flag-led timeout stops the strip (known-limit)
		{"timeout -k 5 30 gh pr merge 5356", 0, false},
		{"timeout --foreground 30 gh pr merge 5356", 0, false},
		// conservative side: command -v strips only the wrapper, stops at -v
		{"command -v gh pr merge 5356", 0, false},
		// conservative side: sudo-with-argument keeps -u (agent-side #5342 class)
		{"sudo -u x gh pr merge 5356", 0, false},
		// non-merge commands under every new shape stay ungated
		{"command go test ./...", 0, false},
		{"timeout 30 go test ./...", 0, false},
		{"timeout 30s make lint", 0, false},
		{"command echo gh pr merge 5356", 0, false},
	}
	for _, tc := range cases {
		pr, hit := MatchMergeGateCommand(tc.cmd, def)
		if hit != tc.wantHit || (hit && pr != tc.wantPR) {
			t.Errorf("MatchMergeGateCommand(%q) = %d,%v want %d,%v", tc.cmd, pr, hit, tc.wantPR, tc.wantHit)
		}
	}
}
