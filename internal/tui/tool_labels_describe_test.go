package tui

import (
	"strings"
	"testing"
)

// This file is the table-driven regression net for describeTool
// (tool_labels.go). describeTool is a ~113-branch display dispatcher that was
// historically covered by only a handful of tests; these tables pin the
// current output contract (DisplayName / Detail / Activity per tool, arg
// combination, and language) so that a future refactor of the function has a
// safety net. The intent is to lock BEHAVIOR, not implementation: localized
// label expectations are computed through the same helpers describeTool uses,
// while pure display logic (truncation, durations, fallback chains) is pinned
// with exact strings.

var describeToolLangs = []Language{LangEnglish, LangZhCN}

// routingCase pins one switch branch of describeTool. When want is nil the
// expectation is toolPresentationFor(lang, action, target), i.e. the branch
// must route to exactly that action label with exactly that detail target.
type routingCase struct {
	name   string
	tool   string
	args   string
	action string
	target string
	want   func(lang Language) toolPresentation
}

func TestDescribeToolRoutingTable(t *testing.T) {
	cases := []routingCase{
		// --- file tools -------------------------------------------------
		{name: "read_file path", tool: "read_file", args: `{"path":"src/main.go"}`,
			action: "read", target: displayToolFileTarget("src/main.go")},
		{name: "edit_file no old_text creates", tool: "edit_file", args: `{"file_path":"new_file.go","new_text":"x"}`,
			action: "create", target: displayToolFileTarget("new_file.go")},
		{name: "edit_file blank old_text creates", tool: "edit_file", args: `{"file_path":"new_file.go","old_text":"   "}`,
			action: "create", target: displayToolFileTarget("new_file.go")},
		{name: "edit_file with old_text edits", tool: "edit_file", args: `{"file_path":"old_file.go","old_text":"a","new_text":"b"}`,
			action: "edit", target: displayToolFileTarget("old_file.go")},
		{name: "write_file path", tool: "write_file", args: `{"path":"out.txt"}`,
			action: "write", target: displayToolFileTarget("out.txt")},
		{name: "glob pattern", tool: "glob", args: `{"pattern":"**/*.go"}`,
			action: "find", target: displayToolTarget("**/*.go")},
		{name: "grep pattern", tool: "grep", args: `{"pattern":"TODO"}`,
			action: "search", target: displayToolTarget("TODO")},
		{name: "search_files query fallback", tool: "search_files", args: `{"query":"auth"}`,
			action: "search", target: displayToolTarget("auth")},
		{name: "search_files path fallback", tool: "search_files", args: `{"path":"internal"}`,
			action: "search", target: displayToolTarget("internal")},
		{name: "list_directory path", tool: "list_directory", args: `{"path":"cmd"}`,
			action: "list", target: displayToolFileTarget("cmd")},
		{name: "list_directory directory fallback", tool: "list_directory", args: `{"directory":"internal"}`,
			action: "list", target: displayToolFileTarget("internal")},

		// --- command tools (no leading-comment title) --------------------
		{name: "run_command plain", tool: "run_command", args: `{"command":"echo hi"}`,
			action: "run", target: displayToolTarget("echo hi")},
		{name: "bash alias cmd", tool: "bash", args: `{"cmd":"ls -la"}`,
			action: "run", target: displayToolTarget("ls -la")},
		{name: "powershell alias", tool: "powershell", args: `{"command":"Get-ChildItem"}`,
			action: "run", target: displayToolTarget("Get-ChildItem")},
		{name: "run_command empty", tool: "run_command", args: `{}`,
			action: "run", target: ""},
		{name: "start_command plain", tool: "start_command", args: `{"command":"go test ./..."}`,
			action: "run_in_background", target: displayToolTarget("go test ./...")},

		// --- background job tools ---------------------------------------
		{name: "read_command_output job", tool: "read_command_output", args: `{"job_id":"abcdefgh1234"}`,
			action: "output", target: displayToolTarget(shortenJobID("abcdefgh1234"))},
		{name: "stop_command job", tool: "stop_command", args: `{"job_id":"wxyz98765"}`,
			action: "stop", target: displayToolTarget(shortenJobID("wxyz98765"))},
		{name: "wait_command with seconds", tool: "wait_command", args: `{"job_id":"job-42","wait_seconds":"5"}`,
			action: "wait", target: displayToolTarget(shortenJobID("job-42") + " (5s)")},
		{name: "wait_command bare", tool: "wait_command", args: `{"job_id":"job-42"}`,
			action: "wait", target: displayToolTarget(shortenJobID("job-42"))},
		{name: "list_commands", tool: "list_commands", args: `{}`,
			action: "list_jobs", target: ""},

		// --- web ---------------------------------------------------------
		{name: "web_fetch url", tool: "web_fetch", args: `{"url":"https://example.com/a"}`,
			action: "fetch", target: displayToolTarget("https://example.com/a")},
		{name: "web_search query", tool: "web_search", args: `{"query":"go generics"}`,
			action: "search", target: displayToolTarget("go generics")},

		// --- misc single-branch tools ------------------------------------
		{name: "todo_write", tool: "todo_write", args: `{}`,
			action: "todo", target: ""},
		{name: "task prompt", tool: "task", args: `{"prompt":"refactor utils"}`,
			action: "task", target: displayToolTarget("refactor utils")},
		{name: "agent prompt fallback", tool: "agent", args: `{"prompt":"scan deps"}`,
			action: "task", target: displayToolTarget("scan deps")},
		{name: "agent type fallback", tool: "agent", args: `{"agent_type":"reviewer"}`,
			action: "task", target: displayToolTarget("reviewer")},
		{name: "skill", tool: "skill", args: `{"skill":"deploy"}`,
			action: "skill", target: displayToolTarget("deploy")},
		{name: "ask_user title", tool: "ask_user", args: `{"title":"Pick one"}`,
			action: "ask", target: displayToolTarget("Pick one")},
		{name: "ask_user first question", tool: "ask_user", args: `{"questions":[{"title":"A?"},{"title":"B?"}]}`,
			action: "ask", target: displayToolTarget("A? +1")},

		// --- git ----------------------------------------------------------
		{name: "git_status path", tool: "git_status", args: `{"path":"sub"}`,
			action: "inspect", target: displayToolFileTarget("sub")},
		{name: "git_diff plain", tool: "git_diff", args: `{}`,
			action: "diff", target: ""},
		{name: "git_diff cached", tool: "git_diff", args: `{"cached":"true"}`,
			action: "diff", target: "--cached"},
		{name: "git_diff file", tool: "git_diff", args: `{"file":"a.go"}`,
			action: "diff", target: displayToolFileTarget("a.go")},
		{name: "git_diff cached and file", tool: "git_diff", args: `{"cached":"true","file":"a.go"}`,
			action: "diff", target: "--cached " + displayToolFileTarget("a.go")},
		{name: "git_log", tool: "git_log", args: `{}`,
			action: "log", target: ""},
		{name: "git_show revision", tool: "git_show", args: `{"revision":"HEAD~1"}`,
			action: "show", target: displayToolTarget("HEAD~1")},
		{name: "git_blame file", tool: "git_blame", args: `{"file":"x.go"}`,
			action: "blame", target: displayToolFileTarget("x.go")},
		{name: "git_branch_list local", tool: "git_branch_list", args: `{}`,
			action: "branches", target: ""},
		{name: "git_branch_list remote", tool: "git_branch_list", args: `{"remote":"true"}`,
			action: "branches", target: "--remote"},
		{name: "git_remote", tool: "git_remote", args: `{}`,
			action: "remote", target: ""},
		{name: "git_stash_list", tool: "git_stash_list", args: `{}`,
			action: "stash", target: "list"},
		{name: "git_add files", tool: "git_add", args: `{"files":["a.go","b.go"]}`,
			action: "stage", target: displayToolFileTarget("a.go, b.go")},
		{name: "git_commit message", tool: "git_commit", args: `{"message":"fix: crash"}`,
			action: "commit", target: compactSingleLine("fix: crash")},
		{name: "git_stash default push", tool: "git_stash", args: `{}`,
			action: "stash", target: "push"},
		{name: "git_stash pop", tool: "git_stash", args: `{"action":"pop"}`,
			action: "stash", target: "pop"},

		// --- cron simple branches -----------------------------------------
		{name: "cron_delete", tool: "cron_delete", args: `{}`,
			action: "delete", target: "cron job"},
		{name: "cron_list", tool: "cron_list", args: `{}`,
			action: "inspect", target: "cron jobs"},

		// --- plan mode ------------------------------------------------------
		{name: "enter_plan_mode", tool: "enter_plan_mode", args: `{}`,
			want: func(lang Language) toolPresentation {
				return toolPresentation{
					DisplayName: localizedToolLabel(lang, "enter_plan"),
					Detail:      "",
					Activity:    localizedToolActivity(lang, "enter_plan", ""),
				}
			}},
		{name: "exit_plan_mode", tool: "exit_plan_mode", args: `{}`,
			want: func(lang Language) toolPresentation {
				return toolPresentation{
					DisplayName: localizedToolLabel(lang, "exit_plan"),
					Detail:      "",
					Activity:    localizedToolActivity(lang, "exit_plan", ""),
				}
			}},

		// --- task family ----------------------------------------------------
		{name: "task_create subject", tool: "task_create", args: `{"subject":"write tests"}`,
			action: "task", target: displayToolTarget("write tests")},
		{name: "task_update taskId fallback", tool: "task_update", args: `{"taskId":"t-9"}`,
			action: "task", target: displayToolTarget("t-9")},
		{name: "task_list bare", tool: "task_list", args: `{}`,
			action: "task", target: ""},

		// --- agent/team/swarm family ----------------------------------------
		// NOTE: a spawn_agent call with a description never reaches this
		// switch: the universal description block intercepts it first (see
		// TestDescribeToolDescriptionPriority). Only the description-free
		// shapes below hit the branch.
		{name: "spawn_agent default label", tool: "spawn_agent", args: `{"task":"do it"}`,
			want: func(lang Language) toolPresentation {
				label := toolLabelFor(lang, "spawn_agent")
				return toolPresentation{DisplayName: label, Detail: compactSingleLine("do it"), Activity: label}
			}},
		{name: "spawn_agent with model", tool: "spawn_agent", args: `{"task":"do it","model":"m-7"}`,
			want: func(lang Language) toolPresentation {
				label := toolLabelFor(lang, "spawn_agent") + " [m-7]"
				return toolPresentation{DisplayName: label, Detail: compactSingleLine("do it"), Activity: label}
			}},
		{name: "list_agents", tool: "list_agents", args: `{}`,
			want: func(lang Language) toolPresentation {
				label := toolLabelFor(lang, "list_agents")
				return toolPresentation{DisplayName: label, Detail: "", Activity: label}
			}},
		{name: "wait_agent id", tool: "wait_agent", args: `{"agent_id":"agent-abcdef01"}`,
			want: func(lang Language) toolPresentation {
				label := toolLabelFor(lang, "wait_agent")
				return toolPresentation{DisplayName: label, Detail: shortenJobID("agent-abcdef01"), Activity: label}
			}},
		{name: "team_create name", tool: "team_create", args: `{"name":"devs"}`,
			want: func(lang Language) toolPresentation {
				return toolPresentation{
					DisplayName: localizedToolLabel(lang, "team_create"),
					Detail:      "devs",
					Activity:    localizedToolActivity(lang, "team_create", "devs"),
				}
			}},
		{name: "team_delete", tool: "team_delete", args: `{"team_id":"team-1"}`,
			want: func(lang Language) toolPresentation {
				return toolPresentation{
					DisplayName: localizedToolLabel(lang, "team_delete"),
					Detail:      "team-1",
					Activity:    localizedToolActivity(lang, "team_delete", ""),
				}
			}},
		{name: "teammate_list", tool: "teammate_list", args: `{"team_id":"team-2"}`,
			want: func(lang Language) toolPresentation {
				return toolPresentation{
					DisplayName: localizedToolLabel(lang, "teammate_list"),
					Detail:      "team-2",
					Activity:    localizedToolActivity(lang, "teammate_list", ""),
				}
			}},
		{name: "teammate_shutdown", tool: "teammate_shutdown", args: `{"teammate_id":"tm-3"}`,
			want: func(lang Language) toolPresentation {
				return toolPresentation{
					DisplayName: localizedToolLabel(lang, "teammate_shutdown"),
					Detail:      "tm-3",
					Activity:    localizedToolActivity(lang, "teammate_shutdown", "tm-3"),
				}
			}},
		{name: "teammate_results id", tool: "teammate_results", args: `{"teammate_id":"tm-4"}`,
			want: func(lang Language) toolPresentation {
				return toolPresentation{
					DisplayName: localizedToolLabel(lang, "teammate_results"),
					Detail:      displayToolTarget("tm-4"),
					Activity:    localizedToolActivity(lang, "teammate_results", "tm-4"),
				}
			}},
		{name: "teammate_results team fallback", tool: "teammate_results", args: `{"team_id":"team-5"}`,
			want: func(lang Language) toolPresentation {
				return toolPresentation{
					DisplayName: localizedToolLabel(lang, "teammate_results"),
					Detail:      displayToolTarget("team-5"),
					Activity:    localizedToolActivity(lang, "teammate_results", ""),
				}
			}},
		{name: "swarm_task_claim subject", tool: "swarm_task_claim", args: `{"subject":"fix bug"}`,
			want: func(lang Language) toolPresentation {
				return toolPresentation{
					DisplayName: localizedToolLabel(lang, "swarm_task_claim"),
					Detail:      displayToolTarget("fix bug"),
					Activity:    localizedToolActivity(lang, "swarm_task_claim", ""),
				}
			}},
		{name: "swarm_task_complete", tool: "swarm_task_complete", args: `{"task_id":"st-1"}`,
			want: func(lang Language) toolPresentation {
				return toolPresentation{
					DisplayName: localizedToolLabel(lang, "swarm_task_complete"),
					Detail:      "st-1",
					Activity:    localizedToolActivity(lang, "swarm_task_complete", ""),
				}
			}},
		{name: "swarm_task_list", tool: "swarm_task_list", args: `{}`,
			want: func(lang Language) toolPresentation {
				return toolPresentation{
					DisplayName: localizedToolLabel(lang, "swarm_task_list"),
					Detail:      "",
					Activity:    localizedToolActivity(lang, "swarm_task_list", ""),
				}
			}},

		// --- named agents -----------------------------------------------------
		{name: "create_namedagent", tool: "create_namedagent", args: `{"name":"reviewer"}`,
			want: func(lang Language) toolPresentation {
				label := localizedToolLabel(lang, "create_namedagent") + ": reviewer"
				return toolPresentation{DisplayName: label, Detail: "", Activity: label}
			}},
		{name: "delete_namedagent", tool: "delete_namedagent", args: `{"name":"old"}`,
			want: func(lang Language) toolPresentation {
				label := localizedToolLabel(lang, "delete_namedagent") + ": old"
				return toolPresentation{DisplayName: label, Detail: "", Activity: label}
			}},
		{name: "list_namedagent", tool: "list_namedagent", args: `{}`,
			want: func(lang Language) toolPresentation {
				label := localizedToolLabel(lang, "list_namedagent")
				return toolPresentation{DisplayName: label, Detail: "", Activity: label}
			}},

		// --- mcp ----------------------------------------------------------------
		{name: "list_mcp_capabilities server", tool: "list_mcp_capabilities", args: `{"server":"github"}`,
			want: func(lang Language) toolPresentation {
				return toolPresentation{
					DisplayName: localizedToolLabel(lang, "list_mcp_capabilities"),
					Detail:      "github",
					Activity:    localizedToolActivity(lang, "list_mcp_capabilities", ""),
				}
			}},
		{name: "get_mcp_prompt name", tool: "get_mcp_prompt", args: `{"name":"review"}`,
			want: func(lang Language) toolPresentation {
				return toolPresentation{
					DisplayName: localizedToolLabel(lang, "get_mcp_prompt"),
					Detail:      "review",
					Activity:    localizedToolActivity(lang, "get_mcp_prompt", "review"),
				}
			}},
		{name: "read_mcp_resource uri", tool: "read_mcp_resource", args: `{"uri":"file:///x"}`,
			want: func(lang Language) toolPresentation {
				return toolPresentation{
					DisplayName: localizedToolLabel(lang, "read_mcp_resource"),
					Detail:      "file:///x",
					Activity:    localizedToolActivity(lang, "read_mcp_resource", ""),
				}
			}},

		// --- a2a ------------------------------------------------------------------
		{name: "a2a_remote target", tool: "a2a_remote", args: `{"target":"peer-1"}`,
			want: func(lang Language) toolPresentation {
				return toolPresentation{
					DisplayName: localizedToolLabel(lang, "a2a_remote"),
					Detail:      "peer-1",
					Activity:    localizedToolActivity(lang, "a2a_remote", "peer-1"),
				}
			}},
		{name: "a2a_discover", tool: "a2a_discover", args: `{}`,
			want: func(lang Language) toolPresentation {
				return toolPresentation{
					DisplayName: localizedToolLabel(lang, "a2a_discover"),
					Detail:      "",
					Activity:    localizedToolActivity(lang, "a2a_discover", ""),
				}
			}},
		{name: "a2a_send_task target", tool: "a2a_send_task", args: `{"target":"peer-2"}`,
			want: func(lang Language) toolPresentation {
				return toolPresentation{
					DisplayName: localizedToolLabel(lang, "a2a_send_task"),
					Detail:      "peer-2",
					Activity:    localizedToolActivity(lang, "a2a_send_task", "peer-2"),
				}
			}},
		{name: "a2a_get_task", tool: "a2a_get_task", args: `{}`,
			want: func(lang Language) toolPresentation {
				return toolPresentation{
					DisplayName: localizedToolLabel(lang, "a2a_get_task"),
					Detail:      "",
					Activity:    localizedToolActivity(lang, "a2a_get_task", ""),
				}
			}},
		{name: "a2a_list_tasks", tool: "a2a_list_tasks", args: `{}`,
			want: func(lang Language) toolPresentation {
				return toolPresentation{
					DisplayName: localizedToolLabel(lang, "a2a_list_tasks"),
					Detail:      "",
					Activity:    localizedToolActivity(lang, "a2a_list_tasks", ""),
				}
			}},
		{name: "a2a_cancel_task", tool: "a2a_cancel_task", args: `{}`,
			want: func(lang Language) toolPresentation {
				return toolPresentation{
					DisplayName: localizedToolLabel(lang, "a2a_cancel_task"),
					Detail:      "",
					Activity:    localizedToolActivity(lang, "a2a_cancel_task", ""),
				}
			}},
	}

	for _, lang := range describeToolLangs {
		for _, tc := range cases {
			t.Run(tc.name+" ["+string(lang)+"]", func(t *testing.T) {
				got := describeTool(lang, tc.tool, tc.args)
				want := toolPresentationFor(lang, tc.action, tc.target)
				if tc.want != nil {
					want = tc.want(lang)
				}
				if got != want {
					t.Fatalf("describeTool(%s, %q, %s)\n  got  %+v\n  want %+v", lang, tc.tool, tc.args, got, want)
				}
			})
		}
	}
}

func TestDescribeToolSwarmTaskCreateRouting(t *testing.T) {
	for _, lang := range describeToolLangs {
		// No subject in the raw args: falls through to the switch branch and
		// builds "subject → assignee" detail from parsed args.
		got := describeTool(lang, "swarm_task_create", `{"assignee":"peer"}`)
		want := toolPresentation{
			DisplayName: localizedToolLabel(lang, "swarm_task_create"),
			Detail:      " → peer",
			Activity:    localizedToolActivity(lang, "swarm_task_create", " → peer"),
		}
		if got != want {
			t.Fatalf("assignee-only swarm_task_create [%s]: got %+v, want %+v", lang, got, want)
		}
	}
}

func TestDescribeToolDescriptionPriority(t *testing.T) {
	type dpCase struct {
		name       string
		tool       string
		args       string
		wantName   string
		wantDetail string
	}
	cases := []dpCase{
		{name: "description with file target", tool: "edit_file",
			args:       `{"description":"apply fix","file_path":"a/b.go","old_text":"x"}`,
			wantName:   "apply fix (Edit File)",
			wantDetail: displayToolFileTarget("a/b.go")},
		{name: "description falls back to command", tool: "run_command",
			args:       `{"description":"run checks","command":"go vet ./..."}`,
			wantName:   "run checks (Bash)",
			wantDetail: displayToolTarget("go vet ./...")},
		{name: "description falls back to path", tool: "mystery",
			args:       `{"description":"probe","path":"p/q.txt"}`,
			wantName:   "probe (Mystery)",
			wantDetail: displayToolTarget(displayToolFileTarget("p/q.txt"))},
		{name: "description falls back to query", tool: "web_search",
			args:       `{"description":"lookup","query":"go release notes"}`,
			wantName:   "lookup (Web Search)",
			wantDetail: displayToolTarget("go release notes")},
		{name: "description falls back to pattern", tool: "grep",
			args:       `{"description":"scan","pattern":"TODO"}`,
			wantName:   "scan (Grep)",
			wantDetail: displayToolTarget("TODO")},
		{name: "description only", tool: "unknown_tool",
			args:       `{"description":"just saying"}`,
			wantName:   "just saying (Unknown Tool)",
			wantDetail: ""},
		{name: "spawn agent description intercepts switch", tool: "spawn_agent",
			args:       `{"description":"run review","task":"review code"}`,
			wantName:   "run review (Spawn Agent)",
			wantDetail: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := describeTool(LangEnglish, tc.tool, tc.args)
			if got.DisplayName != tc.wantName {
				t.Fatalf("DisplayName = %q, want %q", got.DisplayName, tc.wantName)
			}
			if got.Detail != tc.wantDetail {
				t.Fatalf("Detail = %q, want %q", got.Detail, tc.wantDetail)
			}
			if got.Activity != strings.TrimSuffix(tc.wantName, " ("+friendlyToolName(tc.tool)+")") {
				t.Fatalf("Activity = %q, want description only", got.Activity)
			}
		})
	}
}

func TestDescribeToolDescriptionSpawnAgentModel(t *testing.T) {
	// Explicit model wins over the resolver.
	got := describeTool(LangEnglish, "spawn_agent", `{"description":"delegate","model":"m-explicit"}`)
	if got.DisplayName != "delegate [m-explicit] (Spawn Agent)" {
		t.Fatalf("explicit model DisplayName = %q", got.DisplayName)
	}
	if got.Activity != "delegate [m-explicit]" {
		t.Fatalf("explicit model Activity = %q", got.Activity)
	}

	// No explicit model: resolver (parent runtime model) is shown instead.
	orig := spawnAgentModelResolver
	spawnAgentModelResolver = func() string { return "m-inherited" }
	t.Cleanup(func() { spawnAgentModelResolver = orig })
	got = describeTool(LangEnglish, "spawn_agent", `{"description":"delegate"}`)
	if got.DisplayName != "delegate [m-inherited] (Spawn Agent)" {
		t.Fatalf("resolver model DisplayName = %q", got.DisplayName)
	}
}

func TestDescribeToolUseNamedAgentModelResolver(t *testing.T) {
	orig := namedAgentModelResolver
	namedAgentModelResolver = func(name string) string {
		if name != "reviewer" {
			return ""
		}
		return "glm-5"
	}
	t.Cleanup(func() { namedAgentModelResolver = orig })

	got := describeTool(LangEnglish, "use_namedagent", `{"name":"reviewer","task":"review\ncode"}`)
	wantLabel := localizedToolLabel(LangEnglish, "use_namedagent") + ": reviewer [glm-5]"
	want := toolPresentation{
		DisplayName: wantLabel,
		Detail:      compactSingleLine("review\ncode"),
		Activity:    wantLabel,
	}
	if got != want {
		t.Fatalf("use_namedagent with resolver: got %+v, want %+v", got, want)
	}

	// No model override from the template: label has no bracket suffix.
	namedAgentModelResolver = func(string) string { return "" }
	got = describeTool(LangEnglish, "use_namedagent", `{"name":"reviewer"}`)
	if strings.Contains(got.DisplayName, "[") {
		t.Fatalf("expected no model bracket, got %q", got.DisplayName)
	}
}

func TestDescribeToolCommandPreview(t *testing.T) {
	for _, lang := range describeToolLangs {
		// Leading # comment becomes the title; remaining lines the detail.
		got := describeTool(lang, "run_command", "{\"command\":\"# Build project\\ngo build ./...\\ngo vet ./...\"}")
		want := toolPresentation{
			DisplayName: displayToolTarget("Build project"),
			Detail:      displayToolTarget("go build ./...") + "; " + displayToolTarget("go vet ./..."),
			Activity:    localizedCommandActivity(lang, displayToolTarget("Build project")),
		}
		if got != want {
			t.Fatalf("comment-titled command [%s]: got %+v, want %+v", lang, got, want)
		}

		// More than 2 detail lines collapses to two lines plus "+N more".
		got = describeTool(lang, "run_command", "{\"command\":\"# Deploy\\nstep1\\nstep2\\nstep3\\nstep4\\nstep5\\nstep6\"}")
		if got.DisplayName != displayToolTarget("Deploy") {
			t.Fatalf("multiline DisplayName = %q", got.DisplayName)
		}
		wantDetail := displayToolTarget("step1") + "; " + displayToolTarget("step2") + "; +4 more"
		if got.Detail != wantDetail {
			t.Fatalf("multiline Detail = %q, want %q", got.Detail, wantDetail)
		}

		// Comment-only command: title shown, empty detail.
		got = describeTool(lang, "start_command", `{"command":"#only a title"}`)
		if got.DisplayName != displayToolTarget("only a title") {
			t.Fatalf("comment-only DisplayName = %q", got.DisplayName)
		}
		if got.Detail != "" {
			t.Fatalf("comment-only Detail = %q, want empty", got.Detail)
		}
	}
}

func TestDescribeToolWriteCommandInput(t *testing.T) {
	long := strings.Repeat("a", 70)
	got := describeTool(LangEnglish, "write_command_input", `{"input":"`+long+`"}`)
	if got.Detail != "→ "+strings.Repeat("a", 57)+"…" {
		t.Fatalf("long ASCII input Detail = %q", got.Detail)
	}

	// CJK runes count double toward the 60-width limit.
	cjk := strings.Repeat("汉", 31)
	got = describeTool(LangEnglish, "write_command_input", `{"input":"`+cjk+`"}`)
	if got.Detail != "→ "+strings.Repeat("汉", 28)+"…" {
		t.Fatalf("CJK input Detail = %q", got.Detail)
	}

	// job_id is shortened and prefixed.
	got = describeTool(LangEnglish, "write_command_input", `{"input":"stop","job_id":"abcdefgh1234"}`)
	if got.Detail != "[abcdefgh] → stop" {
		t.Fatalf("job input Detail = %q", got.Detail)
	}

	// No input: falls back to job id or the generic background label.
	got = describeTool(LangEnglish, "write_command_input", `{"job_id":"job-77"}`)
	want := toolPresentationFor(LangEnglish, "input", displayToolTarget("job-77"))
	if got != want {
		t.Fatalf("job-only input: got %+v, want %+v", got, want)
	}
	got = describeTool(LangEnglish, "write_command_input", `{}`)
	want = toolPresentationFor(LangEnglish, "input", displayToolTarget("background command"))
	if got != want {
		t.Fatalf("empty input: got %+v, want %+v", got, want)
	}
}

func TestDescribeToolSleepDurations(t *testing.T) {
	cases := []struct {
		name string
		args string
		want string
	}{
		{name: "seconds", args: `{"seconds":"90"}`, want: "1m30s"},
		{name: "subsecond", args: `{"seconds":"0","milliseconds":"500"}`, want: "500ms"},
		{name: "mixed", args: `{"seconds":"1","milliseconds":"50"}`, want: "1.05s"},
		{name: "zero", args: `{}`, want: "0s"},
		{name: "negative clamps to zero", args: `{"seconds":"-5"}`, want: "0s"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := describeTool(LangEnglish, "sleep", tc.args)
			want := toolPresentation{
				DisplayName: localizedToolLabel(LangEnglish, "sleep"),
				Detail:      tc.want,
				Activity:    localizedToolActivity(LangEnglish, "sleep", tc.want),
			}
			if got != want {
				t.Fatalf("sleep(%s): got %+v, want %+v", tc.args, got, want)
			}
		})
	}
}

func TestDescribeToolCronAndWorktreeAndMemory(t *testing.T) {
	cases := []struct {
		name  string
		tool  string
		args  string
		label string
		want  toolPresentation
	}{
		{name: "cron_create", tool: "cron_create", args: `{"cron":"*/5 * * * *"}`, label: "cron_create",
			want: toolPresentation{Detail: "*/5 * * * *"}},
		{name: "cron_update", tool: "cron_update", args: `{"jobId":"cron-3"}`, label: "cron_update",
			want: toolPresentation{Detail: "cron-3"}},
		{name: "cron_pause", tool: "cron_pause", args: `{"jobId":"cron-3"}`, label: "cron_pause",
			want: toolPresentation{Detail: "cron-3"}},
		{name: "cron_resume", tool: "cron_resume", args: `{"jobId":"cron-4"}`, label: "cron_resume",
			want: toolPresentation{Detail: "cron-4"}},
		{name: "cron_get", tool: "cron_get", args: `{"jobId":"cron-5"}`, label: "cron_get",
			want: toolPresentation{Detail: "cron-5"}},
		{name: "enter_worktree", tool: "enter_worktree", args: `{"name":"feature"}`, label: "enter_worktree",
			want: toolPresentation{Detail: "feature"}},
		{name: "exit_worktree", tool: "exit_worktree", args: `{"action":"keep"}`, label: "exit_worktree",
			want: toolPresentation{Detail: "keep"}},
		{name: "save_memory", tool: "save_memory", args: `{"key":"k1"}`, label: "save_memory",
			want: toolPresentation{Detail: "k1"}},
		{name: "delete_memory", tool: "delete_memory", args: `{"key":"k2"}`, label: "delete_memory",
			want: toolPresentation{Detail: "k2"}},
		{name: "config with value", tool: "config", args: `{"setting":"model","value":"gpt-5"}`, label: "config",
			want: toolPresentation{Detail: "model = gpt-5"}},
		{name: "config without value", tool: "config", args: `{"setting":"scope"}`, label: "config",
			want: toolPresentation{Detail: "scope"}},
	}
	for _, lang := range describeToolLangs {
		for _, tc := range cases {
			t.Run(tc.name+" ["+string(lang)+"]", func(t *testing.T) {
				got := describeTool(lang, tc.tool, tc.args)
				want := tc.want
				want.DisplayName = localizedToolLabel(lang, tc.label)
				want.Activity = localizedToolActivity(lang, tc.label, want.Detail)
				if got != want {
					t.Fatalf("describeTool(%s, %q, %s): got %+v, want %+v", lang, tc.tool, tc.args, got, want)
				}
			})
		}
	}
}

func TestDescribeToolSendMessageTruncation(t *testing.T) {
	for _, lang := range describeToolLangs {
		// Short message: "to: message".
		got := describeTool(lang, "send_message", `{"to":"bob","message":"hello"}`)
		want := toolPresentation{
			DisplayName: localizedToolLabel(lang, "send_message"),
			Detail:      "bob: hello",
			Activity:    localizedToolActivity(lang, "send_message", "bob"),
		}
		if got != want {
			t.Fatalf("short send_message [%s]: got %+v, want %+v", lang, got, want)
		}

		// Long message truncates at width 37 (runes) with an ellipsis;
		// Activity still carries only the recipient.
		long := strings.Repeat("a", 50)
		got = describeTool(lang, "send_message", `{"to":"bob","message":"`+long+`"}`)
		want = toolPresentation{
			DisplayName: localizedToolLabel(lang, "send_message"),
			Detail:      "bob: " + strings.Repeat("a", 37) + "…",
			Activity:    localizedToolActivity(lang, "send_message", "bob"),
		}
		if got != want {
			t.Fatalf("long send_message [%s]: got %+v, want %+v", lang, got, want)
		}

		// Recipient only.
		got = describeTool(lang, "send_message", `{"to":"carol"}`)
		want = toolPresentation{
			DisplayName: localizedToolLabel(lang, "send_message"),
			Detail:      "carol",
			Activity:    localizedToolActivity(lang, "send_message", "carol"),
		}
		if got != want {
			t.Fatalf("recipient-only send_message [%s]: got %+v, want %+v", lang, got, want)
		}
	}
}

func TestDescribeToolTeammateSpawnAndSwarmCreate(t *testing.T) {
	for _, lang := range describeToolLangs {
		// teammate_spawn without name: bare label.
		got := describeTool(lang, "teammate_spawn", `{}`)
		want := toolPresentation{
			DisplayName: localizedToolLabel(lang, "teammate_spawn"),
			Detail:      "",
			Activity:    localizedToolActivity(lang, "teammate_spawn", ""),
		}
		if got != want {
			t.Fatalf("teammate_spawn bare [%s]: got %+v, want %+v", lang, got, want)
		}

		// teammate_spawn with name: "label: name".
		got = describeTool(lang, "teammate_spawn", `{"name":"builder"}`)
		want = toolPresentation{
			DisplayName: localizedToolLabel(lang, "teammate_spawn") + ": builder",
			Detail:      "builder",
			Activity:    localizedToolActivity(lang, "teammate_spawn", "builder"),
		}
		if got != want {
			t.Fatalf("teammate_spawn named [%s]: got %+v, want %+v", lang, got, want)
		}

		// A raw subject short-circuits describeTool before the switch:
		// DisplayName/Activity become the subject itself, Detail stays empty,
		// and the "subject → assignee" switch formatting is unreachable.
		got = describeTool(lang, "swarm_task_create", `{"subject":"tests","assignee":"qa"}`)
		want = toolPresentation{
			DisplayName: "tests",
			Detail:      "",
			Activity:    "tests",
		}
		if got != want {
			t.Fatalf("swarm_task_create raw subject [%s]: got %+v, want %+v", lang, got, want)
		}
	}
}

func TestDescribeToolLspFallback(t *testing.T) {
	for _, lang := range describeToolLangs {
		got := describeTool(lang, "lsp_hover", `{"path":"a/b.go","line":"42"}`)
		want := toolPresentation{
			DisplayName: "LSP",
			Detail:      displayToolFileTarget("a/b.go") + ":42",
			Activity:    "LSP hover " + displayToolFileTarget("a/b.go") + ":42",
		}
		if got != want {
			t.Fatalf("lsp_hover [%s]: got %+v, want %+v", lang, got, want)
		}

		got = describeTool(lang, "lsp_rename", `{"path":"x.go","new_name":"y.go"}`)
		want = toolPresentation{
			DisplayName: "LSP",
			Detail:      displayToolFileTarget("x.go") + " → y.go",
			Activity:    "LSP rename " + displayToolFileTarget("x.go") + " → y.go",
		}
		if got != want {
			t.Fatalf("lsp_rename [%s]: got %+v, want %+v", lang, got, want)
		}

		got = describeTool(lang, "lsp_workspace_symbols", `{"query":"foo"}`)
		want = toolPresentation{
			DisplayName: "LSP",
			Detail:      "",
			Activity:    "LSP workspace symbols",
		}
		if got != want {
			t.Fatalf("lsp_workspace_symbols [%s]: got %+v, want %+v", lang, got, want)
		}

		// Unmapped lsp_ tool derives the action from the name suffix.
		got = describeTool(lang, "lsp_future_thing", `{"path":"z.go"}`)
		want = toolPresentation{
			DisplayName: "LSP",
			Detail:      displayToolFileTarget("z.go"),
			Activity:    "LSP future_thing " + displayToolFileTarget("z.go"),
		}
		if got != want {
			t.Fatalf("lsp_future_thing [%s]: got %+v, want %+v", lang, got, want)
		}
	}
}

func TestDescribeToolUnknownFallbackDetailChain(t *testing.T) {
	cases := []struct {
		name       string
		tool       string
		args       string
		wantDetail string
	}{
		{name: "path via file target", tool: "frobnicate", args: `{"path":"p.txt"}`,
			wantDetail: displayToolTarget(displayToolFileTarget("p.txt"))},
		{name: "url", tool: "fetcher", args: `{"url":"https://x.io"}`,
			wantDetail: displayToolTarget("https://x.io")},
		{name: "input", tool: "feeder", args: `{"input":"go"}`,
			wantDetail: displayToolTarget("go")},
		// NOTE: description-only args never reach the default case - the
		// universal description block returns first.
		{name: "nothing", tool: "empty_tool", args: `{}`, wantDetail: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := describeTool(LangEnglish, tc.tool, tc.args)
			pretty := prettifyToolName(tc.tool)
			want := toolPresentation{
				DisplayName: pretty,
				Detail:      tc.wantDetail,
				Activity:    localizedGenericActivity(LangEnglish, pretty),
			}
			if got != want {
				t.Fatalf("unknown fallback %q: got %+v, want %+v", tc.tool, got, want)
			}
		})
	}
}

// TestDescribeToolFamilyFallbacks pins the per-family total-function
// contract: every family renderer delegates unmatched tool names to
// describeUnknownFamilyTool, so a name added to describeTool's dispatch
// switch without a matching family case degrades to the generic unknown
// presentation (never a zero value).
func TestDescribeToolFamilyFallbacks(t *testing.T) {
	families := map[string]func(Language, string, map[string]any, string) toolPresentation{
		"file":    describeFileFamilyTool,
		"command": describeCommandFamilyTool,
		"jobs":    describeJobsFamilyTool,
		"web":     describeWebFamilyTool,
		"git":     describeGitFamilyTool,
		"cron":    describeCronFamilyTool,
		"session": describeSessionFamilyTool,
		"task":    describeTaskFamilyTool,
		"agent":   describeAgentFamilyTool,
		"team":    describeTeamFamilyTool,
		"mcp":     describeMCPFamilyTool,
		"a2a":     describeA2AFamilyTool,
	}
	for _, lang := range describeToolLangs {
		for name, family := range families {
			got := family(lang, "totally_bogus_tool", map[string]any{}, "")
			want := describeTool(lang, "totally_bogus_tool", `{}`)
			if got != want {
				t.Fatalf("%s family fallback [%s]: got %+v, want %+v", name, lang, got, want)
			}
		}
	}
}
