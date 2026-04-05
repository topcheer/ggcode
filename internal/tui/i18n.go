package tui

import (
	"fmt"
	"strings"
)

type Language string

const (
	LangEnglish Language = "en"
	LangZhCN    Language = "zh-CN"
)

func normalizeLanguage(s string) Language {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "zh", "zh-cn", "zh_hans", "zh-hans", "cn", "zh-sg":
		return LangZhCN
	default:
		return LangEnglish
	}
}

func (m Model) currentLanguage() Language {
	if m.language != "" {
		return m.language
	}
	return LangEnglish
}

func (m Model) t(key string, args ...any) string {
	return tr(m.currentLanguage(), key, args...)
}

func (m *Model) setLanguage(lang string) {
	m.language = normalizeLanguage(lang)
	if m.config != nil {
		m.config.Language = string(m.language)
	}
	m.input.Placeholder = m.t("input.placeholder")
	m.approvalOptions = defaultApprovalOptionsFor(m.currentLanguage())
	m.diffOptions = diffConfirmOptionsFor(m.currentLanguage())
}

func (m Model) languageLabel() string {
	switch m.currentLanguage() {
	case LangZhCN:
		return "简体中文"
	default:
		return "English"
	}
}

func supportedLanguageUsage(lang Language) string {
	if lang == LangZhCN {
		return "支持: en, zh-CN"
	}
	return "Supported: en, zh-CN"
}

func languageSwitchLabel(lang Language) string {
	if lang == LangZhCN {
		return "切换界面语言"
	}
	return "Switch interface language"
}

func localizeSlashDescription(lang Language, cmd string) string {
	switch cmd {
	case "/help", "/?":
		return tr(lang, "slash.help")
	case "/cost":
		return tr(lang, "slash.cost")
	case "/sessions":
		return tr(lang, "slash.sessions")
	case "/resume":
		return tr(lang, "slash.resume")
	case "/export":
		return tr(lang, "slash.export")
	case "/model":
		return tr(lang, "slash.model")
	case "/provider":
		return tr(lang, "slash.provider")
	case "/clear":
		return tr(lang, "slash.clear")
	case "/mcp":
		return tr(lang, "slash.mcp")
	case "/memory":
		return tr(lang, "slash.memory")
	case "/undo":
		return tr(lang, "slash.undo")
	case "/checkpoints":
		return tr(lang, "slash.checkpoints")
	case "/allow":
		return tr(lang, "slash.allow")
	case "/plugins":
		return tr(lang, "slash.plugins")
	case "/image":
		return tr(lang, "slash.image")
	case "/fullscreen":
		return tr(lang, "slash.fullscreen")
	case "/mode":
		return tr(lang, "slash.mode")
	case "/lang":
		return tr(lang, "slash.lang")
	case "/exit", "/quit":
		return tr(lang, "slash.exit")
	case "/agents":
		return tr(lang, "slash.agents")
	case "/agent":
		return tr(lang, "slash.agent")
	case "/compact":
		return tr(lang, "slash.compact")
	case "/todo":
		return tr(lang, "slash.todo")
	case "/bug":
		return tr(lang, "slash.bug")
	case "/config":
		return tr(lang, "slash.config")
	case "/status":
		return tr(lang, "slash.status")
	default:
		return cmd
	}
}

func tr(lang Language, key string, args ...any) string {
	var msg string
	switch lang {
	case LangZhCN:
		msg = zhCatalog(key)
	default:
		msg = enCatalog(key)
	}
	if len(args) == 0 {
		return msg
	}
	return fmt.Sprintf(msg, args...)
}

func enCatalog(key string) string {
	switch key {
	case "workspace.tagline":
		return "geek AI workspace"
	case "header.terminal_native":
		return "terminal-native AI coding"
	case "session.ephemeral":
		return "ephemeral"
	case "agents.idle":
		return "idle"
	case "agents.running":
		return "%d running"
	case "activity.idle":
		return "idle"
	case "panel.conversation":
		return "Conversation"
	case "panel.composer":
		return "Composer"
	case "panel.composer_locked":
		return "Composer locked"
	case "panel.commands":
		return "Commands:"
	case "panel.files":
		return "Files:"
	case "panel.agent_status":
		return "Agent status"
	case "panel.approval_required":
		return "Approval required"
	case "panel.bypass_approval":
		return "Bypass mode approval"
	case "panel.review_file_change":
		return "Review file change"
	case "label.provider":
		return "provider"
	case "label.model":
		return "model"
	case "label.mode":
		return "mode"
	case "label.session":
		return "session"
	case "label.agents":
		return "agents"
	case "label.activity":
		return "activity"
	case "label.tool":
		return "tool"
	case "label.input":
		return "input"
	case "label.file":
		return "file"
	case "label.directory":
		return "directory"
	case "empty.ask":
		return "Ask for a refactor, bug fix, explanation, or tests."
	case "empty.tips":
		return "Tips: use @path to include files, /? for help, and Shift+Tab to change mode."
	case "hint.autocomplete":
		return "Tab/Shift+Tab cycle • Enter apply • Esc close"
	case "hint.mention":
		return "@ attaches files/folders • Tab/Shift+Tab cycle • Enter apply"
	case "hint.mode":
		return "mode"
	case "hint.enter_send":
		return "Enter send"
	case "hint.help":
		return "/? help"
	case "hint.add_context":
		return "@ add context"
	case "hint.scroll":
		return "PgUp/PgDn scroll"
	case "hint.shift_tab_mode":
		return "Shift+Tab mode"
	case "hint.ctrlc_cancel":
		return "Ctrl+C cancel"
	case "hint.ctrlc_exit":
		return "Ctrl+C clear/exit"
	case "hint.image_attached":
		return "image attached"
	case "queued.count":
		return "%d queued"
	case "queued.output":
		return "[queued %d pending]\n\n"
	case "status.thinking":
		return "Thinking..."
	case "status.writing":
		return "Writing..."
	case "status.cancelling":
		return "Cancelling..."
	case "status.tokens":
		return "tokens"
	case "status.tools_used":
		return "%d tools used"
	case "tool.done":
		return "done"
	case "tool.failed":
		return "failed"
	case "tool.no_output":
		return "no output"
	case "tool.output":
		return "output"
	case "tool.content":
		return "content"
	case "tool.match":
		return "match"
	case "tool.matches":
		return "matches"
	case "tool.entry":
		return "entry"
	case "tool.result":
		return "result"
	case "approval.rejected":
		return "  Rejected.\n"
	case "approval.allow":
		return "Allow"
	case "approval.allow_always":
		return "Allow Always"
	case "approval.deny":
		return "Deny"
	case "approval.accept":
		return "Accept"
	case "approval.reject":
		return "Reject"
	case "exit.confirm":
		return "Press Ctrl-C again to exit.\n\n"
	case "interrupted":
		return "[interrupted]\n\n"
	case "lang.current":
		return "Current language: %s\nUsage: /lang <en|zh-CN>\n%s\n\n"
	case "lang.invalid":
		return "Unsupported language: %s\n%s\n\n"
	case "lang.switch":
		return "Language switched to: %s\n\n"
	case "mode.current":
		return "Current mode: %s\nUsage: /mode <supervised|plan|auto|bypass>\n\n"
	case "input.placeholder":
		return "Type a message..."
	case "command.unknown":
		return "Unknown command: %s\n"
	case "command.help_hint":
		return "Type /help for available commands\n\n"
	case "command.usage.allow":
		return "Usage: /allow <tool-name>\n\n"
	case "command.usage.resume":
		return "Usage: /resume <session-id>\n\n"
	case "command.usage.export":
		return "Usage: /export <session-id>\n\n"
	case "command.model_switched":
		return "Switched model to: %s (provider: %s)\n\n"
	case "command.model_failed":
		return "Failed to switch model: %v\n\n"
	case "command.model_current":
		return "Current model: %s (provider: %s)\nUsage: /model <model-name>\n\n"
	case "command.provider_unknown":
		return "Unknown provider: %s (available: %v)\n\n"
	case "command.provider_switched":
		return "Switched provider to: %s (model: %s)\n\n"
	case "command.provider_failed":
		return "Failed to switch provider: %v\n\n"
	case "command.provider_current":
		return "Current provider: %s (model: %s)\nAvailable: %s\nUsage: /provider <name>\n\n"
	case "command.allow_set":
		return "✓ %s is now always allowed\n\n"
	case "command.custom":
		return "Custom command /%s:\n"
	case "command.mention_error":
		return "Mention expansion error: %v"
	case "session.list_failed":
		return "Error listing sessions: %v\n\n"
	case "session.untitled":
		return "untitled"
	case "session.store_missing":
		return "Session store not configured.\n\n"
	case "session.none":
		return "No sessions found.\n\n"
	case "session.list.title":
		return "Sessions:\n\n"
	case "session.list.item":
		return "  %d. %s  %s  (%s)\n"
	case "session.list.hint":
		return "\nUse /resume <id> to continue a session\n\n"
	case "session.new":
		return "New session: %s\n\n"
	case "session.resume":
		return "Resumed session: %s — %s (%d messages)\n\n"
	case "session.resume_failed":
		return "Failed to resume session %s: %v\n\n"
	case "session.resume_fallback":
		return "Starting new session instead.\n\n"
	case "session.export_failed":
		return "Error exporting session: %v\n\n"
	case "session.write_failed":
		return "Error writing file: %v\n\n"
	case "session.exported":
		return "Exported session %s to %s\n\n"
	case "checkpoint.disabled":
		return "Checkpointing not enabled.\n\n"
	case "checkpoint.undo_failed":
		return "Undo failed: %v\n\n"
	case "checkpoint.undid":
		return "Undid %s on %s (checkpoint %s)\n"
	case "checkpoint.none":
		return "No checkpoints.\n\n"
	case "checkpoint.list.title":
		return "Checkpoints (%d):\n\n"
	case "checkpoint.list.item":
		return "  %d. %s  %s  %s  %s\n"
	case "checkpoint.list.hint":
		return "\nUse /undo to revert the most recent.\n\n"
	case "memory.auto_unavailable":
		return "Auto memory not initialized.\n\n"
	case "memory.list_failed":
		return "Error listing memories: %v\n\n"
	case "memory.none":
		return "No auto memories saved.\n\n"
	case "memory.auto_title":
		return "Auto Memories:\n"
	case "memory.clear_failed":
		return "Error clearing memories: %v\n\n"
	case "memory.cleared":
		return "All auto memories cleared.\n\n"
	case "memory.title":
		return "Memory:\n"
	case "memory.project":
		return "Project Memory (GGCODE.md):\n"
	case "memory.project_none":
		return "  No GGCODE.md files loaded.\n"
	case "memory.auto":
		return "Auto Memory:\n"
	case "memory.auto_none":
		return "  No auto memories loaded.\n"
	case "memory.usage":
		return "\nUsage: /memory [list|clear]\n\n"
	case "compact.unavailable":
		return "Context manager not available.\n\n"
	case "compact.failed":
		return "Compact failed: %v\n\n"
	case "compact.done":
		return "Conversation history compacted.\n\n"
	case "todo.cleared":
		return "Todo list cleared.\n\n"
	case "todo.clear_failed":
		return "Error clearing todos: %v\n\n"
	case "todo.none":
		return "No todo list found. Use the todo_write tool to create one.\n\n"
	case "todo.read_failed":
		return "Error reading todos: %v\n\n"
	case "todo.parse_failed":
		return "Error parsing todos: %v\n\n"
	case "todo.title":
		return "Todo list:\n%s\n\n"
	case "bug.title":
		return "=== Bug Report Diagnostics ===\n\n"
	case "bug.version":
		return "Version: ggcode (dev)\n"
	case "bug.os":
		return "OS: %s %s\n"
	case "bug.go":
		return "Go: %s\n"
	case "bug.provider":
		return "Provider: %s\n"
	case "bug.model":
		return "Model: %s\n"
	case "bug.session":
		return "Session: %s (%d messages)\n"
	case "bug.mcp":
		return "MCP servers: %d\n"
	case "bug.last_error":
		return "Last error: %s\n"
	case "bug.hint":
		return "\nPlease include this information when reporting a bug.\n\n"
	case "config.usage":
		return "Usage: /config set <key> <value>\n\n"
	case "config.not_loaded":
		return "Config not loaded.\n\n"
	case "config.model_set":
		return "Config: model = %s\n\n"
	case "config.provider_set":
		return "Config: provider = %s\n\n"
	case "config.language_set":
		return "Config: language = %s\n\n"
	case "config.unknown_key":
		return "Unknown config key: %s\nSupported: model, provider, language\n\n"
	case "config.title":
		return "Current Configuration:\n"
	case "status.title":
		return "Status:\n"
	case "plugins.unavailable":
		return "Plugin manager not available.\n\n"
	case "plugins.none":
		return "No plugins loaded.\n\n"
	case "plugins.title":
		return "Plugins:\n"
	case "mcp.none":
		return "No MCP servers configured.\n\n"
	case "mcp.title":
		return "MCP Servers:\n"
	case "cost.unavailable":
		return "Cost tracking not enabled.\n\n"
	case "cost.none":
		return "No cost data yet.\n\n"
	case "cost.summary":
		return "Cost Summary (all sessions)\n"
	case "cost.total":
		return "\n  Total: %s\n\n"
	case "cost.current":
		return "Current Session Cost\n"
	case "cost.current_none":
		return "No cost data for current session yet.\n\n"
	case "image.usage":
		return "Usage: /image <path/to/file.png>\n"
	case "image.formats":
		return "Supported formats: PNG, JPEG, GIF, WebP (max 20MB)\n\n"
	case "fullscreen.state":
		return "Fullscreen: %s\n\n"
	case "fullscreen.on":
		return "on"
	case "fullscreen.off":
		return "off"
	case "agents.unavailable":
		return "Sub-agent manager not configured.\n\n"
	case "agents.none":
		return "No sub-agents spawned yet.\nUsage: LLM can use spawn_agent tool to create sub-agents.\n\n"
	case "agents.title":
		return "%d sub-agent(s):\n"
	case "agents.item":
		return "  %s [%s]%s - %s\n"
	case "agents.hint":
		return "\nUse /agent <id> for details, /agent cancel <id> to cancel.\n\n"
	case "agent.usage":
		return "Usage: /agent <id> or /agent cancel <id>\n\n"
	case "agent.cancelled":
		return "Cancelled sub-agent %s\n\n"
	case "agent.cancel_failed":
		return "Could not cancel %s (not found or not running)\n\n"
	case "agent.not_found":
		return "Sub-agent %s not found\n\n"
	case "agent.title":
		return "Agent: %s\nStatus: %s\nTask: %s\n"
	case "agent.result":
		return "Result: %s\n"
	case "agent.error":
		return "Error: %v\n"
	case "slash.help":
		return "Show help message"
	case "slash.cost":
		return "Show session cost stats"
	case "slash.sessions":
		return "List saved sessions"
	case "slash.resume":
		return "Resume a previous session"
	case "slash.export":
		return "Export session to markdown"
	case "slash.model":
		return "Switch model"
	case "slash.provider":
		return "Switch provider"
	case "slash.clear":
		return "Clear conversation"
	case "slash.mcp":
		return "Show MCP servers"
	case "slash.memory":
		return "Manage memory"
	case "slash.undo":
		return "Undo last file edit"
	case "slash.checkpoints":
		return "List checkpoints"
	case "slash.allow":
		return "Always allow a tool"
	case "slash.plugins":
		return "List loaded plugins"
	case "slash.image":
		return "Attach an image"
	case "slash.fullscreen":
		return "Toggle fullscreen"
	case "slash.mode":
		return "Set permission mode"
	case "slash.lang":
		return "Switch interface language"
	case "slash.exit":
		return "Exit ggcode"
	case "slash.agents":
		return "List sub-agents"
	case "slash.agent":
		return "Sub-agent details"
	case "slash.compact":
		return "Compress conversation history"
	case "slash.todo":
		return "View/manage todo list"
	case "slash.bug":
		return "Report a bug"
	case "slash.config":
		return "View/modify configuration"
	case "slash.status":
		return "Show current status"
	case "help.text":
		return `Available commands:
  /help, /?          Show this help message
  /cost              Show current session cost stats
  /cost all          Show all session cost summary
  /sessions          List all saved sessions
  /resume <id>       Resume a previous session
  /export <id>       Export session to markdown file
  /model <name>      Switch model
  /provider <name>   Switch provider
  /lang <code>       Switch interface language
  /clear             Clear conversation history
  /mcp               Show connected MCP servers and tools
  /memory            Show loaded memory files
  /memory list       List auto memory entries
  /memory clear      Clear all auto memories
  /undo              Undo the last file edit (checkpoint rollback)
  /checkpoints       List all file edit checkpoints

  /allow <tool>      Always allow a specific tool
  /plugins           List loaded plugins and their tools
  /image <path>      Attach an image file
  /fullscreen        Toggle fullscreen mode
  /mode <mode>       Set permission mode (supervised|plan|auto|bypass)
  /agents            List sub-agents
  /agent <id>        Show sub-agent details
  /agent cancel <id> Cancel a sub-agent

  /compact           Compress conversation history
  /todo              View todo list
  /todo clear        Clear todo list
  /bug               Report a bug with diagnostics
  /config            Show current configuration
  /config set <k> <v> Set a config value
  /status            Show current status
  /exit, /quit       Exit ggcode

Keyboard shortcuts:
  Tab                Cycle autocomplete or approval choices
  Shift+Tab          Reverse cycle autocomplete, otherwise toggle permission mode
  Enter              Send message / apply current selection
  Esc                Cancel autocomplete
  ↑/↓                 Browse command history (or autocomplete)
  PgUp/PgDn          Scroll conversation output
  Ctrl+C             Cancel current activity, otherwise clear input then press again to exit
  Ctrl+D             Exit immediately

Mouse:
  Option+drag / Shift+drag  Select text to copy (bypasses app mouse capture)
  Mouse wheel               Scroll conversation output`
	default:
		return key
	}
}

func zhCatalog(key string) string {
	switch key {
	case "workspace.tagline":
		return "极客 AI 工作台"
	case "header.terminal_native":
		return "终端原生 AI 编码"
	case "session.ephemeral":
		return "临时会话"
	case "agents.idle":
		return "空闲"
	case "agents.running":
		return "%d 个运行中"
	case "activity.idle":
		return "空闲"
	case "panel.conversation":
		return "对话"
	case "panel.composer":
		return "输入"
	case "panel.composer_locked":
		return "输入已锁定"
	case "panel.commands":
		return "命令："
	case "panel.files":
		return "文件："
	case "panel.agent_status":
		return "Agent 状态"
	case "panel.approval_required":
		return "需要确认"
	case "panel.bypass_approval":
		return "Bypass 模式确认"
	case "panel.review_file_change":
		return "审阅文件修改"
	case "label.provider":
		return "提供商"
	case "label.model":
		return "模型"
	case "label.mode":
		return "模式"
	case "label.session":
		return "会话"
	case "label.agents":
		return "子 Agent"
	case "label.activity":
		return "活动"
	case "label.tool":
		return "工具"
	case "label.input":
		return "输入"
	case "label.file":
		return "文件"
	case "label.directory":
		return "目录"
	case "empty.ask":
		return "你可以让我做重构、修复 bug、解释代码或补测试。"
	case "empty.tips":
		return "提示：用 @path 引用文件，/? 查看帮助，Shift+Tab 切换模式。"
	case "hint.autocomplete":
		return "Tab/Shift+Tab 切换 • Enter 应用 • Esc 关闭"
	case "hint.mention":
		return "@ 可附加文件/目录 • Tab/Shift+Tab 切换 • Enter 应用"
	case "hint.mode":
		return "模式"
	case "hint.enter_send":
		return "Enter 发送"
	case "hint.help":
		return "/? 帮助"
	case "hint.add_context":
		return "@ 添加上下文"
	case "hint.scroll":
		return "PgUp/PgDn 滚动"
	case "hint.shift_tab_mode":
		return "Shift+Tab 切模式"
	case "hint.ctrlc_cancel":
		return "Ctrl+C 取消"
	case "hint.ctrlc_exit":
		return "Ctrl+C 清空/退出"
	case "hint.image_attached":
		return "已附加图片"
	case "queued.count":
		return "%d 条排队中"
	case "queued.output":
		return "[已排队 %d 条待发送]\n\n"
	case "status.thinking":
		return "思考中..."
	case "status.writing":
		return "输出中..."
	case "status.cancelling":
		return "取消中..."
	case "status.tokens":
		return "tokens"
	case "status.tools_used":
		return "已调用 %d 个工具"
	case "tool.done":
		return "完成"
	case "tool.failed":
		return "失败"
	case "tool.no_output":
		return "无输出"
	case "tool.output":
		return "输出"
	case "tool.content":
		return "内容"
	case "tool.match":
		return "匹配"
	case "tool.matches":
		return "匹配"
	case "tool.entry":
		return "项"
	case "tool.result":
		return "结果"
	case "approval.rejected":
		return "  已拒绝。\n"
	case "approval.allow":
		return "允许"
	case "approval.allow_always":
		return "总是允许"
	case "approval.deny":
		return "拒绝"
	case "approval.accept":
		return "接受"
	case "approval.reject":
		return "拒绝"
	case "exit.confirm":
		return "再按一次 Ctrl-C 退出。\n\n"
	case "interrupted":
		return "[已中断]\n\n"
	case "lang.current":
		return "当前语言：%s\n用法：/lang <en|zh-CN>\n%s\n\n"
	case "lang.invalid":
		return "不支持的语言：%s\n%s\n\n"
	case "lang.switch":
		return "已切换语言为：%s\n\n"
	case "mode.current":
		return "当前模式：%s\n用法：/mode <supervised|plan|auto|bypass>\n\n"
	case "input.placeholder":
		return "输入消息..."
	case "command.unknown":
		return "未知命令：%s\n"
	case "command.help_hint":
		return "输入 /help 查看可用命令\n\n"
	case "command.usage.allow":
		return "用法：/allow <tool-name>\n\n"
	case "command.usage.resume":
		return "用法：/resume <session-id>\n\n"
	case "command.usage.export":
		return "用法：/export <session-id>\n\n"
	case "command.model_switched":
		return "已切换模型为：%s（提供商：%s）\n\n"
	case "command.model_failed":
		return "切换模型失败：%v\n\n"
	case "command.model_current":
		return "当前模型：%s（提供商：%s）\n用法：/model <model-name>\n\n"
	case "command.provider_unknown":
		return "未知提供商：%s（可用：%v）\n\n"
	case "command.provider_switched":
		return "已切换提供商为：%s（模型：%s）\n\n"
	case "command.provider_failed":
		return "切换提供商失败：%v\n\n"
	case "command.provider_current":
		return "当前提供商：%s（模型：%s）\n可用：%s\n用法：/provider <name>\n\n"
	case "command.allow_set":
		return "✓ %s 已设为永久允许\n\n"
	case "command.custom":
		return "自定义命令 /%s：\n"
	case "command.mention_error":
		return "展开 @ 引用失败：%v"
	case "session.list_failed":
		return "列出会话失败：%v\n\n"
	case "session.untitled":
		return "未命名"
	case "session.store_missing":
		return "未配置会话存储。\n\n"
	case "session.none":
		return "没有找到会话。\n\n"
	case "session.list.title":
		return "会话列表：\n\n"
	case "session.list.item":
		return "  %d. %s  %s  （%s）\n"
	case "session.list.hint":
		return "\n使用 /resume <id> 继续某个会话\n\n"
	case "session.new":
		return "新会话：%s\n\n"
	case "session.resume":
		return "已恢复会话：%s — %s（%d 条消息）\n\n"
	case "session.resume_failed":
		return "恢复会话 %s 失败：%v\n\n"
	case "session.resume_fallback":
		return "将改为创建新会话。\n\n"
	case "session.export_failed":
		return "导出会话失败：%v\n\n"
	case "session.write_failed":
		return "写入文件失败：%v\n\n"
	case "session.exported":
		return "已导出会话 %s 到 %s\n\n"
	case "checkpoint.disabled":
		return "未启用检查点。\n\n"
	case "checkpoint.undo_failed":
		return "撤销失败：%v\n\n"
	case "checkpoint.undid":
		return "已撤销 %s 对 %s 的修改（检查点 %s）\n"
	case "checkpoint.none":
		return "没有检查点。\n\n"
	case "checkpoint.list.title":
		return "检查点（%d）：\n\n"
	case "checkpoint.list.item":
		return "  %d. %s  %s  %s  %s\n"
	case "checkpoint.list.hint":
		return "\n使用 /undo 回滚最近一次修改。\n\n"
	case "memory.auto_unavailable":
		return "自动记忆未初始化。\n\n"
	case "memory.list_failed":
		return "列出记忆失败：%v\n\n"
	case "memory.none":
		return "没有自动记忆。\n\n"
	case "memory.auto_title":
		return "自动记忆：\n"
	case "memory.clear_failed":
		return "清空记忆失败：%v\n\n"
	case "memory.cleared":
		return "已清空所有自动记忆。\n\n"
	case "memory.title":
		return "记忆：\n"
	case "memory.project":
		return "项目记忆（GGCODE.md）：\n"
	case "memory.project_none":
		return "  未加载 GGCODE.md 文件。\n"
	case "memory.auto":
		return "自动记忆：\n"
	case "memory.auto_none":
		return "  未加载自动记忆。\n"
	case "memory.usage":
		return "\n用法：/memory [list|clear]\n\n"
	case "compact.unavailable":
		return "上下文管理器不可用。\n\n"
	case "compact.failed":
		return "压缩失败：%v\n\n"
	case "compact.done":
		return "已压缩对话历史。\n\n"
	case "todo.cleared":
		return "已清空 todo 列表。\n\n"
	case "todo.clear_failed":
		return "清空 todo 失败：%v\n\n"
	case "todo.none":
		return "没有找到 todo 列表。请使用 todo_write 工具创建。\n\n"
	case "todo.read_failed":
		return "读取 todo 失败：%v\n\n"
	case "todo.parse_failed":
		return "解析 todo 失败：%v\n\n"
	case "todo.title":
		return "Todo 列表：\n%s\n\n"
	case "bug.title":
		return "=== Bug 报告诊断信息 ===\n\n"
	case "bug.version":
		return "版本：ggcode（dev）\n"
	case "bug.os":
		return "系统：%s %s\n"
	case "bug.go":
		return "Go：%s\n"
	case "bug.provider":
		return "提供商：%s\n"
	case "bug.model":
		return "模型：%s\n"
	case "bug.session":
		return "会话：%s（%d 条消息）\n"
	case "bug.mcp":
		return "MCP 服务器：%d\n"
	case "bug.last_error":
		return "最近错误：%s\n"
	case "bug.hint":
		return "\n报告 bug 时请附带这些信息。\n\n"
	case "config.usage":
		return "用法：/config set <key> <value>\n\n"
	case "config.not_loaded":
		return "配置未加载。\n\n"
	case "config.model_set":
		return "配置：model = %s\n\n"
	case "config.provider_set":
		return "配置：provider = %s\n\n"
	case "config.language_set":
		return "配置：language = %s\n\n"
	case "config.unknown_key":
		return "未知配置项：%s\n支持：model, provider, language\n\n"
	case "config.title":
		return "当前配置：\n"
	case "status.title":
		return "状态：\n"
	case "plugins.unavailable":
		return "插件管理器不可用。\n\n"
	case "plugins.none":
		return "没有加载插件。\n\n"
	case "plugins.title":
		return "插件：\n"
	case "mcp.none":
		return "没有配置 MCP 服务器。\n\n"
	case "mcp.title":
		return "MCP 服务器：\n"
	case "cost.unavailable":
		return "未启用成本统计。\n\n"
	case "cost.none":
		return "还没有成本数据。\n\n"
	case "cost.summary":
		return "成本汇总（全部会话）\n"
	case "cost.total":
		return "\n  总计：%s\n\n"
	case "cost.current":
		return "当前会话成本\n"
	case "cost.current_none":
		return "当前会话还没有成本数据。\n\n"
	case "image.usage":
		return "用法：/image <path/to/file.png>\n"
	case "image.formats":
		return "支持格式：PNG、JPEG、GIF、WebP（最大 20MB）\n\n"
	case "fullscreen.state":
		return "全屏：%s\n\n"
	case "fullscreen.on":
		return "开启"
	case "fullscreen.off":
		return "关闭"
	case "agents.unavailable":
		return "子 Agent 管理器未配置。\n\n"
	case "agents.none":
		return "还没有创建子 Agent。\n用法：LLM 可以使用 spawn_agent 工具创建子 Agent。\n\n"
	case "agents.title":
		return "%d 个子 Agent：\n"
	case "agents.item":
		return "  %s [%s]%s - %s\n"
	case "agents.hint":
		return "\n使用 /agent <id> 查看详情，/agent cancel <id> 取消。\n\n"
	case "agent.usage":
		return "用法：/agent <id> 或 /agent cancel <id>\n\n"
	case "agent.cancelled":
		return "已取消子 Agent %s\n\n"
	case "agent.cancel_failed":
		return "无法取消 %s（未找到或未在运行）\n\n"
	case "agent.not_found":
		return "未找到子 Agent %s\n\n"
	case "agent.title":
		return "Agent：%s\n状态：%s\n任务：%s\n"
	case "agent.result":
		return "结果：%s\n"
	case "agent.error":
		return "错误：%v\n"
	case "slash.help":
		return "显示帮助"
	case "slash.cost":
		return "显示会话成本"
	case "slash.sessions":
		return "列出已保存会话"
	case "slash.resume":
		return "恢复历史会话"
	case "slash.export":
		return "导出会话为 Markdown"
	case "slash.model":
		return "切换模型"
	case "slash.provider":
		return "切换提供商"
	case "slash.clear":
		return "清空对话"
	case "slash.mcp":
		return "显示 MCP 服务器"
	case "slash.memory":
		return "管理记忆"
	case "slash.undo":
		return "撤销最近一次文件修改"
	case "slash.checkpoints":
		return "列出检查点"
	case "slash.allow":
		return "永久允许某个工具"
	case "slash.plugins":
		return "列出已加载插件"
	case "slash.image":
		return "附加图片"
	case "slash.fullscreen":
		return "切换全屏"
	case "slash.mode":
		return "设置权限模式"
	case "slash.lang":
		return "切换界面语言"
	case "slash.exit":
		return "退出 ggcode"
	case "slash.agents":
		return "列出子 Agent"
	case "slash.agent":
		return "查看子 Agent 详情"
	case "slash.compact":
		return "压缩对话历史"
	case "slash.todo":
		return "查看/管理 todo"
	case "slash.bug":
		return "报告 bug"
	case "slash.config":
		return "查看/修改配置"
	case "slash.status":
		return "显示当前状态"
	case "help.text":
		return `可用命令：
  /help, /?          显示帮助
  /cost              显示当前会话成本
  /cost all          显示全部会话成本汇总
  /sessions          列出已保存会话
  /resume <id>       恢复历史会话
  /export <id>       导出会话为 Markdown 文件
  /model <name>      切换模型
  /provider <name>   切换提供商
  /lang <code>       切换界面语言
  /clear             清空对话历史
  /mcp               显示已连接的 MCP 服务器和工具
  /memory            显示已加载记忆
  /memory list       列出自动记忆条目
  /memory clear      清空自动记忆
  /undo              撤销最近一次文件修改（回滚检查点）
  /checkpoints       列出所有文件修改检查点

  /allow <tool>      永久允许某个工具
  /plugins           列出已加载插件及其工具
  /image <path>      附加图片文件
  /fullscreen        切换全屏模式
  /mode <mode>       设置权限模式（supervised|plan|auto|bypass）
  /agents            列出子 Agent
  /agent <id>        查看子 Agent 详情
  /agent cancel <id> 取消子 Agent

  /compact           压缩对话历史
  /todo              查看 todo 列表
  /todo clear        清空 todo 列表
  /bug               生成 bug 诊断信息
  /config            显示当前配置
  /config set <k> <v> 设置配置项
  /status            显示当前状态
  /exit, /quit       退出 ggcode

键盘快捷键：
  Tab                在补全或确认选项中切换
  Shift+Tab          反向切换补全，否则切换权限模式
  Enter              发送消息 / 应用当前选择
  Esc                取消补全
  ↑/↓                 浏览命令历史（或补全）
  PgUp/PgDn          滚动对话输出
  Ctrl+C             取消当前活动；空闲时先清空输入，再次按下退出
  Ctrl+D             立即退出

鼠标：
  Option+拖拽 / Shift+拖拽  选择文本复制（绕过应用鼠标捕获）
  鼠标滚轮                 滚动对话输出`
	default:
		return key
	}
}
