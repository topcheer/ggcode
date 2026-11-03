package agent

import (
	"strings"
	"testing"
)

func TestErrRegressionState_Reset(t *testing.T) {
	s := newErrRegressionState()
	s.lastErrorCount = 5
	s.hadEdits = true
	s.warningCount = 2
	s.reset()
	if s.lastErrorCount != -1 || s.hadEdits || s.warningCount != 0 {
		t.Fatalf("reset did not clear state: %+v", s)
	}
}

func TestErrRegressionState_NoPreviousBaseline(t *testing.T) {
	s := newErrRegressionState()

	got := s.recordVerify("go test", "error: foo", true)
	if got != "" {
		t.Fatalf("expected no guidance on first verify, got: %s", got)
	}
}

func TestErrRegressionState_NoEditsBetweenVerifies(t *testing.T) {
	s := newErrRegressionState()

	s.recordVerify("go test", buildOutput(5), true)

	got := s.recordVerify("go test", buildOutput(10), true)
	if got != "" {
		t.Fatalf("expected no guidance without edits, got: %s", got)
	}
}

func TestErrRegressionState_RegressionDetected(t *testing.T) {
	s := newErrRegressionState()
	s.recordVerify("go test", buildOutput(2), true)
	s.recordEdit()
	got := s.recordVerify("go test", buildOutput(8), true)
	if got == "" {
		t.Fatal("expected regression guidance, got empty string")
	}
	if !strings.Contains(got, "NEGATIVE PROGRESS") {
		t.Fatalf("expected NEGATIVE PROGRESS in guidance, got: %s", got)
	}
	if !strings.Contains(got, "2 to 8") {
		t.Fatalf("expected error counts in message, got: %s", got)
	}
}

func TestErrRegressionState_SmallIncreaseNoWarning(t *testing.T) {
	s := newErrRegressionState()
	s.recordVerify("go test", buildOutput(3), true)
	s.recordEdit()
	got := s.recordVerify("go test", buildOutput(5), true)
	if got != "" {
		t.Fatalf("expected no guidance for small increase, got: %s", got)
	}
}

func TestErrRegressionState_ErrorDecreasesNoWarning(t *testing.T) {
	s := newErrRegressionState()
	s.recordVerify("go test", buildOutput(10), true)
	s.recordEdit()
	got := s.recordVerify("go test", buildOutput(3), true)
	if got != "" {
		t.Fatalf("expected no guidance when errors decrease, got: %s", got)
	}
}

func TestErrRegressionState_SuccessNoWarning(t *testing.T) {
	s := newErrRegressionState()
	s.recordVerify("go test", buildOutput(5), true)
	s.recordEdit()
	got := s.recordVerify("go test", "", false)
	if got != "" {
		t.Fatalf("expected no guidance on success, got: %s", got)
	}
}

func TestErrRegressionState_MaxWarnings(t *testing.T) {
	s := newErrRegressionState()

	for i := 0; i < 3; i++ {
		s.recordVerify("go test", buildOutput(2), true)
		s.recordEdit()
		got := s.recordVerify("go test", buildOutput(10), true)
		if i < maxRegressionWarnings {
			if got == "" {
				t.Fatalf("expected guidance on call %d", i)
			}
		} else {
			if got != "" {
				t.Fatalf("expected no guidance after maxWarnings on call %d", i)
			}
		}
	}
}

func TestCountErrors(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  int
	}{
		{"empty", "", 0},
		{"no errors", "all good\nbuilding...\nok", 0},
		{"single error", "error: undefined variable x", 1},
		{"multiple errors", "error: foo\nerror: bar\nerror: baz", 3},
		{"mixed case", "Error: something\nERROR: other", 2},
		{"with file prefix", "main.go:10: error: syntax error", 1},
		{"filters zero errors", "0 errors found\nno error detected", 0},

		{"go compiler", "./main.go:5:2: undefined: foo\n./main.go:6:3: undefined: bar", 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := countVerifyErrors(tt.input)
			if got != tt.want {
				t.Errorf("countVerifyErrors(%q) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}

func TestErrorRegressionCheckCommand(t *testing.T) {
	a := &Agent{errRegression: newErrRegressionState()}

	if g := a.errorRegressionCheckCommand("read_file", nil, buildOutput(5), true); g != "" {
		t.Fatalf("expected empty for non-verify tool, got: %s", g)
	}

	if g := a.errorRegressionCheckCommand("run_command", []byte(`{"command":"ls -la"}`), buildOutput(5), true); g != "" {
		t.Fatalf("expected empty for non-verify command, got: %s", g)
	}
}

func buildOutput(n int) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		b.WriteString("error: something broke\n")
	}
	return b.String()
}

func TestErrorLinesReMatchesToolchainOutput(t *testing.T) {
	for _, line := range []string{
		"./internal/agent/foo.go:12:5: undefined: helpers.Render",
		"--- FAIL: TestAgentLoop (0.30s)",
		"error[E0308]: mismatched types",
		"panic: runtime error: index out of range",
		"E       AssertionError: expected 3, got 4",
	} {
		if !errorLinesRe.MatchString(line) {
			t.Errorf("errorLinesRe missed toolchain error line: %q", line)
		}
	}
	for _, line := range []string{
		"--- PASS: TestSomething (0.10s)",
		"ok  \texample.com/pkg\t0.5s",
		"all checks passed, no findings",
	} {
		if errorLinesRe.MatchString(line) {
			t.Errorf("errorLinesRe false-positive on: %q", line)
		}
	}
}

func TestVerifyCommandKindSemicolonPipeSeparated(t *testing.T) {

	if got := verifyCommandKind("cd /app; go test ./..."); got != "go test ./..." {
		t.Errorf("semicolon compound: got %q, want %q", got, "go test ./...")
	}
	if got := verifyCommandKind("cd /app | go test ./..."); got != "go test ./..." {
		t.Errorf("pipe compound: got %q, want %q", got, "go test ./...")
	}

	kb := verifyCommandKind("cd /x; go build ./...")
	kt := verifyCommandKind("cd /x; go test ./...")
	if kb == kt {
		t.Errorf("build and test behind semicolon compared as same kind: %q", kb)
	}

	if got := verifyCommandKind("GOFLAGS=-p=1 cd /app && go test ./..."); got != "go test ./..." {
		t.Errorf("legacy && compound: got %q", got)
	}
}

func TestVerifyCommandKindScopeAndVerbosity(t *testing.T) {

	focused := verifyCommandKind("go test ./internal/agent -run TestFoo")
	full := verifyCommandKind("go test ./...")
	if focused == full {
		t.Errorf("focused and full runs share kind %q - focus-to-full would false-fire", focused)
	}

	quiet := verifyCommandKind("go test ./...")
	verbose := verifyCommandKind("go test ./... -v")
	if quiet == verbose {
		t.Errorf("-v shares kind with quiet run: %q", quiet)
	}

	if a, b := verifyCommandKind("go test ./internal/im/"), verifyCommandKind("go test ./internal/im/"); a != b {
		t.Errorf("identical commands got different kinds: %q vs %q", a, b)
	}
}
