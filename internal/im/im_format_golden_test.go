package im

// r166 golden harness for imLabel + formatToolCallText.
//
// Provenance: names and label keys below were extracted MECHANICALLY from
// origin/main 29fad0ab0 (internal/im/tool_format.go:22-363 and
// internal/im/tool_format_helpers.go:52-568) — no hand transcription.
// The golden file was generated from the pre-refactor switch code.
//
// Regenerate after INTENTIONAL label/copy changes only:
//
//	IM_GOLDEN_DUMP=1 go test -tags goolm -run TestDumpIMFormatGolden ./internal/im/

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

var goldenToolNames = []string{
	"a2a_remote",
	"a2a_send_task",
	"ask_user",
	"bash",
	"browser",
	"cancel_agent",
	"config",
	"cron_create",
	"cron_delete",
	"cron_get",
	"cron_list",
	"cron_pause",
	"cron_resume",
	"cron_update",
	"delegate",
	"delete_memory",
	"edit_file",
	"enter_plan_mode",
	"enter_worktree",
	"exit_plan_mode",
	"exit_worktree",
	"git_add",
	"git_blame",
	"git_branch_list",
	"git_commit",
	"git_diff",
	"git_log",
	"git_remote",
	"git_show",
	"git_stash",
	"git_stash_list",
	"git_status",
	"glob",
	"grep",
	"im",
	"lanchat",
	"list_directory",
	"list_worktree",
	"lsp_code_actions",
	"lsp_definition",
	"lsp_diagnostics",
	"lsp_hover",
	"lsp_implementation",
	"lsp_references",
	"lsp_rename",
	"lsp_symbols",
	"lsp_workspace_symbols",
	"mobile_device",
	"multi_file_edit",
	"multi_file_read",
	"multi_file_write",
	"notebook_edit",
	"powershell",
	"read_file",
	"run_command",
	"save_memory",
	"screenshot",
	"search_files",
	"send_message",
	"skill",
	"sleep",
	"spawn_agent",
	"start_command",
	"stop_command",
	"swarm_task_complete",
	"swarm_task_create",
	"switch_mode",
	"task_create",
	"task_get",
	"task_list",
	"task_output",
	"task_stop",
	"task_update",
	"team_create",
	"team_delete",
	"teammate_results",
	"teammate_shutdown",
	"teammate_spawn",
	"todo_write",
	"wait_agent",
	"wait_command",
	"warp",
	"web_fetch",
	"web_search",
	"write_command_input",
	"write_file",
	// default-path probes (no dedicated case; prettifyToolName route)
	"mcp__cf__search",
	"totally_unknown_tool",
	"",
}

var goldenLabelKeys = []string{
	"a2a_remote",
	"a2a_send_task",
	"active_commands",
	"ask_user",
	"bg_command",
	"bg_command_started",
	"browser",
	"cancel_agent",
	"command_done",
	"command_failed",
	"command_input",
	"command_manage",
	"command_stopped",
	"config",
	"cron_get",
	"cron_pause",
	"cron_paused",
	"cron_resume",
	"cron_resumed",
	"cron_update",
	"cron_update_done",
	"delegate",
	"delegated_to",
	"delete_memory",
	"edit_file",
	"edit_multi",
	"edit_notebook",
	"edits",
	"errors",
	"failed",
	"fetch",
	"files",
	"files_edited",
	"files_failed",
	"files_written",
	"find_files",
	"first_lines",
	"from_line",
	"git_blame",
	"git_branch_list",
	"git_commit",
	"git_committed",
	"git_diff",
	"git_log",
	"git_remote",
	"git_show",
	"git_stage",
	"git_staged",
	"git_stash",
	"git_stash_list",
	"git_status",
	"im_manage",
	"input_sent",
	"lines",
	"lines_extracted",
	"list_directory",
	"load_skill",
	"lsp_code_actions",
	"lsp_definition",
	"lsp_diagnostics",
	"lsp_hover",
	"lsp_implementation",
	"lsp_references",
	"lsp_rename",
	"lsp_symbols",
	"matches",
	"mcp_prompt",
	"mcp_service",
	"mcp_service_list",
	"memory_delete",
	"memory_deleted",
	"memory_save",
	"memory_saved",
	"message_sent",
	"mobile_device",
	"mode_switched",
	"no_active_agents",
	"no_active_commands",
	"no_active_subtasks",
	"no_matches",
	"no_new_output",
	"no_output",
	"pages",
	"read",
	"read_file",
	"read_multi",
	"read_output",
	"reply_received",
	"resource_content",
	"resource_read",
	"results",
	"run_command",
	"save_memory",
	"search",
	"send_input",
	"send_message",
	"showing_first",
	"skill_load",
	"skill_loaded",
	"spawn_agent",
	"stop_command",
	"sub_task",
	"sub_task_done",
	"sub_task_list",
	"sub_task_started",
	"swarm_task_complete",
	"swarm_task_create",
	"switch_mode",
	"task_completed",
	"task_create",
	"task_created",
	"task_sent",
	"task_stopped",
	"team_create",
	"team_created",
	"team_delete",
	"team_deleted",
	"teammate_created",
	"teammate_results",
	"teammate_shutdown",
	"teammate_shutdown_done",
	"teammate_spawn",
	"todos",
	"update_todos",
	"wait_agent",
	"wait_command",
	"write_file",
	"write_multi",
}

var goldenArgsShapes = []string{
	"",
	`{"path":"/tmp/report.md","offset":10}`,
	`{"command":"go test ./...","pattern":"foo.*bar","query":"agent auth","url":"https://example.com/x","to":"bob","name":"team-1","mode":"auto","action":"list","subject":"ship it","key":"build-cache","setting":"vendor.model","message":"r166 gold","cron":"*/5 * * * *","target":"a2a-host:99","agent":"codex","agent_id":"agent-7","task":"run the gauntlet","file_path":"/tmp/a.go"}`,
}

var goldenLangs = []ToolLanguage{ToolLangEn, ToolLangZhCN}

func goldenFilePath(t *testing.T) string {
	t.Helper()
	return filepath.Join("testdata", "im_format_golden.txt")
}

// TestDumpIMFormatGolden regenerates the golden file from current code.
func TestDumpIMFormatGolden(t *testing.T) {
	if os.Getenv("IM_GOLDEN_DUMP") == "" {
		t.Skip("set IM_GOLDEN_DUMP=1 to regenerate testdata/im_format_golden.txt")
	}
	f, err := os.Create(goldenFilePath(t))
	if err != nil {
		t.Fatalf("create golden: %v", err)
	}
	defer f.Close()
	w := bufio.NewWriter(f)
	for _, lang := range goldenLangs {
		for _, k := range goldenLabelKeys {
			if _, err := fmt.Fprintf(w, "LABEL\t%s\t%s\t%q\n", lang, k, imLabel(lang, k)); err != nil {
				t.Fatalf("write label: %v", err)
			}
		}
	}
	for _, lang := range goldenLangs {
		for _, name := range goldenToolNames {
			for si, args := range goldenArgsShapes {
				out := formatToolCallText(&ToolCallInfo{ToolName: name, Args: args, Detail: fmt.Sprintf("detail-%d", si), Lang: string(lang)})
				if _, err := fmt.Fprintf(w, "CALL\t%s\t%s\t%d\t%q\n", lang, name, si, out); err != nil {
					t.Fatalf("write call: %v", err)
				}
			}
		}
	}
	if err := w.Flush(); err != nil {
		t.Fatalf("flush: %v", err)
	}
}

func loadGolden(t *testing.T) map[string]string {
	t.Helper()
	data, err := os.ReadFile(goldenFilePath(t))
	if err != nil {
		t.Fatalf("golden file missing (regenerate with IM_GOLDEN_DUMP=1): %v", err)
	}
	g := make(map[string]string)
	for _, line := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
		if strings.HasPrefix(line, "LABEL\t") {
			parts := strings.SplitN(line, "\t", 4) // LABEL, lang, key, %q
			if len(parts) != 4 {
				t.Fatalf("malformed golden line: %q", line)
			}
			g[parts[0]+"\t"+parts[1]+"\t"+parts[2]] = parts[3]
			continue
		}
		parts := strings.SplitN(line, "\t", 5) // CALL, lang, name, shapeIdx, %q
		if len(parts) != 5 {
			t.Fatalf("malformed golden line: %q", line)
		}
		g[parts[0]+"\t"+parts[1]+"\t"+parts[2]+"\t"+parts[3]] = parts[4]
	}
	return g
}

func TestIMLabelGolden(t *testing.T) {
	g := loadGolden(t)
	for _, lang := range goldenLangs {
		for _, k := range goldenLabelKeys {
			key := "LABEL\t" + string(lang) + "\t" + k
			wantRaw, ok := g[key]
			if !ok {
				t.Fatalf("golden missing %s", key)
			}
			want, err := strconv.Unquote(wantRaw)
			if err != nil {
				t.Fatalf("unquote %s: %v", key, err)
			}
			if got := imLabel(lang, k); got != want {
				t.Errorf("imLabel(%s,%q) = %q, want %q", lang, k, got, want)
			}
		}
	}
}

func TestFormatToolCallGolden(t *testing.T) {
	g := loadGolden(t)
	for _, lang := range goldenLangs {
		for _, name := range goldenToolNames {
			for si, args := range goldenArgsShapes {
				key := fmt.Sprintf("CALL\t%s\t%s\t%d", lang, name, si)
				wantRaw, ok := g[key]
				if !ok {
					t.Fatalf("golden missing %s", key)
				}
				want, err := strconv.Unquote(wantRaw)
				if err != nil {
					t.Fatalf("unquote %s: %v", key, err)
				}
				got := formatToolCallText(&ToolCallInfo{ToolName: name, Args: args, Detail: fmt.Sprintf("detail-%d", si), Lang: string(lang)})
				if got != want {
					t.Errorf("formatToolCallText(%s,%q,shape%d):\n got %q\nwant %q", lang, name, si, got, want)
				}
			}
		}
	}
}

// TestIMLabelFallbackPins pins the fallthrough semantics the golden dump
// cannot express negatively: unknown keys pass through raw, and the three
// en-only keys (delete_memory, memory_delete, memory_deleted) are raw
// passthrough under zh-CN (zh table predates them; callers never send them
// with zh, but the contract must not silently change).
func TestIMLabelFallbackPins(t *testing.T) {
	if got := imLabel(ToolLangEn, "definitely_not_a_key"); got != "definitely_not_a_key" {
		t.Errorf("en unknown key = %q, want raw passthrough", got)
	}
	if got := imLabel(ToolLangZhCN, "definitely_not_a_key"); got != "definitely_not_a_key" {
		t.Errorf("zh unknown key = %q, want raw passthrough", got)
	}
	for _, k := range []string{"delete_memory", "memory_delete", "memory_deleted"} {
		if got := imLabel(ToolLangZhCN, k); got != k {
			t.Errorf("zh %q = %q, want raw passthrough (en-only key)", k, got)
		}
		if got := imLabel(ToolLangEn, k); got == k || got == "" {
			t.Errorf("en %q = %q, want a real label", k, got)
		}
	}
}
