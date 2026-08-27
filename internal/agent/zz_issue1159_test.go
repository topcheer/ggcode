package agent

// Tests for issue #1159: phantom-verify command classification adopts the
// established disciplines from premature_success.go:
// 1) issue #350 - bare build-system invocations ("make", "make clean",
//    "npm run dev") must NOT arm verification categories; only whitelisted
//    verify targets do;
// 2) issue #553 - keyword matching is restricted to command position, so
//    echoed or grepped text containing "go test" must not arm anything.
// Guards for issues #1144 (JSON extraction) and #1150/#1151 (test implies
// compile/typecheck) live in zz_issue1144_test.go / zz_issue1150_1151_test.go
// and must keep passing.

import (
	"testing"
)

func phantomIssue1159Run(t *testing.T, tool string, input string) map[string]bool {
	t.Helper()
	st := newPhantomVerifyState()
	st.recordToolCall(tool, input, false)
	return st.categoriesRun
}

func TestPhantomIssue1159BareBuildSystemInvocationsArmNothing(t *testing.T) {
	cases := []string{
		"make",
		"make clean",
		"rm -rf /tmp/x && make",
		"git status; make; git log --oneline | head -5",
		"npm run dev",
		"npm install",
		"yarn start",
	}
	for _, cmd := range cases {
		cats := phantomIssue1159Run(t, "run_command", "command: "+cmd)
		if len(cats) != 0 {
			t.Errorf("command %q armed categories %v, want none", cmd, cats)
		}
	}
}

func TestPhantomIssue1159MakeVerifyTargetsStillArm(t *testing.T) {
	cases := []struct {
		cmd  string
		want string
	}{
		{"make test", phantomCatTest},
		{"gmake e2e", phantomCatTest},
		{"make build", phantomCatBuild},
		{"make lint", phantomCatLint},
		{"mingw32-make test", phantomCatTest},
	}
	for _, tc := range cases {
		cats := phantomIssue1159Run(t, "run_command", tc.cmd)
		if !cats[tc.want] {
			t.Errorf("%q must arm %q, got %v", tc.cmd, tc.want, cats)
		}
	}
}

func TestPhantomIssue1159NpmAndJvmTargetsFollowWhitelist(t *testing.T) {
	cases := []struct {
		cmd   string
		check func(map[string]bool) bool
	}{
		{"npm run test", func(c map[string]bool) bool { return c[phantomCatTest] }},
		{"pnpm run lint", func(c map[string]bool) bool { return c[phantomCatLint] }},
		{"npm install", func(c map[string]bool) bool { return len(c) == 0 }},
		{"cargo check --workspace", func(c map[string]bool) bool { return !c[phantomCatBuild] }},
		{"./gradlew assembleDebug", func(c map[string]bool) bool { return len(c) == 0 }},
		{"mvn clean package", func(c map[string]bool) bool { return len(c) == 0 }},
	}
	for _, tc := range cases {
		got := tc.check(phantomIssue1159Run(t, "run_command", tc.cmd))
		if !got {
			t.Errorf("classification wrong for %q", tc.cmd)
		}
	}
}

func TestPhantomIssue1159MidlineQuotedGoTestDoesNotArm(t *testing.T) {
	// Issue #553 discipline: only command-position occurrences count. This
	// line searches for the words instead of running tests.
	for _, cmd := range []string{
		`grep -rn "go test" .`,
		`echo 'remember to go test ./...' > NOTES.md`,
		`echo clang-tidy comes later`,
	} {
		cats := phantomIssue1159Run(t, "run_command", cmd)
		if len(cats) != 0 {
			t.Errorf("non-execution text %q armed %v, want none", cmd, cats)
		}
	}
}

func TestPhantomIssue1159RealVerificationsStillArm(t *testing.T) {
	type row struct {
		cmd  string
		want string
	}
	rows := []row{
		{"go build ./...", phantomCatBuild},
		{"cd /tmp && go vet ./...", phantomCatLint},
		{"go test ./internal/agent/ -count=1", phantomCatTest},
	}
	for _, r := range rows {
		cats := phantomIssue1159Run(t, "run_command", r.cmd)
		if !cats[r.want] {
			t.Errorf("command %q must arm %q, got %v", r.cmd, r.want, cats)
		}
	}
}

func TestPhantomIssue1159TestImpliesCompileAndTypecheck(t *testing.T) {
	// Issues #1150/#1151 behavior preserved: go test implies compile and
	// typecheck happened too.
	cats := phantomIssue1159Run(t, "run_command", "go test ./...")
	for _, c := range []string{phantomCatTest, phantomCatCompile, phantomCatTypecheck} {
		if !cats[c] {
			t.Errorf("go test must also arm %q, got %v", c, cats)
		}
	}
}

func TestPhantomIssue1159FailedCommandDoesNotArm(t *testing.T) {
	st := newPhantomVerifyState()
	st.recordToolCall("run_command", "go build ./...", true)
	if len(st.categoriesRun) != 0 {
		t.Errorf("failed verification must not arm categories, got %v", st.categoriesRun)
	}
}

func TestPhantomIssue1159NonCommandToolsUnaffected(t *testing.T) {
	// File-content tools must never classify their payloads (#593 P1).
	cats := phantomIssue1159Run(t, "write_file", "docs about how to go test things and make targets")
	if cats[phantomCatTest] || cats[phantomCatBuild] {
		t.Errorf("write_file payload leaked into classification: %v", cats)
	}
	// CI checks still count as verification via ci_status (#593 P3).
	cats = phantomIssue1159Run(t, "ci_status", "{}")
	if len(cats) != 0 && !cats[phantomCatCI] {
		t.Errorf("unexpected categories for ci_status: %v", cats)
	}
}
