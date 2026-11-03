package agent

import (
	"strings"
	"testing"
)

func TestIssue593_P1(t *testing.T) {
	s := newPhantomVerifyState()

	toolInput := `{"path":"/tmp/notes.md","content":"notes about go test conventions"}`
	s.recordToolCall("write_file", toolInput, false)

	if s.categoriesRun[phantomCatTest] {
		t.Error("P1 failed: write_file content with 'go test' should not arm test category")
	}

	s.recordToolCall("run_command", `{"command":"go test ./..."}`, false)

	if !s.categoriesRun[phantomCatTest] {
		t.Error("P1 failed: run_command 'go test' should arm test category")
	}
}

func TestIssue593_P2(t *testing.T) {
	s := newPhantomVerifyState()

	s.recordToolCall("run_command", `{"command":"go test ./..."}`, false)

	if !s.categoriesRun[phantomCatTest] {
		t.Error("expected test category to be armed")
	}

	s.reset()

	if s.categoriesRun[phantomCatTest] {
		t.Error("P2 failed: reset() should clear test category")
	}

	s.warnings = 2
	s.reset()

	if s.warnings != 0 {
		t.Error("P2 failed: reset() should clear warnings count")
	}
}

func TestIssue593_P3(t *testing.T) {
	s := newPhantomVerifyState()

	s.recordToolCall("run_command", `{"command":"go test ./..."}`, true)

	if s.categoriesRun[phantomCatTest] {
		t.Error("P3 failed: failed 'go test' should not arm test category")
	}

	s.recordToolCall("run_command", `{"command":"go test ./..."}`, false)

	if !s.categoriesRun[phantomCatTest] {
		t.Error("P3 failed: successful 'go test' should arm test category")
	}

	s.recordToolCall("ci_status", `{"action":"status"}`, false)

	if !s.categoriesRun[phantomCatCI] {
		t.Error("P3 failed: ci_status should arm CI category")
	}
}

func TestIssue593_P4(t *testing.T) {

	if psIsVerifyCommand(`grep -n "go test" Makefile`) {
		t.Error("P4 failed: grep with quoted 'go test' should not count as verification")
	}

	if !psIsVerifyCommand(`go test ./...`) {
		t.Error("P4 failed: 'go test' at command position should count")
	}

	if !psIsVerifyCommand(`make test`) {
		t.Error("P4 failed: 'make test' should count")
	}

	if psIsVerifyCommand(`make clean`) {
		t.Error("P4 failed: 'make clean' should not count")
	}
}

func TestIssue593_P5(t *testing.T) {
	f := newFalsePremiseState()

	f.recordToolResult("run_command", "build error: undefined symbol", true)

	if len(f.recentErrors) != 1 {
		t.Fatal("expected 1 error after failure")
	}

	f.recordToolResult("start_command", "", false)

	if len(f.recentErrors) != 0 {
		t.Error("P5 failed: cross-tool build success should clear build error")
	}
}

func TestIssue593_P6(t *testing.T) {

	if !matchesBuildSuccessClaim("", true) {
		t.Error("P6 failed: empty output with isBuildOnly=true should match")
	}

	if matchesBuildSuccessClaim("", false) {
		t.Error("P6 failed: empty output with isBuildOnly=false should not match")
	}

	if !matchesBuildSuccessClaim("build passed", true) {
		t.Error("P6 failed: 'build passed' should match with isBuildOnly=true")
	}
	if !matchesBuildSuccessClaim("build passed", false) {
		t.Error("P6 failed: 'build passed' should match with isBuildOnly=false")
	}

	if !matchesBuildSuccessClaim("PASS", false) {
		t.Error("P6 failed: 'PASS' should match with isBuildOnly=false")
	}
}

func TestIssue593_P7(t *testing.T) {

	lower := "call handleError function"

	if acknowledgesError(lower) {
		t.Error("P7 failed: 'handleError' identifier should not trigger acknowledgement")
	}

	lower = "there was an error"

	if !acknowledgesError(lower) {
		t.Error("P7 failed: actual 'there was an error' should trigger acknowledgement")
	}

	lower = "create an errorGroup"

	if acknowledgesError(lower) {
		t.Error("P7 failed: 'errorGroup' identifier should not trigger acknowledgement")
	}

	lower = "an error occurred"

	if !acknowledgesError(lower) {
		t.Error("P7 failed: standalone 'error' should trigger acknowledgement")
	}
}

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
