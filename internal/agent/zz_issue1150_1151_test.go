package agent

import (
	"strings"
	"testing"
)

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

func TestIssue1150_FailedTestCommandDoesNotArmCategories(t *testing.T) {
	s := newPhantomVerifyState()
	s.recordToolCall("run_command", "go test ./...", true)
	if len(s.categoriesRun) != 0 {
		t.Errorf("failed command must not arm any category, got %v", s.categoriesRun)
	}
}

func TestIssue1151_TwoBeyondTargetReadsExemptWithoutSearch(t *testing.T) {
	s := newPrematureCommitState()
	s.recordExploration("read_file", []string{"/proj/pkg/bar.go"})
	s.recordExploration("read_file", []string{"/proj/pkg/baz.go"})

	msg := s.checkFirstEdit([]string{"/proj/pkg/qux.go"})
	if msg != "" {
		t.Errorf("expected exemption when 2 non-target files were read, got: %s", msg)
	}
}

func TestIssue1151_MultiTargetWithSingleBeyondReadWarns(t *testing.T) {
	s := newPrematureCommitState()
	s.recordExploration("read_file", []string{"/proj/pkg/a.go"})
	s.recordExploration("read_file", []string{"/proj/pkg/b.go"})
	s.recordExploration("read_file", []string{"/proj/pkg/c.go"})

	msg := s.checkFirstEdit([]string{"/proj/pkg/a.go", "/proj/pkg/b.go"})
	if msg == "" {
		t.Fatal("expected warning when only 1 file beyond the edit targets was read")
	}
	if !strings.Contains(msg, "[premature-commitment]") {
		t.Errorf("unexpected warning text: %s", msg)
	}
}

func TestIssue1151_SingleBeyondTargetReadInsufficient(t *testing.T) {
	s := newPrematureCommitState()
	s.recordExploration("read_file", []string{"/proj/pkg/target.go"})
	s.recordExploration("read_file", []string{"/proj/pkg/other.go"})

	msg := s.checkFirstEdit([]string{"/proj/pkg/target.go"})
	if msg == "" {
		t.Fatal("expected warning when only 1 beyond-target file was read")
	}
}
