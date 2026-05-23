package im

import (
	"strings"
	"testing"
)

func TestFormatIMTodoResult(t *testing.T) {
	tests := []struct {
		name string
		tr   *ToolResultInfo
		want string
	}{
		{
			name: "invalid json",
			tr:   &ToolResultInfo{Args: "not json"},
			want: "待办",
		},
		{
			name: "empty todos",
			tr:   &ToolResultInfo{Args: `{"todos":[]}`},
			want: "待办",
		},
		{
			name: "todos with statuses",
			tr: &ToolResultInfo{Args: `{"todos":[
				{"id":"1","content":"task done","status":"done"},
				{"id":"2","content":"task in progress","status":"in_progress"},
				{"id":"3","content":"task pending","status":"pending"}
			]}`},
			want: "task done",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatIMTodoResult(tt.tr)
			if !strings.Contains(got, tt.want) {
				t.Errorf("formatIMTodoResult() = %q, want to contain %q", got, tt.want)
			}
		})
	}
}

func TestFormatIMReadFileResult(t *testing.T) {
	tests := []struct {
		name string
		tr   *ToolResultInfo
		want string
	}{
		{
			name: "error case",
			tr:   &ToolResultInfo{IsError: true, Result: "not found", Args: `{"file_path":"/tmp/main.go"}`},
			want: "main.go",
		},
		{
			name: "success with path",
			tr:   &ToolResultInfo{Args: `{"file_path":"/tmp/main.go"}`, Result: "package main\n\nfunc main() {}"},
			want: "main.go",
		},
		{
			name: "success no path",
			tr:   &ToolResultInfo{Result: "package main"},
			want: "Read",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatIMReadFileResult(tt.tr)
			if !strings.Contains(got, tt.want) {
				t.Errorf("formatIMReadFileResult() = %q, want to contain %q", got, tt.want)
			}
		})
	}
}

func TestFormatIMListDirResult(t *testing.T) {
	tests := []struct {
		name string
		tr   *ToolResultInfo
		want string
	}{
		{
			name: "error case with path",
			tr:   &ToolResultInfo{IsError: true, Result: "not found", Args: `{"path":"/tmp/dir"}`},
			want: "/tmp/dir",
		},
		{
			name: "error case no path",
			tr:   &ToolResultInfo{IsError: true, Result: "not found"},
			want: "List",
		},
		{
			name: "success with path",
			tr:   &ToolResultInfo{Args: `{"path":"/tmp/dir"}`, Result: "file1.go\nfile2.go"},
			want: "/tmp/dir",
		},
		{
			name: "success no path",
			tr:   &ToolResultInfo{Result: "file1.go\nfile2.go"},
			want: "2",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatIMListDirResult(tt.tr)
			if !strings.Contains(got, tt.want) {
				t.Errorf("formatIMListDirResult() = %q, want to contain %q", got, tt.want)
			}
		})
	}
}

func TestFormatIMGlobResult(t *testing.T) {
	tests := []struct {
		name string
		tr   *ToolResultInfo
		want string
	}{
		{
			name: "error case with pattern",
			tr:   &ToolResultInfo{IsError: true, Result: "no matches", Args: `{"pattern":"**/*.go"}`},
			want: "**/*.go",
		},
		{
			name: "error case no pattern",
			tr:   &ToolResultInfo{IsError: true, Result: "fail"},
			want: "Glob",
		},
		{
			name: "success with pattern",
			tr:   &ToolResultInfo{Args: `{"pattern":"**/*.go"}`, Result: "file1.go\nfile2.go\nfile3.go"},
			want: "3",
		},
		{
			name: "success no pattern",
			tr:   &ToolResultInfo{Result: "file1.go"},
			want: "1",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatIMGlobResult(tt.tr)
			if !strings.Contains(got, tt.want) {
				t.Errorf("formatIMGlobResult() = %q, want to contain %q", got, tt.want)
			}
		})
	}
}

func TestFormatIMEditResult(t *testing.T) {
	tests := []struct {
		name string
		tr   *ToolResultInfo
		want string
	}{
		{
			name: "error case with path",
			tr:   &ToolResultInfo{IsError: true, Result: "not found", Args: `{"file_path":"/tmp/main.go"}`},
			want: "main.go",
		},
		{
			name: "error case no path",
			tr:   &ToolResultInfo{IsError: true, Result: "conflict"},
			want: "Edit",
		},
		{
			name: "success with path",
			tr:   &ToolResultInfo{Args: `{"file_path":"/tmp/main.go","old_text":"old","new_text":"new"}`},
			want: "main.go",
		},
		{
			name: "success no path",
			tr:   &ToolResultInfo{Args: `{"old_text":"old","new_text":"new"}`},
			want: "Edit",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatIMEditResult(tt.tr)
			if !strings.Contains(got, tt.want) {
				t.Errorf("formatIMEditResult() = %q, want to contain %q", got, tt.want)
			}
		})
	}
}

func TestFormatIMWriteResult(t *testing.T) {
	tests := []struct {
		name string
		tr   *ToolResultInfo
		want string
	}{
		{
			name: "error case with path",
			tr:   &ToolResultInfo{IsError: true, Result: "denied", Args: `{"path":"/tmp/new.go"}`},
			want: "new.go",
		},
		{
			name: "success with path",
			tr:   &ToolResultInfo{Args: `{"path":"/tmp/new.go","content":"package main\nfunc main() {}\n"}`},
			want: "new.go",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatIMWriteResult(tt.tr)
			if !strings.Contains(got, tt.want) {
				t.Errorf("formatIMWriteResult() = %q, want to contain %q", got, tt.want)
			}
		})
	}
}

func TestFormatIMSpawnAgentResult(t *testing.T) {
	tests := []struct {
		name string
		tr   *ToolResultInfo
		want string
	}{
		{
			name: "error case",
			tr:   &ToolResultInfo{IsError: true, Result: "spawn failed"},
			want: "sub-agent",
		},
		{
			name: "empty success",
			tr:   &ToolResultInfo{IsError: false, Result: ""},
			want: "sub-agent",
		},
		{
			name: "success with output",
			tr:   &ToolResultInfo{IsError: false, Result: "agent-1 created"},
			want: "sub-agent",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatIMSpawnAgentResult(tt.tr)
			if !strings.Contains(got, tt.want) {
				t.Errorf("formatIMSpawnAgentResult() = %q, want to contain %q", got, tt.want)
			}
		})
	}
}

func TestFormatIMSkillResult(t *testing.T) {
	tests := []struct {
		name string
		tr   *ToolResultInfo
		want string
	}{
		{
			name: "error case",
			tr:   &ToolResultInfo{IsError: true, Result: "skill not found"},
			want: "skill not found",
		},
		{
			name: "empty success",
			tr:   &ToolResultInfo{IsError: false, Result: ""},
			want: "技能",
		},
		{
			name: "success with output",
			tr:   &ToolResultInfo{IsError: false, Result: "skill loaded: go-test"},
			want: "skill loaded: go-test",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatIMSkillResult(tt.tr)
			if !strings.Contains(got, tt.want) {
				t.Errorf("formatIMSkillResult() = %q, want to contain %q", got, tt.want)
			}
		})
	}
}

func TestFormatIMSaveMemoryResult(t *testing.T) {
	tests := []struct {
		name string
		tr   *ToolResultInfo
		want string
	}{
		{
			name: "error case",
			tr:   &ToolResultInfo{IsError: true, Result: "save failed"},
			want: "save failed",
		},
		{
			name: "empty success",
			tr:   &ToolResultInfo{IsError: false, Result: ""},
			want: "记忆",
		},
		{
			name: "success with output",
			tr:   &ToolResultInfo{IsError: false, Result: "saved key: build-cmd"},
			want: "saved key: build-cmd",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatIMSaveMemoryResult(tt.tr)
			if !strings.Contains(got, tt.want) {
				t.Errorf("formatIMSaveMemoryResult() = %q, want to contain %q", got, tt.want)
			}
		})
	}
}

func TestFormatIMStartCommandResult(t *testing.T) {
	tests := []struct {
		name string
		tr   *ToolResultInfo
		want string
	}{
		{
			name: "error case with cmd",
			tr:   &ToolResultInfo{IsError: true, Result: "failed", Args: `{"command":"go build"}`},
			want: "Failed",
		},
		{
			name: "success with command",
			tr:   &ToolResultInfo{IsError: false, Args: `{"command":"npm run dev"}`},
			want: "Started",
		},
		{
			name: "success no command",
			tr:   &ToolResultInfo{IsError: false, Result: ""},
			want: "Started",
		},
		{
			name: "success with detail fallback",
			tr:   &ToolResultInfo{IsError: false, Detail: "ls -la", Args: `{"command":"ls -la"}`},
			want: "Started",
		},
		{
			name: "failure",
			tr:   &ToolResultInfo{IsError: true, Result: "permission denied"},
			want: "Failed",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatIMStartCommandResult(tt.tr)
			if !strings.Contains(got, tt.want) {
				t.Errorf("formatIMStartCommandResult() = %q, want to contain %q", got, tt.want)
			}
		})
	}
}

func TestFormatSpecialIMToolResult(t *testing.T) {
	// Test dispatching to correct formatters
	tests := []struct {
		name    string
		tr      *ToolResultInfo
		handled bool
	}{
		{name: "run_command", tr: &ToolResultInfo{ToolName: "run_command", Args: `{"command":"go build"}`}, handled: true},
		{name: "bash", tr: &ToolResultInfo{ToolName: "bash", Args: `{"command":"ls"}`}, handled: true},
		{name: "todo_write", tr: &ToolResultInfo{ToolName: "todo_write", Args: `{"todos":[]}`}, handled: true},
		{name: "read_file", tr: &ToolResultInfo{ToolName: "read_file", Args: `{"file_path":"/tmp/a.go"}`}, handled: true},
		{name: "edit_file", tr: &ToolResultInfo{ToolName: "edit_file", Args: `{"file_path":"/tmp/a.go"}`}, handled: true},
		{name: "write_file", tr: &ToolResultInfo{ToolName: "write_file", Args: `{"path":"/tmp/a.go"}`}, handled: true},
		{name: "list_directory", tr: &ToolResultInfo{ToolName: "list_directory", Result: "file.go"}, handled: true},
		{name: "glob", tr: &ToolResultInfo{ToolName: "glob", Args: `{"pattern":"**/*.go"}`, Result: "a.go"}, handled: true},
		{name: "search_files", tr: &ToolResultInfo{ToolName: "search_files", Args: `{"pattern":"TODO"}`, Result: "found 3"}, handled: true},
		{name: "git_status", tr: &ToolResultInfo{ToolName: "git_status", Result: "M main.go"}, handled: true},
		{name: "start_command", tr: &ToolResultInfo{ToolName: "start_command", Args: `{"command":"npm start"}`}, handled: true},
		{name: "stop_command", tr: &ToolResultInfo{ToolName: "stop_command"}, handled: true},
		{name: "spawn_agent", tr: &ToolResultInfo{ToolName: "spawn_agent"}, handled: true},
		{name: "wait_agent", tr: &ToolResultInfo{ToolName: "wait_agent"}, handled: true},
		{name: "list_agents", tr: &ToolResultInfo{ToolName: "list_agents"}, handled: true},
		{name: "mcp_list_capabilities", tr: &ToolResultInfo{ToolName: "mcp_list_capabilities"}, handled: true},
		{name: "cron_create", tr: &ToolResultInfo{ToolName: "cron_create"}, handled: true},
		{name: "cron_delete", tr: &ToolResultInfo{ToolName: "cron_delete"}, handled: true},
		{name: "cron_list", tr: &ToolResultInfo{ToolName: "cron_list"}, handled: true},
		{name: "sleep", tr: &ToolResultInfo{ToolName: "sleep", Args: `{"seconds":5}`}, handled: true},
		{name: "skill", tr: &ToolResultInfo{ToolName: "skill"}, handled: true},
		{name: "save_memory", tr: &ToolResultInfo{ToolName: "save_memory"}, handled: true},
		{name: "web_fetch", tr: &ToolResultInfo{ToolName: "web_fetch", Args: `{"url":"https://example.com"}`}, handled: true},
		{name: "web_search", tr: &ToolResultInfo{ToolName: "web_search", Args: `{"query":"golang"}`}, handled: true},
		{name: "worktree_enter", tr: &ToolResultInfo{ToolName: "worktree_enter"}, handled: true},
		{name: "worktree_exit", tr: &ToolResultInfo{ToolName: "worktree_exit"}, handled: true},
		{name: "read_command_output", tr: &ToolResultInfo{ToolName: "read_command_output"}, handled: true},
		{name: "write_command_input", tr: &ToolResultInfo{ToolName: "write_command_input"}, handled: true},
		{name: "list_commands", tr: &ToolResultInfo{ToolName: "list_commands"}, handled: true},
		{name: "wait_command", tr: &ToolResultInfo{ToolName: "wait_command"}, handled: true},
		{name: "mcp_read_resource", tr: &ToolResultInfo{ToolName: "mcp_read_resource"}, handled: true},
		{name: "mcp_get_prompt", tr: &ToolResultInfo{ToolName: "mcp_get_prompt"}, handled: true},
		// Silent tools (handled but empty result is ok)
		{name: "teammate_list silent", tr: &ToolResultInfo{ToolName: "teammate_list"}, handled: true},
		{name: "swarm_task_list silent", tr: &ToolResultInfo{ToolName: "swarm_task_list"}, handled: true},
		{name: "a2a_discover silent", tr: &ToolResultInfo{ToolName: "a2a_discover"}, handled: true},
		// Unknown tool still handled (by error formatter)
		{name: "unknown tool", tr: &ToolResultInfo{ToolName: "unknown_tool_xyz"}, handled: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handled, result := formatSpecialIMToolResult(tt.tr)
			if handled != tt.handled {
				t.Errorf("handled = %v, want %v", handled, tt.handled)
			}
			_ = result
		})
	}
}

func TestFormatIMSearchResult(t *testing.T) {
	tests := []struct {
		name string
		tr   *ToolResultInfo
		want string
	}{
		{
			name: "error case",
			tr:   &ToolResultInfo{IsError: true, Result: "fail", Args: `{"pattern":"TODO"}`},
			want: "TODO",
		},
		{
			name: "success with matches",
			tr:   &ToolResultInfo{Args: `{"pattern":"TODO"}`, Result: "main.go: TODO fix this\nutil.go: TODO refactor"},
			want: "2",
		},
		{
			name: "success empty",
			tr:   &ToolResultInfo{Args: `{"pattern":"NOTFOUND"}`, Result: ""},
			want: "0",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatIMSearchResult(tt.tr)
			if !strings.Contains(got, tt.want) {
				t.Errorf("formatIMSearchResult() = %q, want to contain %q", got, tt.want)
			}
		})
	}
}
