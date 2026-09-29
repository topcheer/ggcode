// Package toollabel provides the shared, table-driven localization core for
// tool labels and activity strings rendered by the TUI and IM frontends.
//
// Extracted (r251) from two near-identical dispatch switches that had begun
// to drift: internal/tui/tool_labels_helpers.go (localizedToolActivity
// CC 166 / localizedToolLabel CC 142) and internal/im/tool_status.go
// (CC 58 / CC 30, self-described as "mirroring the TUI's describeTool
// pipeline"). Both frontends now delegate here, so adding an action key
// happens in exactly one place. Output is pinned byte-for-byte by the
// callers' existing golden tests plus the parity tests in this package.
//
// The tables carry the TUI superset: the IM frontend only ever requests the
// core file/command/search actions, all of which are byte-identical in both
// legacy implementations (verified entry-by-entry before extraction).
package toollabel

import (
	"strings"
	"unicode"
)

// Lang enumerates the languages the label tables support. The TUI and IM
// frontends are binary zh-CN vs en at this layer: every other frontend
// language (de, ja, fr, ...) renders the English strings, exactly as the
// legacy switches' default branches did.
type Lang int

const (
	// En renders the English strings (legacy default branch).
	En Lang = iota
	// ZhCN renders the Simplified Chinese strings.
	ZhCN
)

// Label returns the localized short label for a tool action
// ("read" -> "读"/"Read"). Unknown actions fall back to GenericToolName,
// matching the legacy switch-and-fallback behavior.
func Label(lang Lang, action string) string {
	if s, ok := labels[lang][action]; ok {
		return s
	}
	return GenericToolName(lang, action)
}

// Activity returns the localized in-progress activity string for a tool
// action. With a target it renders the "读取 x"/"Reading x" prefix form;
// without one it renders the "读取文件"/"Reading file" form. Unknown
// actions fall back to GenericActivity, matching the legacy behavior.
func Activity(lang Lang, action, target string) string {
	if target == "" {
		if s, ok := activityNoTarget[lang][action]; ok {
			return s
		}
	}
	if prefix, ok := activityWithTarget[lang][action]; ok {
		return prefix + target
	}
	return GenericActivity(lang, target)
}

// GenericActivity renders the fallback activity for an unrecognized label
// ("运行 x"/"Running x").
func GenericActivity(lang Lang, label string) string {
	if lang == ZhCN {
		return "运行 " + label
	}
	return "Running " + label
}

// GenericToolName renders the fallback tool name: zh-CN keeps the raw name
// with underscores replaced by spaces, other languages prettify
// (capitalize each word).
func GenericToolName(lang Lang, name string) string {
	if lang == ZhCN {
		return strings.ReplaceAll(name, "_", " ")
	}
	return PrettifyToolName(name)
}

// PrettifyToolName turns "spawn_agent" into "Spawn Agent".
func PrettifyToolName(name string) string {
	name = strings.ReplaceAll(name, "-", " ")
	name = strings.ReplaceAll(name, "_", " ")
	parts := strings.Fields(name)
	for i, part := range parts {
		if part == "" {
			continue
		}
		runes := []rune(part)
		runes[0] = unicode.ToUpper(runes[0])
		parts[i] = string(runes)
	}
	return strings.Join(parts, " ")
}

// labels maps action -> short label per language
// (legacy tui localizedToolLabel / im localizedToolLabel).
var labels = map[Lang]map[string]string{
	ZhCN: {
		"read":                  "读",
		"edit":                  "编辑",
		"create":                "创建",
		"write":                 "写",
		"search":                "搜索",
		"find":                  "查找",
		"list":                  "列出",
		"run":                   "执行",
		"run_in_background":     "后台运行",
		"fetch":                 "抓取",
		"todo":                  "更新待办",
		"task":                  "执行任务",
		"skill":                 "使用技能",
		"save_memory":           "保存记忆",
		"delete":                "删除",
		"delete_memory":         "删除记忆",
		"sleep":                 "等待",
		"cron_create":           "创建定时",
		"cron_update":           "更新定时",
		"cron_pause":            "暂停定时",
		"cron_resume":           "恢复定时",
		"cron_get":              "查看定时",
		"config":                "配置",
		"enter_worktree":        "进入工作树",
		"exit_worktree":         "退出工作树",
		"send_message":          "发送消息",
		"enter_plan":            "制定计划",
		"exit_plan":             "计划",
		"team_create":           "创建团队",
		"team_delete":           "删除团队",
		"teammate_spawn":        "添加成员",
		"teammate_list":         "成员列表",
		"teammate_shutdown":     "停止成员",
		"teammate_results":      "成员结果",
		"swarm_task_create":     "创建任务",
		"swarm_task_claim":      "领取任务",
		"swarm_task_complete":   "完成任务",
		"swarm_task_list":       "任务列表",
		"list_mcp_capabilities": "MCP 服务器",
		"get_mcp_prompt":        "获取 MCP 提示",
		"read_mcp_resource":     "读取 MCP 资源",
		"ask":                   "提问",
		"inspect":               "检查",
		"input":                 "输入",
		"output":                "读取输出",
		"wait":                  "等待",
		"stop":                  "停止",
		"list_jobs":             "后台任务",
		"diff":                  "差异",
		"log":                   "日志",
		"show":                  "查看",
		"blame":                 "溯源",
		"branches":              "分支",
		"remote":                "远程",
		"stash":                 "暂存",
		"stage":                 "暂存文件",
		"commit":                "提交",
		"spawn_agent":           "启动子代理", // #917: was untranslated English
		"list_agents":           "获取子代理列表",
		"wait_agent":            "检查子代理进度", // #917: was untranslated English
		"a2a_remote":            "远程调用",
		"a2a_discover":          "发现代理",
		"a2a_send_task":         "发送任务",
		"a2a_get_task":          "获取任务",
		"a2a_list_tasks":        "任务列表",
		"a2a_cancel_task":       "取消任务",
		"use_namedagent":        "运行命名代理",
		"create_namedagent":     "创建命名代理",
		"delete_namedagent":     "删除命名代理",
		"list_namedagent":       "命名代理列表",
	},
	En: {
		"read":                  "Read",
		"edit":                  "Edit",
		"create":                "Create",
		"write":                 "Write",
		"search":                "Search",
		"find":                  "Find",
		"list":                  "List",
		"run":                   "Run",
		"run_in_background":     "Run in background",
		"fetch":                 "Fetch",
		"todo":                  "Update todos",
		"task":                  "Run task",
		"skill":                 "Using Skill",
		"save_memory":           "Save Memory",
		"delete":                "Delete",
		"delete_memory":         "Delete Memory",
		"sleep":                 "Sleep",
		"cron_create":           "Create Cron",
		"cron_update":           "Update Cron",
		"cron_pause":            "Pause Cron",
		"cron_resume":           "Resume Cron",
		"cron_get":              "Inspect Cron",
		"config":                "Config",
		"enter_worktree":        "Enter Worktree",
		"exit_worktree":         "Exit Worktree",
		"send_message":          "Message",
		"enter_plan":            "Planning",
		"exit_plan":             "Plan",
		"team_create":           "Create Team",
		"team_delete":           "Delete Team",
		"teammate_spawn":        "Add Teammate",
		"teammate_list":         "List Teammates",
		"teammate_shutdown":     "Shutdown Teammate",
		"teammate_results":      "Teammate Results",
		"swarm_task_create":     "Create Task",
		"swarm_task_claim":      "Claim Task",
		"swarm_task_complete":   "Complete Task",
		"swarm_task_list":       "Task List",
		"list_mcp_capabilities": "MCP Servers",
		"get_mcp_prompt":        "Get MCP Prompt",
		"read_mcp_resource":     "Read MCP Resource",
		"ask":                   "Ask",
		"inspect":               "Inspect",
		"input":                 "Input",
		"output":                "Read Output",
		"wait":                  "Wait",
		"stop":                  "Stop",
		"list_jobs":             "List Jobs",
		"diff":                  "Diff",
		"log":                   "Log",
		"show":                  "Show",
		"blame":                 "Blame",
		"branches":              "Branches",
		"remote":                "Remote",
		"stash":                 "Stash",
		"stage":                 "Stage",
		"commit":                "Commit",
		"spawn_agent":           "Starting subagent",
		"list_agents":           "List Agents",
		"wait_agent":            "Checking subagent progress",
		"a2a_remote":            "Remote Call",
		"a2a_discover":          "Discover",
		"a2a_send_task":         "Send Task",
		"a2a_get_task":          "Get Task",
		"a2a_list_tasks":        "List Tasks",
		"a2a_cancel_task":       "Cancel Task",
		"use_namedagent":        "Run Agent",
		"create_namedagent":     "Create Agent",
		"delete_namedagent":     "Delete Agent",
		"list_namedagent":       "List Agents",
	},
}

// activityNoTarget maps action -> activity string used when the tool call
// has no displayable target (legacy tui/im localizedToolActivity, first tier).
var activityNoTarget = map[Lang]map[string]string{
	ZhCN: {
		"read":              "读取文件",
		"edit":              "编辑文件",
		"create":            "创建文件",
		"write":             "写入文件",
		"search":            "搜索中...",
		"find":              "查找文件",
		"list":              "列出目录",
		"run":               "执行命令",
		"run_in_background": "后台运行命令",
		"fetch":             "抓取网页",
		"todo":              "更新待办",
		"task":              "执行任务",
		// #1766 case 2: git sub-command actions with no target used to
		// fall to generic and render "Running " with a trailing space.
		"diff":                  "查看差异",
		"log":                   "查看提交历史",
		"show":                  "查看对象",
		"blame":                 "追溯行历史",
		"branches":              "列出分支",
		"remote":                "查看远程仓库",
		"stash":                 "管理贮藏",
		"stage":                 "暂存变更",
		"commit":                "提交变更",
		"skill":                 "加载技能",
		"save_memory":           "保存记忆中...",
		"delete":                "删除中...",
		"delete_memory":         "删除记忆中...",
		"sleep":                 "等待中...",
		"cron_create":           "创建定时任务...",
		"cron_update":           "更新定时任务...",
		"cron_pause":            "暂停定时任务...",
		"cron_resume":           "恢复定时任务...",
		"cron_get":              "查看定时任务...",
		"config":                "更新配置...",
		"enter_worktree":        "创建工作树...",
		"exit_worktree":         "退出工作树...",
		"send_message":          "发送消息...",
		"enter_plan":            "制定计划中...",
		"exit_plan":             "完成计划...",
		"team_create":           "创建团队中...",
		"team_delete":           "删除团队中...",
		"teammate_spawn":        "添加成员中...",
		"teammate_list":         "查看成员列表...",
		"teammate_shutdown":     "停止成员中...",
		"teammate_results":      "获取成员结果...",
		"swarm_task_create":     "创建任务中...",
		"swarm_task_claim":      "领取任务中...",
		"swarm_task_complete":   "完成任务中...",
		"swarm_task_list":       "查看任务列表...",
		"list_mcp_capabilities": "查看 MCP 服务器...",
		"get_mcp_prompt":        "获取 MCP 提示...",
		"read_mcp_resource":     "读取 MCP 资源...",
		"ask":                   "等待用户输入",
		"inspect":               "检查中...",
		"input":                 "发送输入",
		"output":                "读取输出",
		"wait":                  "等待命令",
		"stop":                  "停止命令",
		"list_jobs":             "列出后台任务",
		"a2a_remote":            "正在远程调用...",
		"a2a_discover":          "正在发现...",
		"a2a_send_task":         "正在发送任务...",
		"a2a_get_task":          "正在获取任务...",
		"a2a_list_tasks":        "正在列出任务...",
		"a2a_cancel_task":       "正在取消任务...",
		"use_namedagent":        "正在运行命名代理...",
		"create_namedagent":     "正在创建命名代理...",
		"delete_namedagent":     "正在删除命名代理...",
		"list_namedagent":       "正在列出命名代理...",
	},
	En: {
		"read":                  "Reading file",
		"edit":                  "Editing file",
		"create":                "Creating file",
		"write":                 "Writing file",
		"search":                "Searching...",
		"find":                  "Finding files",
		"list":                  "Listing directory",
		"run":                   "Running command",
		"run_in_background":     "Running command in background",
		"fetch":                 "Fetching page",
		"todo":                  "Updating todos",
		"task":                  "Running task",
		"diff":                  "Diffing",
		"log":                   "Reading commit history",
		"show":                  "Showing object",
		"blame":                 "Blaming",
		"branches":              "Listing branches",
		"remote":                "Inspecting remotes",
		"stash":                 "Managing stash",
		"stage":                 "Staging changes",
		"commit":                "Committing",
		"skill":                 "Loading skill",
		"save_memory":           "Saving memory...",
		"delete":                "Deleting...",
		"delete_memory":         "Deleting memory...",
		"sleep":                 "Sleeping...",
		"cron_create":           "Scheduling...",
		"cron_update":           "Updating cron job...",
		"cron_pause":            "Pausing cron job...",
		"cron_resume":           "Resuming cron job...",
		"cron_get":              "Inspecting cron job...",
		"config":                "Updating config...",
		"enter_worktree":        "Creating worktree...",
		"exit_worktree":         "Exiting worktree...",
		"send_message":          "Sending message...",
		"enter_plan":            "Planning...",
		"exit_plan":             "Completing plan...",
		"team_create":           "Creating team...",
		"team_delete":           "Deleting team...",
		"teammate_spawn":        "Adding teammate...",
		"teammate_list":         "Listing teammates...",
		"teammate_shutdown":     "Shutting down teammate...",
		"teammate_results":      "Fetching results...",
		"swarm_task_create":     "Creating task...",
		"swarm_task_claim":      "Claiming task...",
		"swarm_task_complete":   "Completing task...",
		"swarm_task_list":       "Listing tasks...",
		"list_mcp_capabilities": "Listing MCP servers...",
		"get_mcp_prompt":        "Fetching MCP prompt...",
		"read_mcp_resource":     "Reading MCP resource...",
		"ask":                   "Waiting for user input",
		"inspect":               "Inspecting...",
		"input":                 "Sending input",
		"output":                "Reading output",
		"wait":                  "Waiting for command",
		"stop":                  "Stopping command",
		"list_jobs":             "Listing background jobs",
		"a2a_remote":            "Calling remote...",
		"a2a_discover":          "Discovering...",
		"a2a_send_task":         "Sending task...",
		"a2a_get_task":          "Getting task...",
		"a2a_list_tasks":        "Listing tasks...",
		"a2a_cancel_task":       "Canceling task...",
		"use_namedagent":        "Running named agent...",
		"create_namedagent":     "Creating named agent...",
		"delete_namedagent":     "Deleting named agent...",
		"list_namedagent":       "Listing named agents...",
	},
}

// activityWithTarget maps action -> target prefix for the "读取 x"/"Reading
// x" form (legacy tui/im localizedToolActivity, second tier). The value
// INCLUDES the trailing space, byte-preserving the legacy concatenation.
var activityWithTarget = map[Lang]map[string]string{
	ZhCN: {
		"read":              "读取 ",
		"edit":              "编辑 ",
		"create":            "创建 ",
		"write":             "写入 ",
		"search":            "搜索 ",
		"find":              "查找 ",
		"list":              "列出 ",
		"run":               "执行 ",
		"run_in_background": "后台运行 ",
		"fetch":             "抓取 ",
		"task":              "执行任务 ",
		"skill":             "加载技能 ",
		"ask":               "提问 ",
		"inspect":           "检查 ",
	},
	En: {
		"read":              "Reading ",
		"edit":              "Editing ",
		"create":            "Creating ",
		"write":             "Writing ",
		"search":            "Searching ",
		"find":              "Finding ",
		"list":              "Listing ",
		"run":               "Running ",
		"run_in_background": "Running in background ",
		"fetch":             "Fetching ",
		"task":              "Running task ",
		"skill":             "Loading skill ",
		"ask":               "Asking ",
		"inspect":           "Inspecting ",
	},
}
