package agent

import (
	"encoding/json"
	"testing"
)

func TestParamValidator_ValidateFilePath(t *testing.T) {
	tests := []struct {
		name string
		path string
		want string // empty = valid, non-empty = error message
	}{
		{"empty path", "", "file path is empty"},
		{"absolute outside workspace", "/etc/passwd", "absolute path '/etc/passwd' may be outside workspace"},
		{"path traversal", "../../../etc/passwd", "path contains '../' which may indicate unintended traversal"},
		{"null character", "test\x00file", "path contains invalid characters"},
		{"valid relative path", "internal/agent/agent.go", ""},
		{"valid workspace absolute", "/Volumes/new/ggai/ggcode/go.mod", ""},
		{"valid home path", "/Users/test/file.txt", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := validateFilePath(tt.path)
			if tt.want != "" && got != tt.want {
				t.Errorf("validateFilePath() = %q, want %q", got, tt.want)
			} else if tt.want == "" && got != "" {
				t.Errorf("validateFilePath() = %q, want empty (valid)", got)
			}
		})
	}
}

func TestParamValidator_ValidateCommand(t *testing.T) {
	tests := []struct {
		name string
		cmd  string
		want string // empty = valid, non-empty = error message
	}{
		{"empty command", "", "command is empty"},
		{"whitespace only", "   ", "command is empty"},
		{"rm -rf /", "rm -rf /", "command contains potentially destructive pattern: 'rm -rf /'"},
		{"rm -rf /*", "sudo rm -rf /*", "command contains potentially destructive pattern: 'rm -rf /'"},
		{"fork bomb", ":(){:|:&};:", "command contains potentially destructive pattern: ':(){:|:&};:'"},
		{"disk overwrite", "echo test > /dev/sda", "command contains potentially destructive pattern: '> /dev/sda'"},
		{"valid command", "go build ./...", ""},
		{"valid git command", "git status", ""},
		{"valid test", "go test -tags goolm ./...", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := validateCommand(tt.cmd)
			if tt.want != "" && got != tt.want {
				t.Errorf("validateCommand() = %q, want %q", got, tt.want)
			} else if tt.want == "" && got != "" {
				t.Errorf("validateCommand() = %q, want empty (valid)", got)
			}
		})
	}
}

func TestParamValidator_IsEmptyValue(t *testing.T) {
	tests := []struct {
		name string
		val  interface{}
		want bool
	}{
		{"nil", nil, true},
		{"empty string", "", true},
		{"whitespace string", "   ", true},
		{"non-empty string", "test", false},
		{"empty array", []interface{}{}, true},
		{"non-empty array", []interface{}{"a", "b"}, false},
		{"empty map", map[string]interface{}{}, true},
		{"non-empty map", map[string]interface{}{"key": "val"}, false},
		{"null JSON", json.RawMessage("null"), true},
		{"empty JSON", json.RawMessage(""), true},
		{"non-empty JSON", json.RawMessage(`{"key":"val"}`), false},
		{"zero int", 0, false}, // zero is NOT considered empty
		{"false", false, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isEmptyValue(tt.val)
			if got != tt.want {
				t.Errorf("isEmptyValue() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestParamValidator_ValidateToolCall(t *testing.T) {
	v := newParamValidator()

	tests := []struct {
		name     string
		toolName string
		args     map[string]interface{}
		wantErr  bool
	}{
		{
			name:     "edit_file with missing path",
			toolName: "edit_file",
			args:     map[string]interface{}{"old_text": "old", "new_text": "new"},
			wantErr:  true,
		},
		{
			name:     "edit_file with empty old_text",
			toolName: "edit_file",
			args:     map[string]interface{}{"file_path": "test.go", "old_text": "", "new_text": "new"},
			wantErr:  true,
		},
		{
			name:     "edit_file valid",
			toolName: "edit_file",
			args:     map[string]interface{}{"file_path": "test.go", "old_text": "old", "new_text": "new"},
			wantErr:  false,
		},
		{
			name:     "write_file with empty content",
			toolName: "write_file",
			args:     map[string]interface{}{"path": "test.go", "content": ""},
			wantErr:  true,
		},
		{
			name:     "git_commit with empty message",
			toolName: "git_commit",
			args:     map[string]interface{}{"message": ""},
			wantErr:  true,
		},
		{
			name:     "git_commit valid",
			toolName: "git_commit",
			args:     map[string]interface{}{"message": "fix: something"},
			wantErr:  false,
		},
		{
			name:     "read_file with path traversal",
			toolName: "read_file",
			args:     map[string]interface{}{"path": "../../../etc/passwd"},
			wantErr:  true,
		},
		{
			name:     "run_command with dangerous pattern",
			toolName: "run_command",
			args:     map[string]interface{}{"command": "rm -rf /"},
			wantErr:  true,
		},
		{
			name:     "run_command empty",
			toolName: "run_command",
			args:     map[string]interface{}{"command": ""},
			wantErr:  true,
		},
		{
			name:     "run_command valid",
			toolName: "run_command",
			args:     map[string]interface{}{"command": "go build ./..."},
			wantErr:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v.reset() // reset hints counter for each test
			got := v.validateToolCall(tt.toolName, tt.args)
			if tt.wantErr && got == "" {
				t.Errorf("validateToolCall() expected error but got none")
			} else if !tt.wantErr && got != "" {
				t.Errorf("validateToolCall() unexpected error: %s", got)
			}
		})
	}
}

func TestParamValidator_MaxHints(t *testing.T) {
	v := newParamValidator()
	v.maxHints = 2

	// First two calls should produce guidance
	got1 := v.validateToolCall("git_commit", map[string]interface{}{"message": ""})
	if got1 == "" {
		t.Error("First call should produce guidance")
	}

	got2 := v.validateToolCall("git_commit", map[string]interface{}{"message": ""})
	if got2 == "" {
		t.Error("Second call should produce guidance")
	}

	// Third call should be silent (max hints reached)
	got3 := v.validateToolCall("git_commit", map[string]interface{}{"message": ""})
	if got3 != "" {
		t.Error("Third call should be silent (max hints reached)")
	}

	// Reset should allow hints again
	v.reset()
	got4 := v.validateToolCall("git_commit", map[string]interface{}{"message": ""})
	if got4 == "" {
		t.Error("After reset, call should produce guidance again")
	}
}

func TestGetFileArg(t *testing.T) {
	tests := []struct {
		name     string
		toolName string
		args     map[string]interface{}
		want     string
		wantOK   bool
	}{
		{
			name:     "read_file with string path",
			toolName: "read_file",
			args:     map[string]interface{}{"path": "test.go"},
			want:     "test.go",
			wantOK:   true,
		},
		{
			name:     "grep with path",
			toolName: "grep",
			args:     map[string]interface{}{"pattern": "test", "path": "internal"},
			want:     "internal",
			wantOK:   true,
		},
		{
			name:     "non-file tool",
			toolName: "git_commit",
			args:     map[string]interface{}{"message": "test"},
			want:     "",
			wantOK:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, gotOK := getFileArg(tt.toolName, tt.args)
			if got != tt.want || gotOK != tt.wantOK {
				t.Errorf("getFileArg() = (%q, %v), want (%q, %v)", got, gotOK, tt.want, tt.wantOK)
			}
		})
	}
}

func TestGetCommandArg(t *testing.T) {
	tests := []struct {
		name     string
		toolName string
		args     map[string]interface{}
		want     string
		wantOK   bool
	}{
		{
			name:     "run_command with command",
			toolName: "run_command",
			args:     map[string]interface{}{"command": "go build"},
			want:     "go build",
			wantOK:   true,
		},
		{
			name:     "start_command with command",
			toolName: "start_command",
			args:     map[string]interface{}{"command": "sleep 10"},
			want:     "sleep 10",
			wantOK:   true,
		},
		{
			name:     "non-command tool",
			toolName: "read_file",
			args:     map[string]interface{}{"path": "test.go"},
			want:     "",
			wantOK:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, gotOK := getCommandArg(tt.toolName, tt.args)
			if got != tt.want || gotOK != tt.wantOK {
				t.Errorf("getCommandArg() = (%q, %v), want (%q, %v)", got, gotOK, tt.want, tt.wantOK)
			}
		})
	}
}
