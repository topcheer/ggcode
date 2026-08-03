package agent

import (
	"encoding/json"
	"testing"
)

func TestShellNativeHint_CatSuggestsReadFile(t *testing.T) {
	s := newShellNativeHintState()
	args, _ := json.Marshal(map[string]string{"command": "cat main.go"})
	hint := s.maybeShellNativeHint("run_command", args)
	if hint == "" {
		t.Fatal("expected hint for cat command")
	}
	if !contains(hint, "read_file") {
		t.Errorf("hint should suggest read_file, got: %s", hint)
	}
}

func TestShellNativeHint_GrepSuggestsGrepTool(t *testing.T) {
	s := newShellNativeHintState()
	args, _ := json.Marshal(map[string]string{"command": "grep -rn 'TODO' ."})
	hint := s.maybeShellNativeHint("run_command", args)
	if hint == "" {
		t.Fatal("expected hint for grep command")
	}
	if !contains(hint, "grep tool") {
		t.Errorf("hint should suggest grep tool, got: %s", hint)
	}
}

func TestShellNativeHint_GitLogSuggestsGitLog(t *testing.T) {
	s := newShellNativeHintState()
	args, _ := json.Marshal(map[string]string{"command": "git log --oneline -5"})
	hint := s.maybeShellNativeHint("run_command", args)
	if hint == "" {
		t.Fatal("expected hint for git log command")
	}
	if !contains(hint, "git_log") {
		t.Errorf("hint should suggest git_log tool, got: %s", hint)
	}
}

func TestShellNativeHint_NoHintForNonShellTool(t *testing.T) {
	s := newShellNativeHintState()
	args, _ := json.Marshal(map[string]string{"command": "cat main.go"})
	hint := s.maybeShellNativeHint("read_file", args)
	if hint != "" {
		t.Errorf("should not fire for read_file tool, got: %s", hint)
	}
}

func TestShellNativeHint_NoHintForBuildCommand(t *testing.T) {
	s := newShellNativeHintState()
	args, _ := json.Marshal(map[string]string{"command": "go build ./..."})
	hint := s.maybeShellNativeHint("run_command", args)
	if hint != "" {
		t.Errorf("should not fire for go build, got: %s", hint)
	}
}

func TestShellNativeHint_FiresOncePerPattern(t *testing.T) {
	s := newShellNativeHintState()
	args, _ := json.Marshal(map[string]string{"command": "cat main.go"})
	hint1 := s.maybeShellNativeHint("run_command", args)
	if hint1 == "" {
		t.Fatal("expected first hint")
	}
	hint2 := s.maybeShellNativeHint("run_command", args)
	if hint2 != "" {
		t.Errorf("should not fire twice for same pattern, got: %s", hint2)
	}
}

func TestShellNativeHint_DifferentPatternsBothFire(t *testing.T) {
	s := newShellNativeHintState()
	catArgs, _ := json.Marshal(map[string]string{"command": "cat main.go"})
	grepArgs, _ := json.Marshal(map[string]string{"command": "grep TODO ."})
	hint1 := s.maybeShellNativeHint("run_command", catArgs)
	hint2 := s.maybeShellNativeHint("run_command", grepArgs)
	if hint1 == "" || hint2 == "" {
		t.Fatal("both patterns should fire independently")
	}
}

func TestShellNativeHint_ResetClearsFiredSet(t *testing.T) {
	s := newShellNativeHintState()
	args, _ := json.Marshal(map[string]string{"command": "cat main.go"})
	_ = s.maybeShellNativeHint("run_command", args)
	s.reset()
	hint := s.maybeShellNativeHint("run_command", args)
	if hint == "" {
		t.Fatal("should fire again after reset")
	}
}

func TestShellNativeHint_StartCommandAlsoTriggers(t *testing.T) {
	s := newShellNativeHintState()
	args, _ := json.Marshal(map[string]string{"command": "find . -name '*.go'"})
	hint := s.maybeShellNativeHint("start_command", args)
	if hint == "" {
		t.Fatal("expected hint for find in start_command")
	}
	if !contains(hint, "glob") {
		t.Errorf("hint should suggest glob tool, got: %s", hint)
	}
}

func TestShellNativeHint_GitCommitSuggestsGitCommit(t *testing.T) {
	s := newShellNativeHintState()
	args, _ := json.Marshal(map[string]string{"command": "git commit -m 'test'"})
	hint := s.maybeShellNativeHint("run_command", args)
	if hint == "" {
		t.Fatal("expected hint for git commit")
	}
	if !contains(hint, "git_commit") {
		t.Errorf("hint should suggest git_commit tool, got: %s", hint)
	}
}

func TestExtractShellCommandFromArgs(t *testing.T) {
	tests := []struct {
		name string
		json string
		want string
	}{
		{"simple", `{"command":"cat foo.go"}`, "cat foo.go"},
		{"with_path", `{"path":"/tmp","command":"grep bar ."}`, "grep bar ."},
		{"no_command", `{"path":"/tmp"}`, ""},
		{"escaped_quote", `{"command":"echo \"hello\""}`, `echo "hello"`},
		{"empty", ``, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractShellCommandFromArgs([]byte(tt.json))
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}
