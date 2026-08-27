package agent

import (
	"strings"
	"testing"
)

// Guard tests for issues #1150 and #1151.

// #1150: test commands (go test / cargo test / pytest) imply compilation
// and type checking success, so running any of them must arm the compile
// and typecheck verification categories. Otherwise legitimate statements
// like "the code compiles cleanly" after a green go test get misflagged as
// phantom verifications.
func TestIssue1150_TestCommandsArmCompileAndTypecheck(t *testing.T) {
	cases := []struct {
		name string
		cmd  string
	}{
		{"go test", "go test ./internal/agent/"},
		{"cargo test", "cargo test --all"},
		{"pytest", "pytest -q tests/"},
	}
	text := "The code compiles cleanly and there are no type errors."
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newPhantomVerifyState()
			s.recordToolCall("run_command", tc.cmd, false)

			if !s.categoriesRun[phantomCatCompile] {
				t.Errorf("%q should arm %q category", tc.cmd, phantomCatCompile)
			}
			if !s.categoriesRun[phantomCatTypecheck] {
				t.Errorf("%q should arm %q category", tc.cmd, phantomCatTypecheck)
			}

			for _, c := range s.detectPhantomClaims(text) {
				if c.category == phantomCatCompile || c.category == phantomCatTypecheck {
					t.Errorf("after %q, %s claim should not be flagged: %q",
						tc.cmd, c.category, c.statement)
				}
			}
		})
	}
}

// #1150 negative control: without any verification command, compile and
// typecheck outcome claims must still be flagged.
func TestIssue1150_CompileTypecheckClaimsStillFlaggedWithoutCommand(t *testing.T) {
	s := newPhantomVerifyState()
	claims := s.detectPhantomClaims("The code compiles cleanly and there are no type errors.")

	cats := make(map[string]bool)
	for _, c := range claims {
		cats[c.category] = true
	}
	if !cats[phantomCatCompile] {
		t.Error("expected compile claim to be flagged without verification commands")
	}
	if !cats[phantomCatTypecheck] {
		t.Error("expected typecheck claim to be flagged without verification commands")
	}
}

// #1144 companion: failed verification runs must not arm categories either,
// keeping the #593/#350 semantics intact alongside the #1150 regex widening.
func TestIssue1150_FailedTestCommandDoesNotArmCategories(t *testing.T) {
	s := newPhantomVerifyState()
	s.recordToolCall("run_command", "go test ./...", true)
	if len(s.categoriesRun) != 0 {
		t.Errorf("failed command must not arm any category, got %v", s.categoriesRun)
	}
}

// #1151: reading exactly 2 files beyond the edit target (both non-target,
// no search performed) must exempt the first edit. The old condition
// required total reads >= 3 including edit targets, so this valid case
// warned spuriously.
func TestIssue1151_TwoBeyondTargetReadsExemptWithoutSearch(t *testing.T) {
	s := newPrematureCommitState()
	s.recordExploration("read_file", "/proj/pkg/bar.go")
	s.recordExploration("read_file", "/proj/pkg/baz.go")

	msg := s.checkFirstEdit([]string{"/proj/pkg/qux.go"})
	if msg != "" {
		t.Errorf("expected exemption when 2 non-target files were read, got: %s", msg)
	}
}

// #1151 second direction: with multiple edit targets, reading 3 files of
// which only 1 is beyond the targets must now warn. The old total-read
// cutoff (len(filesRead) >= 3) over-exempted this case because target reads
// inflated the count.
func TestIssue1151_MultiTargetWithSingleBeyondReadWarns(t *testing.T) {
	s := newPrematureCommitState()
	s.recordExploration("read_file", "/proj/pkg/a.go")
	s.recordExploration("read_file", "/proj/pkg/b.go")
	s.recordExploration("read_file", "/proj/pkg/c.go")

	msg := s.checkFirstEdit([]string{"/proj/pkg/a.go", "/proj/pkg/b.go"})
	if msg == "" {
		t.Fatal("expected warning when only 1 file beyond the edit targets was read")
	}
	if !strings.Contains(msg, "[premature-commitment]") {
		t.Errorf("unexpected warning text: %s", msg)
	}
}

// #1151 baseline: 1 beyond-target read was and remains insufficient.
func TestIssue1151_SingleBeyondTargetReadInsufficient(t *testing.T) {
	s := newPrematureCommitState()
	s.recordExploration("read_file", "/proj/pkg/target.go")
	s.recordExploration("read_file", "/proj/pkg/other.go")

	msg := s.checkFirstEdit([]string{"/proj/pkg/target.go"})
	if msg == "" {
		t.Fatal("expected warning when only 1 beyond-target file was read")
	}
}
