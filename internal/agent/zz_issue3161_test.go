package agent

import "testing"

// TestIssue3161_StashPushWithoutSeparator pins #3161 gap 1: git allows
// `git stash push <path>` without `--`; such a stash only reverts the named
// path and must NOT count as a bare-stash intervention when the suspect is
// a different file.
func TestIssue3161_StashPushWithoutSeparator(t *testing.T) {
	cases := []struct {
		name, cmd, suspect string
		want               bool
	}{
		{"stash unrelated file no --", "git stash push internal/other/fix.go", "internal/agent/a.go", false},
		{"stash suspect file no --", "git stash push internal/agent/a.go", "internal/agent/a.go", true},
		{"stash unrelated with -m msg", `git stash push -m "wip" internal/other/fix.go`, "internal/agent/a.go", false},
		{"stash suspect with -m msg", `git stash push -m "wip" internal/agent/a.go`, "internal/agent/a.go", true},
		{"bare stash still true", "git stash push", "internal/agent/a.go", true},
		{"bare implicit stash true", "git stash", "internal/agent/a.go", true},
		{"stash push flags only true", "git stash push -u -k", "internal/agent/a.go", true},
	}
	for _, tc := range cases {
		if got := isRevertIntervention("run_command", tc.cmd, tc.suspect); got != tc.want {
			t.Errorf("%s: isRevertIntervention(%q, %q) = %v, want %v", tc.name, tc.cmd, tc.suspect, got, tc.want)
		}
	}
}

// TestIssue3161_PathBoundaryMatch pins #3161 gap 2: suspect
// "util/files.go" must not match the unrelated longer path
// "cmd/util/files.go" in restore commands (plain substring did).
func TestIssue3161_PathBoundaryMatch(t *testing.T) {
	if isRevertIntervention("run_command", "git restore cmd/util/files.go", "util/files.go") {
		t.Error("restore of cmd/util/files.go must not hit suspect util/files.go (substring false positive, issue 3161)")
	}
	if !isRevertIntervention("run_command", "git restore util/files.go", "util/files.go") {
		t.Error("restore of the suspect itself must hit")
	}
	if !isRevertIntervention("run_command", "git restore ./util/files.go", "util/files.go") {
		t.Error("restore of ./-prefixed suspect path must hit (path-boundary suffix)")
	}
	if !isRevertIntervention("run_command", "git checkout -- internal/agent/a.go", "internal/agent/a.go") {
		t.Error("checkout -- of suspect must hit")
	}
	if isRevertIntervention("run_command", "git checkout -- internal/agent/other.go", "internal/agent/a.go") {
		t.Error("checkout -- of unrelated file must not hit")
	}
}

// TestIssue3161_StashManagementUnchanged keeps the pre-existing semantics:
// stash pop/apply/list/show/drop/clear are not interventions.
func TestIssue3161_StashManagementUnchanged(t *testing.T) {
	for _, drop := range []string{"pop", "apply", "list", "show", "drop", "clear"} {
		cmd := "git stash " + drop
		if isRevertIntervention("run_command", cmd, "internal/agent/a.go") {
			t.Errorf("%q must not count as intervention", cmd)
		}
	}
}
