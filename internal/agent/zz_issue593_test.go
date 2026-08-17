package agent

import (
	"strings"
	"testing"
)

// zz_issue593_test.go tests for GitHub issue #593 fixes
// These are probes (ver-83 6/6) for the six priority fixes

// TestIssue593_P1 tests that file content parameters don't trigger
// phantom verification detection (P1).
func TestIssue593_P1(t *testing.T) {
	s := newPhantomVerifyState()

	// P1: write_file content containing "go test conventions" should NOT
	// arm the test category because only the "command" parameter is checked.
	toolInput := `{"path":"/tmp/notes.md","content":"notes about go test conventions"}`
	s.recordToolCall("write_file", toolInput, false)

	if s.categoriesRun[phantomCatTest] {
		t.Error("P1 failed: write_file content with 'go test' should not arm test category")
	}

	// But a real run_command with "go test" SHOULD arm it.
	s.recordToolCall("run_command", `{"command":"go test ./..."}`, false)

	if !s.categoriesRun[phantomCatTest] {
		t.Error("P1 failed: run_command 'go test' should arm test category")
	}
}

// TestIssue593_P2 tests that phantomVerify.reset() is wired at user-turn boundaries (P2).
func TestIssue593_P2(t *testing.T) {
	s := newPhantomVerifyState()

	// Run a verification command
	s.recordToolCall("run_command", `{"command":"go test ./..."}`, false)

	if !s.categoriesRun[phantomCatTest] {
		t.Error("expected test category to be armed")
	}

	// Reset should clear all categories
	s.reset()

	if s.categoriesRun[phantomCatTest] {
		t.Error("P2 failed: reset() should clear test category")
	}

	// Reset should also clear warnings count
	s.warnings = 2
	s.reset()

	if s.warnings != 0 {
		t.Error("P2 failed: reset() should clear warnings count")
	}
}

// TestIssue593_P3 tests that failed verifications don't arm categories (P3).
func TestIssue593_P3(t *testing.T) {
	s := newPhantomVerifyState()

	// P3: a FAILED go test should NOT arm the test category
	s.recordToolCall("run_command", `{"command":"go test ./..."}`, true)

	if s.categoriesRun[phantomCatTest] {
		t.Error("P3 failed: failed 'go test' should not arm test category")
	}

	// A successful go test SHOULD arm it
	s.recordToolCall("run_command", `{"command":"go test ./..."}`, false)

	if !s.categoriesRun[phantomCatTest] {
		t.Error("P3 failed: successful 'go test' should arm test category")
	}

	// P3: ci_status should be recognized as verification
	s.recordToolCall("ci_status", `{"action":"status"}`, false)

	if !s.categoriesRun[phantomCatCI] {
		t.Error("P3 failed: ci_status should arm CI category")
	}
}

// TestIssue593_P4 tests that multi-word verification phrases only match
// at command position, not anywhere in the string (P4).
func TestIssue593_P4(t *testing.T) {
	// P4: `grep -n "go test" Makefile` should NOT count as verification
	// because "go test" is a quoted argument, not at command position.
	if psIsVerifyCommand(`grep -n "go test" Makefile`) {
		t.Error("P4 failed: grep with quoted 'go test' should not count as verification")
	}

	// But `go test ./...` at command position SHOULD count.
	if !psIsVerifyCommand(`go test ./...`) {
		t.Error("P4 failed: 'go test' at command position should count")
	}

	// `make test` should count (whitelisted target)
	if !psIsVerifyCommand(`make test`) {
		t.Error("P4 failed: 'make test' should count")
	}

	// `make clean` should NOT count (not whitelisted)
	if psIsVerifyCommand(`make clean`) {
		t.Error("P4 failed: 'make clean' should not count")
	}
}

// TestIssue593_P5 tests that cross-tool reruns clear errors by category (P5).
func TestIssue593_P5(t *testing.T) {
	f := newFalsePremiseState()

	// P5: run_command "go build" fails
	f.recordToolResult("run_command", "build error: undefined symbol", true)

	if len(f.recentErrors) != 1 {
		t.Fatal("expected 1 error after failure")
	}

	// Fix and re-run with start_command + wait_command (different tool but same category)
	// Empty output for build should succeed and clear the error
	f.recordToolResult("start_command", "", false)

	// The error should be cleared because both are build/test tools (same category)
	if len(f.recentErrors) != 0 {
		t.Error("P5 failed: cross-tool build success should clear build error")
	}
}

// TestIssue593_P6 tests that empty output counts as success for build commands (P6).
func TestIssue593_P6(t *testing.T) {
	// P6: build commands (without "test") that succeed with empty output
	// should count as success.

	// Test 1: empty output with isBuildOnly=true should match
	if !matchesBuildSuccessClaim("", true) {
		t.Error("P6 failed: empty output with isBuildOnly=true should match")
	}

	// Test 2: empty output with isBuildOnly=false should NOT match (test commands need PASS/ok)
	if matchesBuildSuccessClaim("", false) {
		t.Error("P6 failed: empty output with isBuildOnly=false should not match")
	}

	// Test 3: actual success text should match regardless of isBuildOnly
	if !matchesBuildSuccessClaim("build passed", true) {
		t.Error("P6 failed: 'build passed' should match with isBuildOnly=true")
	}
	if !matchesBuildSuccessClaim("build passed", false) {
		t.Error("P6 failed: 'build passed' should match with isBuildOnly=false")
	}

	// Test 4: test output should match with isBuildOnly=false
	if !matchesBuildSuccessClaim("PASS", false) {
		t.Error("P6 failed: 'PASS' should match with isBuildOnly=false")
	}
}

// TestIssue593_P7 tests that acknowledgesErrorRe has word boundaries (P7).
func TestIssue593_P7(t *testing.T) {
	// P7: bare "error" should NOT match identifiers like "handleError"
	lower := "call handleError function"

	if acknowledgesError(lower) {
		t.Error("P7 failed: 'handleError' identifier should not trigger acknowledgement")
	}

	// But actual error references should match
	lower = "there was an error"

	if !acknowledgesError(lower) {
		t.Error("P7 failed: actual 'there was an error' should trigger acknowledgement")
	}

	// errorGroup should also not match
	lower = "create an errorGroup"

	if acknowledgesError(lower) {
		t.Error("P7 failed: 'errorGroup' identifier should not trigger acknowledgement")
	}

	// But "error" as a standalone word should match
	lower = "an error occurred"

	if !acknowledgesError(lower) {
		t.Error("P7 failed: standalone 'error' should trigger acknowledgement")
	}
}

// TestIssue593_ExtractCommandArg tests the helper function for P1.
func TestIssue593_ExtractCommandArg(t *testing.T) {
	tests := []struct {
		name     string
		argsJSON string
		want     string
	}{
		{
			name:     "simple command",
			argsJSON: `{"command":"go build","description":"build project"}`,
			want:     "go build",
		},
		{
			name:     "command with spaces",
			argsJSON: `{"command":"go test ./... -v","description":"run tests"}`,
			want:     "go test ./... -v",
		},
		{
			name:     "no command field",
			argsJSON: `{"path":"/tmp/file.txt","content":"test"}`,
			want:     "",
		},
		{
			name:     "malformed JSON",
			argsJSON: `{not valid json}`,
			want:     "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractCommandArg(tt.argsJSON)
			if got != tt.want {
				t.Errorf("extractCommandArg() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestIssue593_IndexOfToken tests the helper function for P4.
func TestIssue593_IndexOfToken(t *testing.T) {
	tokens := []string{"go", "test", "./...", "-v"}

	tests := []struct {
		name  string
		token string
		want  int
	}{
		{"found first", "go", 0},
		{"found middle", "test", 1},
		{"found last", "-v", 3},
		{"not found", "cargo", -1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := indexOfToken(tokens, tt.token)
			if got != tt.want {
				t.Errorf("indexOfToken() = %d, want %d", got, tt.want)
			}
		})
	}
}

// TestIssue593_PsCommandPositionTokens tests the command position logic for P4.
func TestIssue593_PsCommandPositionTokens(t *testing.T) {
	tests := []struct {
		name     string
		cmd      string
		wantCmds []string
	}{
		{
			name:     "simple command",
			cmd:      "go test ./...",
			wantCmds: []string{"go", "test"},
		},
		{
			name:     "grep with quoted test",
			cmd:      "grep -n \"go test\" Makefile",
			wantCmds: []string{"grep"},
		},
		{
			name:     "pipeline",
			cmd:      "go build | go test",
			wantCmds: []string{"go", "build", "go", "test"},
		},
		{
			name:     "cargo test",
			cmd:      "cargo test",
			wantCmds: []string{"cargo", "test"},
		},
		{
			name:     "python -m pytest",
			cmd:      "python -m pytest",
			wantCmds: []string{"python", "pytest"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tokens := strings.Fields(tt.cmd)
			cmdPosTokens := psCommandPositionTokens(tokens)

			for _, wantCmd := range tt.wantCmds {
				if !cmdPosTokens[wantCmd] {
					t.Errorf("expected command position token %q not found", wantCmd)
				}
			}
		})
	}
}
