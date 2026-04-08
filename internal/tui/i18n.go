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
	if m.providerPanel != nil {
		current := m.providerPanel.modelFilter.Value()
		focused := m.providerPanel.modelFilter.Focused()
		m.providerPanel.modelFilter = newModelFilterInput(m.currentLanguage())
		m.providerPanel.modelFilter.SetValue(current)
		if focused {
			m.providerPanel.modelFilter.Focus()
		}
	}
	if m.modelPanel != nil {
		current := m.modelPanel.filter.Value()
		focused := m.modelPanel.filter.Focused()
		m.modelPanel.filter = newModelFilterInput(m.currentLanguage())
		m.modelPanel.filter.SetValue(current)
		if focused {
			m.modelPanel.filter.Focus()
		}
	}
	if m.harnessPanel != nil {
		m.harnessPanel.actionInput.Placeholder = harnessPanelInputPlaceholder(m.harnessPanel.selectedSection, m.currentLanguage())
	}
	m.approvalOptions = defaultApprovalOptionsFor(m.currentLanguage())
	m.diffOptions = diffConfirmOptionsFor(m.currentLanguage())
	if len(m.langOptions) > 0 {
		m.langOptions = languageOptionsFor(m.currentLanguage())
	}
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

func languageOptionsFor(lang Language) []languageOption {
	switch lang {
	case LangZhCN:
		return []languageOption{
			{label: "简体中文", shortcut: "z", lang: LangZhCN},
			{label: "English", shortcut: "e", lang: LangEnglish},
		}
	default:
		return []languageOption{
			{label: "English", shortcut: "e", lang: LangEnglish},
			{label: "简体中文", shortcut: "z", lang: LangZhCN},
		}
	}
}

func localizeSlashDescription(lang Language, cmd string) string {
	switch cmd {
	case "/help", "/?":
		return tr(lang, "slash.help")
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
	case "/init":
		return tr(lang, "slash.init")
	case "/harness":
		return tr(lang, "slash.harness")
	case "/lang":
		return tr(lang, "slash.lang")
	case "/skills":
		return tr(lang, "slash.skills")
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
	case "/update":
		return tr(lang, "slash.update")
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
	case "panel.mode_policy":
		return "Mode policy"
	case "panel.context":
		return "Context"
	case "panel.mcp":
		return "MCP"
	case "panel.startup":
		return "Initializing"
	case "panel.approval_required":
		return "Approval required"
	case "panel.bypass_approval":
		return "Bypass mode approval"
	case "panel.review_file_change":
		return "Review file change"
	case "label.vendor":
		return "vendor"
	case "label.endpoint":
		return "endpoint"
	case "label.model":
		return "model"
	case "label.mode":
		return "mode"
	case "label.session":
		return "session"
	case "label.agents":
		return "agents"
	case "label.cwd":
		return "cwd"
	case "label.branch":
		return "branch"
	case "label.skills":
		return "skills"
	case "label.activity":
		return "activity"
	case "label.window":
		return "window"
	case "label.usage":
		return "usage"
	case "label.compact":
		return "compact"
	case "label.approval_policy":
		return "approval"
	case "label.tool_policy":
		return "tools"
	case "label.agent_policy":
		return "agent"
	case "label.tool":
		return "tool"
	case "label.input":
		return "input"
	case "label.file":
		return "file"
	case "label.directory":
		return "directory"
	case "context.unavailable":
		return "No context data yet"
	case "context.until_compact":
		return "left"
	case "empty.ask":
		return "Ask for a refactor, bug fix, explanation, or tests."
	case "empty.tips":
		return "Tips: use @path to include files, /? for help, and Shift+Tab to change mode."
	case "startup.banner":
		return "Preparing the terminal UI and filtering startup terminal noise. You can type right away; this banner disappears once startup settles."
	case "hint.autocomplete":
		return "Tab/Shift+Tab cycle • Enter apply • Esc close"
	case "hint.mention":
		return "@ attaches files/folders • Tab/Shift+Tab cycle • Enter apply"
	case "hint.mode":
		return "mode"
	case "mode.approval.ask":
		return "ask as needed"
	case "mode.approval.none":
		return "no approval stops"
	case "mode.approval.critical":
		return "critical only"
	case "mode.tools.rules":
		return "follow tool rules"
	case "mode.tools.readonly":
		return "read-only only"
	case "mode.tools.safe":
		return "safe ops only"
	case "mode.tools.open":
		return "almost all tools"
	case "mode.agent.waits":
		return "waits for you"
	case "mode.agent.autocontinue":
		return "keeps going"
	case "hint.enter_send":
		return "Enter send"
	case "hint.ctrlv_image":
		return "Ctrl+V paste image"
	case "hint.ctrlr_sidebar":
		return "Ctrl+R sidebar"
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
		return "Current language: %s\nUse /lang to choose interactively, or /lang <en|zh-CN> to switch directly.\n%s\n\n"
	case "lang.invalid":
		return "Unsupported language: %s\n%s\n\n"
	case "lang.switch":
		return "Language switched to: %s\n\n"
	case "lang.selection.current":
		return " Current: %s"
	case "lang.selection.hint":
		return " Tab/j/k move • Enter confirm • e/z shortcuts • Esc cancel"
	case "lang.first_use.title":
		return "Choose your preferred language"
	case "lang.first_use.body":
		return " Select the language you want ggcode to use from now on."
	case "lang.first_use.hint":
		return " Tab/j/k move • Enter confirm • e/z shortcuts"
	case "mode.current":
		return "Current mode: %s\nUsage: /mode <supervised|plan|auto|bypass|autopilot>\n  supervised  Ask when a tool has no explicit rule\n  plan        Read-only exploration; deny writes and commands\n  auto        Allow safe operations, deny dangerous ones\n  bypass      Allow almost everything; only stop on critical actions\n  autopilot   bypass + keep going when the model asks back\n\n"
	case "input.placeholder":
		return "Type a message..."
	case "panel.model_filter.prompt":
		return "Filter> "
	case "panel.model_filter.placeholder":
		return "type to filter models"
	case "panel.model_list.none":
		return "(none)"
	case "panel.model_list.no_matches":
		return "(no matches)"
	case "panel.model_list.showing":
		return "showing %d/%d models"
	case "panel.model_list.hidden_above":
		return "%d above"
	case "panel.model_list.hidden_more":
		return "%d more"
	case "panel.provider.vendors":
		return "Vendors"
	case "panel.provider.endpoints":
		return "Endpoints"
	case "panel.provider.models":
		return "Models"
	case "panel.provider.active_draft":
		return "Active draft"
	case "panel.provider.protocol":
		return "Protocol"
	case "panel.provider.protocol.unknown":
		return "(unknown)"
	case "panel.provider.api_key":
		return "API key"
	case "panel.provider.api_key.missing":
		return "missing"
	case "panel.provider.api_key.configured":
		return "configured"
	case "panel.provider.base_url":
		return "Base URL"
	case "panel.provider.base_url.not_set":
		return "(not set)"
	case "panel.provider.tags":
		return "Tags"
	case "panel.provider.model.set_with_m":
		return "(set with m)"
	case "panel.provider.edit":
		return "Edit"
	case "panel.provider.edit.vendor_api_key":
		return "vendor api key"
	case "panel.provider.edit.endpoint_api_key":
		return "endpoint api key"
	case "panel.provider.edit.endpoint_base_url":
		return "endpoint base url"
	case "panel.provider.edit.custom_model":
		return "custom model"
	case "panel.provider.hint.edit":
		return "Enter save • Esc cancel"
	case "panel.provider.hint.main":
		return "Tab/Shift+Tab change focus • j/k move • / focus filter • Enter or s apply • a vendor key • u endpoint key • b base URL • m custom model • Esc close"
	case "panel.provider.saved":
		return "Saved."
	case "panel.provider.saved_activated":
		return "Saved and activated."
	case "panel.provider.refreshing_vendor":
		return "Refreshing models for %s..."
	case "panel.provider.refresh.save_failed":
		return "Refreshed models, but saving config failed: %s"
	case "panel.provider.refresh.partial":
		return "Refreshed %d endpoint(s), discovered %d model(s). Some endpoints failed: %v"
	case "panel.provider.refresh.success":
		return "Refreshed %d endpoint(s), discovered %d model(s)."
	case "panel.provider.refresh.failed":
		return "Model refresh failed: %s"
	case "panel.provider.refresh.none":
		return "No refreshable endpoints for this vendor."
	case "panel.model.models":
		return "Models"
	case "panel.model.vendor":
		return "Vendor"
	case "panel.model.endpoint":
		return "Endpoint"
	case "panel.model.protocol":
		return "Protocol"
	case "panel.model.source":
		return "Source"
	case "panel.model.source.builtin":
		return "built-in"
	case "panel.model.source.remote":
		return "remote"
	case "panel.model.refreshing":
		return "Refreshing latest models..."
	case "panel.model.hint.main":
		return "j/k move • Enter or s apply • r refresh • / focus filter • Esc close • /model <name> direct switch"
	case "panel.model.saved_runtime_inactive":
		return "Saved config, but current runtime is still inactive: %s"
	case "panel.model.switched":
		return "Switched model to %s."
	case "panel.model.refresh.save_failed":
		return "Refreshed models, but saving config failed: %s"
	case "panel.model.refresh.builtin_reason":
		return "Using built-in models: %s"
	case "panel.model.refresh.remote_loaded":
		return "Loaded %d remote model(s)."
	case "panel.model.refresh.builtin_loaded":
		return "Loaded built-in models."
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
	case "init.resolve_failed":
		return "Failed to resolve init target: %v\n\n"
	case "init.generate_failed":
		return "Failed to generate GGCODE.md content: %v\n\n"
	case "init.collecting":
		return "Collecting project knowledge..."
	case "command.model_switched":
		return "Switched model to: %s (vendor: %s)\n\n"
	case "command.model_failed":
		return "Failed to switch model: %v\n\n"
	case "command.model_current":
		return "Current model: %s (vendor: %s)\nUse /model to open the model panel or /model <model-name> to switch directly.\n\n"
	case "command.provider_unknown":
		return "Unknown vendor: %s (available: %v)\n\n"
	case "command.provider_switched":
		return "Switched vendor to: %s (model: %s)\n\n"
	case "command.provider_failed":
		return "Failed to update provider selection: %v\n\n"
	case "command.provider_current":
		return "Current vendor: %s (model: %s)\nAvailable vendors: %s\nUsage: /provider [vendor]\n\n"
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
		return "Project Memory:\n"
	case "memory.project_none":
		return "  No project memory files loaded.\n"
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
		return "Version: %s\n"
	case "bug.os":
		return "OS: %s %s\n"
	case "bug.go":
		return "Go: %s\n"
	case "bug.provider":
		return "Vendor: %s\n"
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
	case "panel.update":
		return "Update"
	case "label.version":
		return "Version"
	case "label.latest":
		return "Latest"
	case "update.sidebar_hint":
		return "New release available. Run /update."
	case "update.up_to_date":
		return "You are up to date."
	case "update.available":
		return "update available: %s"
	case "update.current":
		return "current: %s (latest: %s)"
	case "update.unknown":
		return "not checked yet"
	case "update.check_failed":
		return "check failed: %s"
	case "update.unavailable":
		return "Update is unavailable in this session.\n\n"
	case "update.preparing":
		return "Preparing update"
	case "update.failed":
		return "Update failed: %v\n\n"
	case "update.restart_failed":
		return "Update prepared, but restart failed: %v\n\n"
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
	case "mcp.active_tools":
		return "Active tools"
	case "mcp.more":
		return "… %d more • /mcp"
	case "image.usage":
		return "Usage: /image <path/to/file.png>\n"
	case "image.formats":
		return "Supported formats: PNG, JPEG, GIF, WebP (max 20MB)\n\n"
	case "image.attached":
		return "Image attached: %s\n"
	case "image.attached_hint":
		return "Send a message to include the image, or /image to attach another.\n\n"
	case "image.clipboard_failed":
		return "pasting image from clipboard failed: %v"
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
	case "slash.sessions":
		return "List saved sessions"
	case "slash.resume":
		return "Resume a previous session"
	case "slash.export":
		return "Export session to markdown"
	case "slash.model":
		return "Switch model"
	case "slash.provider":
		return "Open provider manager"
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
	case "slash.init":
		return "Generate project GGCODE.md"
	case "slash.harness":
		return "Run harness workflow commands"
	case "slash.lang":
		return "Switch interface language"
	case "slash.skills":
		return "Browse available skills"
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
	case "slash.update":
		return "Update ggcode"
	case "help.text":
		return `Available commands:
  /help, /?          Show this help message
  /sessions          List all saved sessions
  /resume <id>       Resume a previous session
  /export <id>       Export session to markdown file
  /model [name]      Open model panel or switch directly
  /provider [vendor] Open provider manager
  /lang [code]       Choose or switch interface language
  /skills            Browse available skills
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
  /mode <mode>       Set agent mode (supervised|plan|auto|bypass|autopilot)
  /init              Generate GGCODE.md from the current project
  /harness ...       Run harness control-plane commands
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
  /update            Update ggcode to the latest release
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
	case "command.harness_usage":
		return "Usage: /harness <init|check|queue|tasks|run|rerun|run-queued|monitor|contexts|inbox|review|promote|release|gc|doctor> ... (release supports rollouts|advance|pause|resume|abort|approve|reject)"
	case "command.harness_queue_usage":
		return "Usage: /harness queue <goal>"
	case "command.harness_run_usage":
		return "Usage: /harness run <goal>"
	case "command.harness_rerun_usage":
		return "Usage: /harness rerun <task-id>"
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
	case "panel.mode_policy":
		return "模式说明"
	case "panel.context":
		return "上下文"
	case "panel.mcp":
		return "MCP"
	case "panel.startup":
		return "正在初始化"
	case "panel.approval_required":
		return "需要确认"
	case "panel.bypass_approval":
		return "Bypass 模式确认"
	case "panel.review_file_change":
		return "审阅文件修改"
	case "label.vendor":
		return "供应商"
	case "label.endpoint":
		return "接入点"
	case "label.model":
		return "模型"
	case "label.mode":
		return "模式"
	case "label.session":
		return "会话"
	case "label.agents":
		return "子 Agent"
	case "label.cwd":
		return "目录"
	case "label.branch":
		return "分支"
	case "label.skills":
		return "技能"
	case "label.activity":
		return "活动"
	case "label.window":
		return "窗口"
	case "label.usage":
		return "占用"
	case "label.compact":
		return "压缩"
	case "label.approval_policy":
		return "审批"
	case "label.tool_policy":
		return "工具"
	case "label.agent_policy":
		return "行为"
	case "label.tool":
		return "工具"
	case "label.input":
		return "输入"
	case "label.file":
		return "文件"
	case "label.directory":
		return "目录"
	case "context.unavailable":
		return "暂无上下文数据"
	case "context.until_compact":
		return "后触发"
	case "empty.ask":
		return "你可以让我做重构、修复 bug、解释代码或补测试。"
	case "empty.tips":
		return "提示：用 @path 引用文件，/? 查看帮助，Shift+Tab 切换模式。"
	case "startup.banner":
		return "正在准备终端界面并过滤启动期的终端噪声。你现在就可以输入；一旦界面进入可交互状态，这个提示会自动消失。"
	case "hint.autocomplete":
		return "Tab/Shift+Tab 切换 • Enter 应用 • Esc 关闭"
	case "hint.mention":
		return "@ 可附加文件/目录 • Tab/Shift+Tab 切换 • Enter 应用"
	case "hint.mode":
		return "模式"
	case "mode.approval.ask":
		return "按需询问"
	case "mode.approval.none":
		return "不会停下来审批"
	case "mode.approval.critical":
		return "仅关键操作"
	case "mode.tools.rules":
		return "遵循工具规则"
	case "mode.tools.readonly":
		return "仅只读"
	case "mode.tools.safe":
		return "仅安全操作"
	case "mode.tools.open":
		return "几乎全部工具"
	case "mode.agent.waits":
		return "等待你决策"
	case "mode.agent.autocontinue":
		return "自动继续推进"
	case "hint.enter_send":
		return "Enter 发送"
	case "hint.ctrlv_image":
		return "Ctrl+V 粘贴图片"
	case "hint.ctrlr_sidebar":
		return "Ctrl+R 侧栏"
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
		return "当前语言：%s\n使用 /lang 打开选择列表，或用 /lang <en|zh-CN> 直接切换。\n%s\n\n"
	case "lang.invalid":
		return "不支持的语言：%s\n%s\n\n"
	case "lang.switch":
		return "已切换语言为：%s\n\n"
	case "lang.selection.current":
		return " 当前语言：%s"
	case "lang.selection.hint":
		return " Tab/j/k 移动 • Enter 确认 • e/z 快捷键 • Esc 取消"
	case "lang.first_use.title":
		return "选择你偏好的语言"
	case "lang.first_use.body":
		return " 请选择今后 ggcode 默认使用的界面语言。"
	case "lang.first_use.hint":
		return " Tab/j/k 移动 • Enter 确认 • e/z 快捷键"
	case "mode.current":
		return "当前模式：%s\n用法：/mode <supervised|plan|auto|bypass|autopilot>\n  supervised  未显式配置的工具会询问\n  plan        严格只读探索；拒绝写入和命令\n  auto        自动允许安全操作，拒绝危险操作\n  bypass      基本全放行，只在关键操作时停下\n  autopilot   等同 bypass，并在模型反问时自动继续\n\n"
	case "input.placeholder":
		return "输入消息..."
	case "panel.model_filter.prompt":
		return "筛选> "
	case "panel.model_filter.placeholder":
		return "输入以筛选模型"
	case "panel.model_list.none":
		return "(空)"
	case "panel.model_list.no_matches":
		return "(无匹配)"
	case "panel.model_list.showing":
		return "显示 %d/%d 个模型"
	case "panel.model_list.hidden_above":
		return "上方还有 %d 个"
	case "panel.model_list.hidden_more":
		return "还有 %d 个"
	case "panel.provider.vendors":
		return "供应商"
	case "panel.provider.endpoints":
		return "端点"
	case "panel.provider.models":
		return "模型"
	case "panel.provider.active_draft":
		return "当前草稿"
	case "panel.provider.protocol":
		return "协议"
	case "panel.provider.protocol.unknown":
		return "(未知)"
	case "panel.provider.api_key":
		return "API Key"
	case "panel.provider.api_key.missing":
		return "未配置"
	case "panel.provider.api_key.configured":
		return "已配置"
	case "panel.provider.base_url":
		return "Base URL"
	case "panel.provider.base_url.not_set":
		return "(未设置)"
	case "panel.provider.tags":
		return "标签"
	case "panel.provider.model.set_with_m":
		return "(按 m 设置)"
	case "panel.provider.edit":
		return "编辑"
	case "panel.provider.edit.vendor_api_key":
		return "供应商 API Key"
	case "panel.provider.edit.endpoint_api_key":
		return "端点 API Key"
	case "panel.provider.edit.endpoint_base_url":
		return "端点 Base URL"
	case "panel.provider.edit.custom_model":
		return "自定义模型"
	case "panel.provider.hint.edit":
		return "Enter 保存 • Esc 取消"
	case "panel.provider.hint.main":
		return "Tab/Shift+Tab 切换焦点 • j/k 移动 • / 聚焦筛选 • Enter 或 s 应用 • a 供应商 key • u 端点 key • b Base URL • m 自定义模型 • Esc 关闭"
	case "panel.provider.saved":
		return "已保存。"
	case "panel.provider.saved_activated":
		return "已保存并激活。"
	case "panel.provider.refreshing_vendor":
		return "正在刷新 %s 的模型..."
	case "panel.provider.refresh.save_failed":
		return "模型已刷新，但保存配置失败：%s"
	case "panel.provider.refresh.partial":
		return "已刷新 %d 个端点，发现 %d 个模型。部分端点失败：%v"
	case "panel.provider.refresh.success":
		return "已刷新 %d 个端点，发现 %d 个模型。"
	case "panel.provider.refresh.failed":
		return "模型刷新失败：%s"
	case "panel.provider.refresh.none":
		return "这个供应商没有可刷新的端点。"
	case "panel.model.models":
		return "模型"
	case "panel.model.vendor":
		return "供应商"
	case "panel.model.endpoint":
		return "端点"
	case "panel.model.protocol":
		return "协议"
	case "panel.model.source":
		return "来源"
	case "panel.model.source.builtin":
		return "内置"
	case "panel.model.source.remote":
		return "远端"
	case "panel.model.refreshing":
		return "正在刷新最新模型..."
	case "panel.model.hint.main":
		return "j/k 移动 • Enter 或 s 应用 • r 刷新 • / 聚焦筛选 • Esc 关闭 • /model <name> 直接切换"
	case "panel.model.saved_runtime_inactive":
		return "配置已保存，但当前运行时仍未激活：%s"
	case "panel.model.switched":
		return "已切换模型为 %s。"
	case "panel.model.refresh.save_failed":
		return "模型已刷新，但保存配置失败：%s"
	case "panel.model.refresh.builtin_reason":
		return "使用内置模型：%s"
	case "panel.model.refresh.remote_loaded":
		return "已加载 %d 个远端模型。"
	case "panel.model.refresh.builtin_loaded":
		return "已加载内置模型。"
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
	case "init.resolve_failed":
		return "解析初始化目标失败：%v\n\n"
	case "init.generate_failed":
		return "生成 GGCODE.md 内容失败：%v\n\n"
	case "init.collecting":
		return "正在收集项目知识..."
	case "command.model_switched":
		return "已切换模型为：%s（供应商：%s）\n\n"
	case "command.model_failed":
		return "切换模型失败：%v\n\n"
	case "command.model_current":
		return "当前模型：%s（供应商：%s）\n使用 /model 打开模型面板，或用 /model <model-name> 直接切换。\n\n"
	case "command.provider_unknown":
		return "未知供应商：%s（可用：%v）\n\n"
	case "command.provider_switched":
		return "已切换供应商为：%s（模型：%s）\n\n"
	case "command.provider_failed":
		return "更新供应商选择失败：%v\n\n"
	case "command.provider_current":
		return "当前供应商：%s（模型：%s）\n可用供应商：%s\n用法：/provider [vendor]\n\n"
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
		return "项目记忆：\n"
	case "memory.project_none":
		return "  未加载项目记忆文件。\n"
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
		return "版本：%s\n"
	case "bug.os":
		return "系统：%s %s\n"
	case "bug.go":
		return "Go：%s\n"
	case "bug.provider":
		return "供应商：%s\n"
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
	case "panel.update":
		return "更新"
	case "label.version":
		return "版本"
	case "label.latest":
		return "最新"
	case "update.sidebar_hint":
		return "发现新版本，可使用 /update 升级。"
	case "update.up_to_date":
		return "当前已是最新版本。"
	case "update.available":
		return "可升级到：%s"
	case "update.current":
		return "当前：%s（最新：%s）"
	case "update.unknown":
		return "尚未检查"
	case "update.check_failed":
		return "检查失败：%s"
	case "update.unavailable":
		return "当前会话无法升级。\n\n"
	case "update.preparing":
		return "正在准备升级"
	case "update.failed":
		return "升级失败：%v\n\n"
	case "update.restart_failed":
		return "升级已准备完成，但重启失败：%v\n\n"
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
	case "mcp.active_tools":
		return "活动工具"
	case "mcp.more":
		return "… 还有 %d 个，使用 /mcp 查看"
	case "image.usage":
		return "用法：/image <path/to/file.png>\n"
	case "image.formats":
		return "支持格式：PNG、JPEG、GIF、WebP（最大 20MB）\n\n"
	case "image.attached":
		return "已附加图片：%s\n"
	case "image.attached_hint":
		return "发送一条消息即可携带这张图片，或继续用 /image 再附加一张。\n\n"
	case "image.clipboard_failed":
		return "从剪贴板粘贴图片失败：%v"
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
	case "slash.sessions":
		return "列出已保存会话"
	case "slash.resume":
		return "恢复历史会话"
	case "slash.export":
		return "导出会话为 Markdown"
	case "slash.model":
		return "切换模型"
	case "slash.provider":
		return "打开供应商管理界面"
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
	case "slash.init":
		return "生成项目 GGCODE.md"
	case "slash.harness":
		return "运行 harness 工作流命令"
	case "slash.lang":
		return "切换界面语言"
	case "slash.skills":
		return "浏览可用 skills"
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
	case "slash.update":
		return "升级 ggcode"
	case "help.text":
		return `可用命令：
  /help, /?          显示帮助
  /sessions          列出已保存会话
  /resume <id>       恢复历史会话
  /export <id>       导出会话为 Markdown 文件
  /model [name]      打开模型面板或直接切换
  /provider [vendor] 打开供应商管理界面
  /lang [code]       选择或切换界面语言
  /skills            浏览可用 skills
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
  /mode <mode>       设置运行模式（supervised|plan|auto|bypass|autopilot）
  /init              基于当前项目生成 GGCODE.md
  /harness ...       运行 harness 控制面命令
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
  /update            升级到最新正式版本
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
	case "command.harness_usage":
		return "用法：/harness <init|check|queue|tasks|run|rerun|run-queued|monitor|contexts|inbox|review|promote|release|gc|doctor> ...（release 支持 rollouts|advance|pause|resume|abort|approve|reject）"
	case "command.harness_queue_usage":
		return "用法：/harness queue <goal>"
	case "command.harness_run_usage":
		return "用法：/harness run <goal>"
	case "command.harness_rerun_usage":
		return "用法：/harness rerun <task-id>"
	default:
		return key
	}
}
