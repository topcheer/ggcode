package tui

import (
	"fmt"
	"strings"
	"sync"
)

var (
	moduleCatalogsMu sync.RWMutex
	enModuleCatalogs = make(map[string]string)
	zhModuleCatalogs = make(map[string]string)
)

// registerCatalog merges a module's key-value pairs into the global extension catalogs.
// Called from init() in i18n module files.
func registerCatalog(en, zh map[string]string) {
	moduleCatalogsMu.Lock()
	defer moduleCatalogsMu.Unlock()
	for k, v := range en {
		enModuleCatalogs[k] = v
	}
	for k, v := range zh {
		zhModuleCatalogs[k] = v
	}
}

func lookupModuleCatalog(lang Language, key string) (string, bool) {
	moduleCatalogsMu.RLock()
	defer moduleCatalogsMu.RUnlock()
	switch lang {
	case LangZhCN:
		v, ok := zhModuleCatalogs[key]
		return v, ok
	default:
		v, ok := enModuleCatalogs[key]
		return v, ok
	}
}

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
	m.syncComposerMode()
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
	if m.pendingQuestionnaire != nil {
		m.pendingQuestionnaire.loadActiveQuestion(m.currentLanguage())
		m.syncQuestionnaireInputWidth()
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
	case "/qq":
		return tr(lang, "slash.qq")
	case "/telegram", "/tg":
		return tr(lang, "slash.telegram")
	case "/pc":
		return tr(lang, "slash.pc")
	case "/discord":
		return tr(lang, "slash.discord")
	case "/feishu", "/lark":
		return tr(lang, "slash.feishu")
	case "/slack":
		return tr(lang, "slash.slack")
	case "/dingtalk", "/ding":
		return tr(lang, "slash.dingtalk")
	case "/im":
		return tr(lang, "slash.im")
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
	case "panel.im":
		return "IM"
	case "panel.mcp":
		return "MCP"
	case "panel.mcp.install_spec_required":
		return "Enter an install spec first."
	case "panel.mcp.installing_server":
		return "Installing MCP server..."
	case "panel.mcp.reconnect_unavailable":
		return "Reconnect unavailable in this session."
	case "panel.mcp.reconnecting":
		return "Reconnecting %s..."
	case "panel.mcp.reconnect_failed":
		return "Unable to reconnect %s."
	case "panel.mcp.installing_browser_preset":
		return "Installing browser automation MCP preset..."
	case "panel.mcp.uninstalling":
		return "Uninstalling %s..."
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
	case "im.none":
		return "No adapters configured"
	case "im.summary":
		return "%d adapters • %d healthy"
	case "im.more":
		return "+%d more (/qq)"
	case "im.runtime.available":
		return "runtime available"
	case "im.runtime.disabled":
		return "disabled"
	case "im.runtime.not_started":
		return "enabled • restart to initialize"
	case "im.status.not_started":
		return "not started"
	case "context.until_compact":
		return "left"
	case "empty.ask":
		return "Ask for a refactor, bug fix, explanation, or tests."
	case "empty.tips":
		return "Tips: use @path to include files, /? for help, and Shift+Tab to change mode."
	case "startup.banner":
		return "Preparing the terminal UI and filtering startup terminal noise. You can type right away; this banner disappears once startup settles."
	case "harness.views":
		return "Views"
	case "harness.items":
		return "Items"
	case "harness.action":
		return "Action"
	case "harness.details":
		return "Details"
	case "harness.none":
		return "(none)"
	case "harness.unknown":
		return "unknown"
	case "harness.unscoped":
		return "unscoped"
	case "harness.unavailable":
		return "Harness unavailable"
	case "harness.unavailable_intro":
		return "Start here in an existing project:"
	case "harness.unavailable_step_init":
		return "  1. Press Enter or i to initialize harness"
	case "harness.unavailable_step_refresh":
		return "  2. Press r to refresh once init finishes"
	case "harness.section.init":
		return "Init"
	case "harness.section.check":
		return "Check"
	case "harness.section.doctor":
		return "Doctor"
	case "harness.section.monitor":
		return "Monitor"
	case "harness.section.gc":
		return "GC"
	case "harness.section.contexts":
		return "Contexts"
	case "harness.section.tasks":
		return "Tasks"
	case "harness.section.queue":
		return "Queue"
	case "harness.section.run":
		return "Run"
	case "harness.section.run_queued":
		return "Run queued"
	case "harness.section.inbox":
		return "Inbox"
	case "harness.section.review":
		return "Review"
	case "harness.section.promote":
		return "Promote"
	case "harness.section.release":
		return "Release"
	case "harness.section.rollouts":
		return "Rollouts"
	case "harness.hints.unavailable":
		return "Enter/i init harness • r refresh • Esc close"
	case "harness.hints.move":
		return "j/k move"
	case "harness.hints.tab":
		return "Tab switch"
	case "harness.hints.refresh":
		return "r refresh"
	case "harness.hints.close":
		return "Esc close"
	case "harness.hints.check":
		return "Enter run checks"
	case "harness.hints.monitor":
		return "Enter refresh snapshot"
	case "harness.hints.gc":
		return "Enter run gc"
	case "harness.hints.type_goal":
		return "type goal"
	case "harness.hints.queue":
		return "Enter queue"
	case "harness.hints.run":
		return "Enter run"
	case "harness.hints.focus_input":
		return "Tab focus input"
	case "harness.hints.rerun":
		return "Enter rerun failed"
	case "harness.hints.next":
		return "Enter next"
	case "harness.hints.all":
		return "a all"
	case "harness.hints.retry_failed":
		return "f retry-failed"
	case "harness.hints.resume":
		return "s resume"
	case "harness.hints.promote_owner":
		return "p promote owner"
	case "harness.hints.retry_owner":
		return "f retry owner"
	case "harness.hints.approve":
		return "Enter approve"
	case "harness.hints.reject":
		return "x reject"
	case "harness.hints.promote":
		return "Enter promote"
	case "harness.hints.apply_batch":
		return "Enter apply batch"
	case "harness.hints.advance":
		return "Enter advance"
	case "harness.hints.approve_gate":
		return "g approve gate"
	case "harness.hints.pause_resume":
		return "p pause/resume"
	case "harness.hints.abort":
		return "x abort"
	case "harness.hint.primary.check":
		return "Press Enter to run checks."
	case "harness.hint.primary.monitor":
		return "Press Enter to refresh the monitor snapshot."
	case "harness.hint.primary.gc":
		return "Press Enter to run garbage collection."
	case "harness.hint.primary.queue":
		return "Type a goal, then press Enter to queue it."
	case "harness.hint.primary.run":
		return "Type a goal, then press Enter to start the run."
	case "harness.hint.primary.tasks":
		return "Press Enter to rerun the selected failed task."
	case "harness.hint.primary.run_queued":
		return "Press Enter for next; a runs all; f retries failed; s resumes interrupted."
	case "harness.hint.primary.inbox":
		return "Press p to promote this owner or f to retry this owner."
	case "harness.hint.primary.review":
		return "Press Enter to approve or x to reject."
	case "harness.hint.primary.promote":
		return "Press Enter to promote the selected task."
	case "harness.hint.primary.release":
		return "Press Enter to apply the current release batch."
	case "harness.hint.primary.rollouts":
		return "Press Enter to advance; g approves gate; p pauses/resumes; x aborts."
	case "harness.hint.primary.none":
		return "No inline input needed for this section."
	case "harness.message.read_only":
		return "Harness panel is read-only while another run is active."
	case "harness.message.monitor_refreshed":
		return "Harness monitor refreshed."
	case "harness.message.rerun_failed_only":
		return "Harness task %s is %s; only failed tasks can be rerun."
	case "harness.message.review_approved":
		return "Approved review for %s"
	case "harness.message.review_rejected":
		return "Rejected review for %s"
	case "harness.message.promoted":
		return "Promoted %s"
	case "harness.message.no_release_tasks":
		return "No harness tasks are ready for release."
	case "harness.message.release_applied":
		return "Applied release batch %s"
	case "harness.message.no_rollouts":
		return "No persisted rollouts found."
	case "harness.message.rollout_advanced":
		return "Advanced rollout %s"
	case "harness.message.owner_promoted":
		return "Promoted %d task(s) for %s"
	case "harness.message.owner_retried":
		return "Retried failed tasks for %s"
	case "harness.message.gate_approved":
		return "Approved next gate for %s"
	case "harness.message.rollout_resumed":
		return "Resumed rollout %s"
	case "harness.message.rollout_paused":
		return "Paused rollout %s"
	case "harness.message.rollout_aborted":
		return "Aborted rollout %s"
	case "harness.message.check_passed":
		return "Harness check passed."
	case "harness.message.check_failed":
		return "Harness check found issues."
	case "harness.message.gc_complete":
		return "Harness gc complete."
	case "harness.message.queue_goal_required":
		return "Type a queue goal in the panel input first."
	case "harness.message.queued":
		return "Queued harness task %s"
	case "harness.activity.status":
		return "Harness %s"
	case "harness.log.phase":
		return "Phase"
	case "harness.log.worker":
		return "Worker"
	case "harness.tool.read_file":
		return "Read file"
	case "harness.tool.write_file":
		return "Write file"
	case "harness.tool.browse_files":
		return "Browse files"
	case "harness.tool.search_code":
		return "Search code"
	case "harness.tool.run_command":
		return "Run command"
	case "harness.tool.fetch_web_page":
		return "Fetch web page"
	case "harness.tool.run_subagent":
		return "Run sub-agent"
	case "harness.tool.update_task_state":
		return "Update task state"
	case "harness.message.run_goal_required":
		return "Type a run goal in the panel input first."
	case "harness.message.no_queued_executed":
		return "No queued harness tasks were executed."
	case "harness.message.queue_retried":
		return "Retried %d failed queued task(s)."
	case "harness.message.queue_resumed":
		return "Resumed %d interrupted queued task(s)."
	case "harness.message.queue_ran":
		return "Ran %d queued task(s)."
	case "harness.preview.not_initialized":
		return "Harness is not initialized in this project yet.\n\nPress Enter or i to run harness init in the current repository."
	case "harness.preview.check":
		return "Run harness checks against the current project.\n\nEnter: run required file/content/context checks plus configured validation commands."
	case "harness.preview.gc":
		return "Run harness garbage collection.\n\nEnter: archive stale tasks, abandon stale blocked/running work, prune old logs, and remove orphaned worktrees."
	case "harness.preview.queue_help":
		return "Type the harness goal here, then press Enter to queue it."
	case "harness.preview.run_help":
		return "Type the harness goal here, then press Enter to start the run."
	case "harness.preview.run_queued":
		return "Queue status:\nqueued=%d running=%d blocked=%d failed=%d\n\nEnter runs the next runnable task.\na runs all runnable tasks.\nf retries failed tasks.\ns resumes interrupted tasks."
	case "harness.preview.no_owner":
		return "No harness owner selected."
	case "harness.preview.no_context":
		return "No harness context selected."
	case "harness.preview.no_task":
		return "No harness task selected."
	case "harness.preview.project_not_initialized":
		return "Harness is not initialized in this project yet."
	case "harness.preview.project_initialized":
		return "Harness is initialized."
	case "harness.preview.project_help":
		return "Use /harness to browse and operate the control plane."
	case "harness.preview.no_doctor":
		return "No harness doctor report."
	case "harness.preview.monitor_unavailable":
		return "Harness monitor unavailable."
	case "harness.label.context_title":
		return "Context"
	case "harness.label.owner_title":
		return "Owner"
	case "harness.label.id":
		return "id"
	case "harness.label.status":
		return "status"
	case "harness.label.goal":
		return "goal"
	case "harness.label.attempts":
		return "attempts"
	case "harness.label.depends_on":
		return "depends_on"
	case "harness.label.context":
		return "context"
	case "harness.label.workspace":
		return "workspace"
	case "harness.label.branch":
		return "branch"
	case "harness.label.worker":
		return "worker"
	case "harness.label.progress":
		return "progress"
	case "harness.label.verification":
		return "verification"
	case "harness.label.changed_files":
		return "changed_files"
	case "harness.label.delivery_report":
		return "delivery_report"
	case "harness.label.delivery_report_human":
		return "delivery report"
	case "harness.label.log":
		return "log"
	case "harness.label.review":
		return "review"
	case "harness.label.review_notes":
		return "review_notes"
	case "harness.label.promotion":
		return "promotion"
	case "harness.label.promotion_notes":
		return "promotion_notes"
	case "harness.label.release_batch":
		return "release_batch"
	case "harness.label.release_batch_human":
		return "release batch"
	case "harness.label.release_notes":
		return "release_notes"
	case "harness.label.error":
		return "error"
	case "harness.label.name":
		return "name"
	case "harness.label.description":
		return "description"
	case "harness.label.owner":
		return "owner"
	case "harness.label.commands":
		return "commands"
	case "harness.label.tasks":
		return "tasks"
	case "harness.label.rollouts":
		return "rollouts"
	case "harness.label.gates":
		return "gates"
	case "harness.label.latest":
		return "latest"
	case "harness.label.repo":
		return "repo"
	case "harness.label.config":
		return "config"
	case "harness.label.project":
		return "project"
	case "harness.label.structure":
		return "structure"
	case "harness.label.contexts":
		return "contexts"
	case "harness.label.workers":
		return "workers"
	case "harness.label.workflow":
		return "workflow"
	case "harness.label.quality":
		return "quality"
	case "harness.label.worktrees":
		return "worktrees"
	case "harness.label.snapshot":
		return "snapshot"
	case "harness.label.events":
		return "events"
	case "harness.label.target":
		return "target"
	case "harness.label.review_ready":
		return "review_ready"
	case "harness.label.promotion_ready":
		return "promotion_ready"
	case "harness.label.retryable":
		return "retryable"
	case "harness.task_title":
		return "Harness task"
	case "harness.doctor_title":
		return "Harness doctor"
	case "harness.monitor_title":
		return "Harness monitor"
	case "harness.latest_task":
		return "Latest task"
	case "harness.latest_event":
		return "Latest event"
	case "harness.focus":
		return "Focus"
	case "harness.status.ok":
		return "ok"
	case "harness.status.needs_attention":
		return "needs attention"
	case "harness.group.review":
		return "review"
	case "harness.group.promotion":
		return "promotion"
	case "harness.group.retry":
		return "retry"
	case "harness.review_ready_short":
		return "review"
	case "harness.promote_ready_short":
		return "promote"
	case "harness.tasks_count":
		return "tasks"
	case "harness.input_empty":
		return "(input box is empty)"
	case "harness.no_waves":
		return "no waves"
	case "harness.mixed":
		return "mixed"
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
	case "interrupt.delivered":
		return "[delivered to active run; revising plan]\n"
	case "status.thinking":
		return "Thinking..."
	case "status.writing":
		return "Writing..."
	case "status.cancelling":
		return "Cancelling..."
	case "status.compacting":
		return "[compacting conversation to stay within context window]"
	case "status.compacted":
		return "[conversation compacted]"
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
	case "approval.execute_plan":
		return "Execute plan"
	case "plan.confirm_execute":
		return "Execute this plan?"
	case "plan.empty":
		return "(empty plan)"
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
	case "mode.persist_failed":
		return "Failed to persist mode preference: %v"
	case "input.placeholder":
		return "Type a message... ($ / ! enters shell mode)"
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
	case "panel.provider.auth":
		return "Auth"
	case "panel.provider.env_var":
		return "Env var"
	case "panel.provider.api_key":
		return "API key"
	case "panel.provider.api_key.missing":
		return "missing"
	case "panel.provider.api_key.configured":
		return "configured"
	case "panel.provider.auth.connected":
		return "connected"
	case "panel.provider.auth.not_connected":
		return "not connected"
	case "panel.provider.base_url":
		return "Base URL"
	case "panel.provider.base_url.not_set":
		return "(not set)"
	case "panel.provider.enterprise_url":
		return "Enterprise URL"
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
	case "panel.provider.hint.copilot":
		return "GitHub Copilot: l login • x logout • b edit enterprise domain"
	case "panel.provider.saved":
		return "Saved."
	case "panel.provider.saved_activated":
		return "Saved and activated."
	case "panel.provider.login.starting":
		return "Starting GitHub Copilot login..."
	case "panel.provider.login.instructions":
		return "Open %s and enter code %s. Waiting for authorization..."
	case "panel.provider.login.copied":
		return "Device code copied to clipboard."
	case "panel.provider.login.copy_failed":
		return "Copying device code failed: %s"
	case "panel.provider.login.browser_opened":
		return "Opened the verification page in your browser."
	case "panel.provider.login.browser_failed":
		return "Opening the verification page failed: %s"
	case "panel.provider.login.success":
		return "GitHub Copilot connected."
	case "panel.provider.login.failed":
		return "GitHub Copilot login failed: %s"
	case "panel.provider.logout.success":
		return "GitHub Copilot disconnected."
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
		return "Current model: %s (vendor: %s)\nAvailable models: %s\nUse /model to open the model panel or /model <model-name> to switch directly.\n\n"
	case "command.provider_unknown":
		return "Unknown vendor: %s (available: %v)\n\n"
	case "command.provider_switched":
		return "Switched vendor to: %s (model: %s)\n\n"
	case "command.provider_failed":
		return "Failed to update provider selection: %v\n\n"
	case "command.provider_current":
		return "Current vendor: %s (endpoint: %s, model: %s)\nAvailable vendors: %s\nAvailable endpoints: %s\nUsage: /provider [vendor] [endpoint]\n\n"
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
	case "slash.qq":
		return "Manage QQ channel binding"
	case "slash.telegram":
		return "Manage Telegram channel binding"
	case "slash.pc":
		return "Manage PC channel binding"
	case "slash.discord":
		return "Manage Discord channel binding"
	case "slash.feishu":
		return "Manage Feishu channel binding"
	case "slash.slack":
		return "Manage Slack channel binding"
	case "slash.dingtalk":
		return "Manage DingTalk channel binding"
	case "slash.im":
		return "Open unified IM channels panel"
	case "panel.qq.directory":
		return "Directory"
	case "panel.qq.runtime":
		return "Runtime"
	case "panel.qq.bots":
		return "QQ Bots"
	case "panel.qq.created":
		return "Created: %d"
	case "panel.qq.bound":
		return "Bound: %d"
	case "panel.qq.available":
		return "Available: %d"
	case "panel.qq.current_binding":
		return "Current Binding"
	case "panel.qq.none":
		return "(none)"
	case "panel.qq.default":
		return "(default)"
	case "panel.qq.adapter":
		return "Adapter: %s"
	case "panel.qq.target":
		return "Target: %s"
	case "panel.qq.channel":
		return "Channel: %s"
	case "panel.qq.bot_list":
		return "QQ Bot List"
	case "panel.qq.no_bots":
		return "No QQ bots configured."
	case "panel.qq.entry.available":
		return "Available"
	case "panel.qq.entry.bound":
		return "Bound"
	case "panel.qq.details":
		return "Details"
	case "panel.qq.status":
		return "Status: %s"
	case "panel.qq.transport":
		return "Transport: %s"
	case "panel.qq.bound_directory":
		return "Bound Directory: %s"
	case "panel.qq.current_directory_target":
		return "Current Directory Target: %s"
	case "panel.qq.current_directory_channel":
		return "Current Directory Channel: %s"
	case "panel.qq.waiting_for_pairing":
		return "(waiting for pairing)"
	case "panel.qq.last_error":
		return "Last Error: %s"
	case "panel.qq.occupied_by":
		return "Occupied by: %s"
	case "panel.qq.create":
		return "Create"
	case "panel.qq.bot_input":
		return "QQ Bot: %s"
	case "panel.qq.create_format":
		return "Format: <bot-id> <appid> <appsecret>"
	case "panel.qq.create_example":
		return "Example: qq-main 123456 secret-value"
	case "panel.qq.create_hint":
		return "Enter create bot • Esc cancel"
	case "panel.qq.actions_hint":
		return "j/k move • Enter or b bind bot • c bind channel • x unbind channel • u unbind bot • i create bot • Esc close"
	case "panel.qq.bind_channel":
		return "Bind Channel"
	case "panel.qq.scan_hint":
		return "Scan the QR code, add the bot, then send a message to start pairing."
	case "panel.qq.qr_code":
		return "QR Code:"
	case "panel.qq.share_link":
		return "Share Link:"
	case "panel.qq.message.no_bot":
		return "No QQ bot available."
	case "panel.qq.message.bound_success":
		return "QQ bot bound to current workspace. Use c to generate the channel bind QR code."
	case "panel.qq.message.share_generated":
		return "QQ share link generated. Scan the QR code, add the bot, then send a message to start pairing."
	case "panel.qq.message.unbound":
		return "QQ channel unbound."
	case "panel.qq.message.cleared":
		return "QQ channel authorization cleared for current workspace."
	case "panel.qq.message.added_bot":
		return "Added QQ bot %s."
	case "panel.qq.error.config_unavailable":
		return "config is unavailable"
	case "panel.qq.error.config_format":
		return "QQ bot config must be: <bot-id> <appid> <appsecret>"
	case "panel.qq.error.adapter_required":
		return "QQ adapter name is required"
	case "panel.qq.error.not_configured":
		return "QQ bot %q is not configured"
	case "panel.qq.error.disabled":
		return "QQ bot %q is disabled"
	case "panel.qq.error.not_qq_adapter":
		return "adapter %q is not a QQ bot"
	case "panel.qq.error.not_online":
		return "QQ bot %q is not online"
	case "panel.qq.error.not_online_detail":
		return "QQ bot %q is not online: %s"
	case "panel.qq.runtime.available":
		return "available"
	case "panel.qq.runtime.disabled":
		return "disabled (set im.enabled: true and restart ggcode)"
	case "panel.qq.runtime.not_started":
		return "not started (restart ggcode to initialize IM runtime)"
	case "panel.qq.status.not_started":
		return "not started"
	case "panel.qq.status.unknown":
		return "unknown"
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
  /qq                Open QQ binding panel
  /telegram          Open Telegram binding panel
  /pc                Open PC binding panel
  /discord           Open Discord binding panel
  /feishu            Open Feishu binding panel
  /slack             Open Slack binding panel
  /dingtalk          Open DingTalk binding panel
  /im                Open unified IM channels panel
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
  Esc                Cancel autocomplete / exit idle shell mode
  ↑/↓                 Browse command history (or autocomplete)
  PgUp/PgDn          Scroll conversation output
  Ctrl+C             Cancel current activity, otherwise clear input then press again to exit
  Ctrl+D             Exit immediately
  $ / !              Enter shell mode

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
	case "command.skill_agent_only":
		return "Skill %s can only be invoked by the agent."
	case "command.harness_owner_promoted":
		return "Promoted %d harness task(s) for owner %s."
	case "command.harness_review_approved":
		return "Approved harness task %s."
	case "command.harness_review_rejected":
		return "Rejected harness task %s."
	case "command.harness_promoted_many":
		return "Promoted %d harness task(s)."
	case "command.harness_promoted_one":
		return "Promoted harness task %s."
	case "command.harness_task_queued_detail":
		return "Queued harness task %s.\n- goal: %s"
	case "command.harness_tasks_empty":
		return "No harness tasks recorded."
	case "command.harness_run_start":
		return "Starting tracked harness run...\nUse /harness monitor or the Tasks/Monitor views for live state."
	case "command.harness_rerun_start":
		return "Starting tracked harness rerun...\nUse /harness monitor or the Tasks/Monitor views for live state."
	case "command.harness_rerun_invalid_status":
		return "Harness task %s is %s; only failed tasks can be rerun."
	case "command.harness_status_starting_run":
		return "Starting harness run..."
	case "command.harness_status_starting_rerun":
		return "Starting harness rerun..."
	case "command.harness_spinner_running":
		return "Running harness"
	case "command.harness_cancelled":
		return "Harness run cancelled."
	default:
		if v, ok := lookupModuleCatalog(LangEnglish, key); ok {
			return v
		}
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
	case "panel.mcp.install_spec_required":
		return "请先输入安装规格。"
	case "panel.mcp.installing_server":
		return "正在安装 MCP 服务..."
	case "panel.mcp.reconnect_unavailable":
		return "当前会话不支持重新连接。"
	case "panel.mcp.reconnecting":
		return "正在重新连接 %s..."
	case "panel.mcp.reconnect_failed":
		return "无法重新连接 %s。"
	case "panel.mcp.installing_browser_preset":
		return "正在安装浏览器自动化 MCP 预设..."
	case "panel.mcp.uninstalling":
		return "正在卸载 %s..."
	case "panel.startup":
		return "正在初始化"
	case "panel.approval_required":
		return "需要确认"
	case "panel.bypass_approval":
		return "Bypass 模式确认"
	case "panel.review_file_change":
		return "审阅文件修改"
	case "panel.im":
		return "IM"
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
	case "im.none":
		return "未配置适配器"
	case "im.summary":
		return "%d 个适配器 • %d 个健康"
	case "im.more":
		return "还有 %d 个（/qq）"
	case "im.runtime.available":
		return "运行时可用"
	case "im.runtime.disabled":
		return "已禁用"
	case "im.runtime.not_started":
		return "已启用 • 重启后初始化"
	case "im.status.not_started":
		return "未启动"
	case "context.until_compact":
		return "后触发"
	case "empty.ask":
		return "你可以让我做重构、修复 bug、解释代码或补测试。"
	case "empty.tips":
		return "提示：用 @path 引用文件，/? 查看帮助，Shift+Tab 切换模式。"
	case "startup.banner":
		return "正在准备终端界面并过滤启动期的终端噪声。你现在就可以输入；一旦界面进入可交互状态，这个提示会自动消失。"
	case "harness.views":
		return "视图"
	case "harness.items":
		return "条目"
	case "harness.action":
		return "操作"
	case "harness.details":
		return "详情"
	case "harness.none":
		return "（无）"
	case "harness.unknown":
		return "未知"
	case "harness.unscoped":
		return "未归属"
	case "harness.unavailable":
		return "Harness 不可用"
	case "harness.unavailable_intro":
		return "已有项目可从这里开始："
	case "harness.unavailable_step_init":
		return "  1. 按 Enter 或 i 初始化 harness"
	case "harness.unavailable_step_refresh":
		return "  2. 初始化完成后按 r 刷新"
	case "harness.section.init":
		return "初始化"
	case "harness.section.check":
		return "检查"
	case "harness.section.doctor":
		return "诊断"
	case "harness.section.monitor":
		return "监控"
	case "harness.section.gc":
		return "清理"
	case "harness.section.contexts":
		return "上下文"
	case "harness.section.tasks":
		return "任务"
	case "harness.section.queue":
		return "排队"
	case "harness.section.run":
		return "运行"
	case "harness.section.run_queued":
		return "运行队列"
	case "harness.section.inbox":
		return "收件箱"
	case "harness.section.review":
		return "评审"
	case "harness.section.promote":
		return "晋升"
	case "harness.section.release":
		return "发布"
	case "harness.section.rollouts":
		return "发布波次"
	case "harness.hints.unavailable":
		return "Enter/i 初始化 • r 刷新 • Esc 关闭"
	case "harness.hints.move":
		return "j/k 移动"
	case "harness.hints.tab":
		return "Tab 切换"
	case "harness.hints.refresh":
		return "r 刷新"
	case "harness.hints.close":
		return "Esc 关闭"
	case "harness.hints.check":
		return "Enter 运行检查"
	case "harness.hints.monitor":
		return "Enter 刷新快照"
	case "harness.hints.gc":
		return "Enter 执行清理"
	case "harness.hints.type_goal":
		return "输入目标"
	case "harness.hints.queue":
		return "Enter 排队"
	case "harness.hints.run":
		return "Enter 运行"
	case "harness.hints.focus_input":
		return "Tab 聚焦输入"
	case "harness.hints.rerun":
		return "Enter 重跑失败任务"
	case "harness.hints.next":
		return "Enter 跑下一个"
	case "harness.hints.all":
		return "a 全部"
	case "harness.hints.retry_failed":
		return "f 重试失败"
	case "harness.hints.resume":
		return "s 恢复"
	case "harness.hints.promote_owner":
		return "p 晋升该 owner"
	case "harness.hints.retry_owner":
		return "f 重试该 owner"
	case "harness.hints.approve":
		return "Enter 通过"
	case "harness.hints.reject":
		return "x 拒绝"
	case "harness.hints.promote":
		return "Enter 晋升"
	case "harness.hints.apply_batch":
		return "Enter 应用批次"
	case "harness.hints.advance":
		return "Enter 推进"
	case "harness.hints.approve_gate":
		return "g 批准 gate"
	case "harness.hints.pause_resume":
		return "p 暂停/恢复"
	case "harness.hints.abort":
		return "x 中止"
	case "harness.hint.primary.check":
		return "按 Enter 运行检查。"
	case "harness.hint.primary.monitor":
		return "按 Enter 刷新监控快照。"
	case "harness.hint.primary.gc":
		return "按 Enter 运行垃圾清理。"
	case "harness.hint.primary.queue":
		return "输入目标后按 Enter 加入队列。"
	case "harness.hint.primary.run":
		return "输入目标后按 Enter 开始运行。"
	case "harness.hint.primary.tasks":
		return "按 Enter 重跑所选失败任务。"
	case "harness.hint.primary.run_queued":
		return "Enter 跑下一个；a 全部运行；f 重试失败；s 恢复中断。"
	case "harness.hint.primary.inbox":
		return "按 p 晋升该 owner，或按 f 重试该 owner。"
	case "harness.hint.primary.review":
		return "按 Enter 通过，或按 x 拒绝。"
	case "harness.hint.primary.promote":
		return "按 Enter 晋升所选任务。"
	case "harness.hint.primary.release":
		return "按 Enter 应用当前发布批次。"
	case "harness.hint.primary.rollouts":
		return "按 Enter 推进；g 批准 gate；p 暂停/恢复；x 中止。"
	case "harness.hint.primary.none":
		return "这个分区不需要内联输入。"
	case "harness.message.read_only":
		return "当前有其他运行进行中，Harness 面板为只读。"
	case "harness.message.monitor_refreshed":
		return "Harness 监控已刷新。"
	case "harness.message.rerun_failed_only":
		return "Harness 任务 %s 当前是 %s；只有失败任务才能重跑。"
	case "harness.message.review_approved":
		return "已通过 %s 的评审"
	case "harness.message.review_rejected":
		return "已拒绝 %s 的评审"
	case "harness.message.promoted":
		return "已晋升 %s"
	case "harness.message.no_release_tasks":
		return "没有可发布的 harness 任务。"
	case "harness.message.release_applied":
		return "已应用发布批次 %s"
	case "harness.message.no_rollouts":
		return "没有持久化的 rollout。"
	case "harness.message.rollout_advanced":
		return "已推进 rollout %s"
	case "harness.message.owner_promoted":
		return "已为 %2$s 晋升 %1$d 个任务"
	case "harness.message.owner_retried":
		return "已为 %s 重试失败任务"
	case "harness.message.gate_approved":
		return "已批准 %s 的下一个 gate"
	case "harness.message.rollout_resumed":
		return "已恢复 rollout %s"
	case "harness.message.rollout_paused":
		return "已暂停 rollout %s"
	case "harness.message.rollout_aborted":
		return "已中止 rollout %s"
	case "harness.message.check_passed":
		return "Harness 检查已通过。"
	case "harness.message.check_failed":
		return "Harness 检查发现问题。"
	case "harness.message.gc_complete":
		return "Harness 清理完成。"
	case "harness.message.queue_goal_required":
		return "请先在面板输入框里填写排队目标。"
	case "harness.message.queued":
		return "已加入 harness 队列：%s"
	case "harness.activity.status":
		return "Harness %s"
	case "harness.log.phase":
		return "阶段"
	case "harness.log.worker":
		return "Worker"
	case "harness.tool.read_file":
		return "读取文件"
	case "harness.tool.write_file":
		return "写入文件"
	case "harness.tool.browse_files":
		return "浏览文件"
	case "harness.tool.search_code":
		return "搜索代码"
	case "harness.tool.run_command":
		return "执行命令"
	case "harness.tool.fetch_web_page":
		return "抓取网页"
	case "harness.tool.run_subagent":
		return "运行子代理"
	case "harness.tool.update_task_state":
		return "更新任务状态"
	case "harness.message.run_goal_required":
		return "请先在面板输入框里填写运行目标。"
	case "harness.message.no_queued_executed":
		return "没有执行任何已排队的 harness 任务。"
	case "harness.message.queue_retried":
		return "已重试 %d 个失败的排队任务。"
	case "harness.message.queue_resumed":
		return "已恢复 %d 个中断的排队任务。"
	case "harness.message.queue_ran":
		return "已运行 %d 个排队任务。"
	case "harness.preview.not_initialized":
		return "当前项目还没有初始化 harness。\n\n按 Enter 或 i 可在当前仓库中运行 harness init。"
	case "harness.preview.check":
		return "对当前项目运行 harness 检查。\n\nEnter：执行所需的文件/内容/上下文检查，以及配置中的校验命令。"
	case "harness.preview.gc":
		return "运行 harness 垃圾清理。\n\nEnter：归档陈旧任务、放弃陈旧的 blocked/running 工作、清理旧日志，并移除孤儿 worktree。"
	case "harness.preview.queue_help":
		return "在这里输入 harness 目标，然后按 Enter 加入队列。"
	case "harness.preview.run_help":
		return "在这里输入 harness 目标，然后按 Enter 开始运行。"
	case "harness.preview.run_queued":
		return "队列状态：\nqueued=%d running=%d blocked=%d failed=%d\n\nEnter 运行下一个可执行任务。\na 运行全部可执行任务。\nf 重试失败任务。\ns 恢复中断任务。"
	case "harness.preview.no_owner":
		return "当前没有选中的 harness owner。"
	case "harness.preview.no_context":
		return "当前没有选中的 harness 上下文。"
	case "harness.preview.no_task":
		return "当前没有选中的 harness 任务。"
	case "harness.preview.project_not_initialized":
		return "当前项目还没有初始化 harness。"
	case "harness.preview.project_initialized":
		return "Harness 已初始化。"
	case "harness.preview.project_help":
		return "使用 /harness 浏览并操作控制平面。"
	case "harness.preview.no_doctor":
		return "没有 harness 诊断报告。"
	case "harness.preview.monitor_unavailable":
		return "Harness 监控不可用。"
	case "harness.label.context_title":
		return "上下文"
	case "harness.label.owner_title":
		return "Owner"
	case "harness.label.id":
		return "id"
	case "harness.label.status":
		return "状态"
	case "harness.label.goal":
		return "目标"
	case "harness.label.attempts":
		return "尝试次数"
	case "harness.label.depends_on":
		return "依赖"
	case "harness.label.context":
		return "上下文"
	case "harness.label.workspace":
		return "工作区"
	case "harness.label.branch":
		return "分支"
	case "harness.label.worker":
		return "worker"
	case "harness.label.progress":
		return "进度"
	case "harness.label.verification":
		return "验证"
	case "harness.label.changed_files":
		return "变更文件数"
	case "harness.label.delivery_report":
		return "交付报告"
	case "harness.label.delivery_report_human":
		return "交付报告"
	case "harness.label.log":
		return "日志"
	case "harness.label.review":
		return "评审"
	case "harness.label.review_notes":
		return "评审备注"
	case "harness.label.promotion":
		return "晋升"
	case "harness.label.promotion_notes":
		return "晋升备注"
	case "harness.label.release_batch":
		return "发布批次"
	case "harness.label.release_batch_human":
		return "发布批次"
	case "harness.label.release_notes":
		return "发布备注"
	case "harness.label.error":
		return "错误"
	case "harness.label.name":
		return "名称"
	case "harness.label.description":
		return "描述"
	case "harness.label.owner":
		return "Owner"
	case "harness.label.commands":
		return "命令数"
	case "harness.label.tasks":
		return "任务"
	case "harness.label.rollouts":
		return "rollout"
	case "harness.label.gates":
		return "gate"
	case "harness.label.latest":
		return "最近任务"
	case "harness.label.repo":
		return "仓库"
	case "harness.label.config":
		return "配置"
	case "harness.label.project":
		return "项目"
	case "harness.label.structure":
		return "结构"
	case "harness.label.contexts":
		return "上下文数"
	case "harness.label.workers":
		return "worker"
	case "harness.label.workflow":
		return "流程"
	case "harness.label.quality":
		return "质量"
	case "harness.label.worktrees":
		return "worktree"
	case "harness.label.snapshot":
		return "快照"
	case "harness.label.events":
		return "事件"
	case "harness.label.target":
		return "目标"
	case "harness.label.review_ready":
		return "待评审"
	case "harness.label.promotion_ready":
		return "待晋升"
	case "harness.label.retryable":
		return "可重试"
	case "harness.task_title":
		return "Harness 任务"
	case "harness.doctor_title":
		return "Harness 诊断"
	case "harness.monitor_title":
		return "Harness 监控"
	case "harness.latest_task":
		return "最近任务"
	case "harness.latest_event":
		return "最近事件"
	case "harness.focus":
		return "关注对象"
	case "harness.status.ok":
		return "正常"
	case "harness.status.needs_attention":
		return "需要关注"
	case "harness.group.review":
		return "评审"
	case "harness.group.promotion":
		return "晋升"
	case "harness.group.retry":
		return "重试"
	case "harness.review_ready_short":
		return "评审"
	case "harness.promote_ready_short":
		return "晋升"
	case "harness.tasks_count":
		return "任务"
	case "harness.input_empty":
		return "（输入框为空）"
	case "harness.no_waves":
		return "没有波次"
	case "harness.mixed":
		return "混合"
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
	case "interrupt.delivered":
		return "[已送达当前运行，正在调整方向]\n"
	case "status.thinking":
		return "思考中..."
	case "status.writing":
		return "输出中..."
	case "status.cancelling":
		return "取消中..."
	case "status.compacting":
		return "[正在压缩会话以保持在上下文窗口内]"
	case "status.compacted":
		return "[会话已压缩]"
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
	case "approval.execute_plan":
		return "执行方案"
	case "plan.confirm_execute":
		return "是否执行此方案？"
	case "plan.empty":
		return "(空方案)"
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
	case "mode.persist_failed":
		return "持久化模式偏好失败：%v"
	case "input.placeholder":
		return "输入消息...（$ / ! 进入 shell 模式）"
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
	case "panel.provider.auth":
		return "认证"
	case "panel.provider.env_var":
		return "环境变量"
	case "panel.provider.api_key":
		return "API Key"
	case "panel.provider.api_key.missing":
		return "未配置"
	case "panel.provider.api_key.configured":
		return "已配置"
	case "panel.provider.auth.connected":
		return "已连接"
	case "panel.provider.auth.not_connected":
		return "未连接"
	case "panel.provider.base_url":
		return "Base URL"
	case "panel.provider.base_url.not_set":
		return "(未设置)"
	case "panel.provider.enterprise_url":
		return "企业地址"
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
	case "panel.provider.hint.copilot":
		return "GitHub Copilot：l 登录 • x 登出 • b 编辑企业域名"
	case "panel.provider.saved":
		return "已保存。"
	case "panel.provider.saved_activated":
		return "已保存并激活。"
	case "panel.provider.login.starting":
		return "正在启动 GitHub Copilot 登录..."
	case "panel.provider.login.instructions":
		return "打开 %s 并输入代码 %s，正在等待授权..."
	case "panel.provider.login.copied":
		return "已将 device code 复制到剪贴板。"
	case "panel.provider.login.copy_failed":
		return "复制 device code 失败：%s"
	case "panel.provider.login.browser_opened":
		return "已在浏览器中打开验证页面。"
	case "panel.provider.login.browser_failed":
		return "打开验证页面失败：%s"
	case "panel.provider.login.success":
		return "GitHub Copilot 已连接。"
	case "panel.provider.login.failed":
		return "GitHub Copilot 登录失败：%s"
	case "panel.provider.logout.success":
		return "GitHub Copilot 已断开。"
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
		return "当前模型：%s（供应商：%s）\n可用模型：%s\n使用 /model 打开模型面板，或用 /model <model-name> 直接切换。\n\n"
	case "command.provider_unknown":
		return "未知供应商：%s（可用：%v）\n\n"
	case "command.provider_switched":
		return "已切换供应商为：%s（模型：%s）\n\n"
	case "command.provider_failed":
		return "更新供应商选择失败：%v\n\n"
	case "command.provider_current":
		return "当前供应商：%s（接口：%s，模型：%s）\n可用供应商：%s\n可用接口：%s\n用法：/provider [vendor] [endpoint]\n\n"
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
	case "slash.qq":
		return "管理 QQ 渠道绑定"
	case "slash.telegram":
		return "管理 Telegram 渠道绑定"
	case "slash.pc":
		return "管理 PC 渠道绑定"
	case "slash.discord":
		return "管理 Discord 渠道绑定"
	case "slash.feishu":
		return "管理飞书渠道绑定"
	case "slash.slack":
		return "管理 Slack 渠道绑定"
	case "slash.dingtalk":
		return "管理钉钉渠道绑定"
	case "slash.im":
		return "打开统一 IM 渠道面板"
	case "panel.qq.directory":
		return "目录"
	case "panel.qq.runtime":
		return "运行时"
	case "panel.qq.bots":
		return "QQ 机器人"
	case "panel.qq.created":
		return "已创建：%d"
	case "panel.qq.bound":
		return "已绑定：%d"
	case "panel.qq.available":
		return "可用：%d"
	case "panel.qq.current_binding":
		return "当前绑定"
	case "panel.qq.none":
		return "(无)"
	case "panel.qq.default":
		return "(默认)"
	case "panel.qq.adapter":
		return "机器人：%s"
	case "panel.qq.target":
		return "目标：%s"
	case "panel.qq.channel":
		return "渠道：%s"
	case "panel.qq.bot_list":
		return "QQ 机器人列表"
	case "panel.qq.no_bots":
		return "没有配置 QQ 机器人。"
	case "panel.qq.entry.available":
		return "可用"
	case "panel.qq.entry.bound":
		return "已绑定"
	case "panel.qq.details":
		return "详情"
	case "panel.qq.status":
		return "状态：%s"
	case "panel.qq.transport":
		return "传输：%s"
	case "panel.qq.bound_directory":
		return "绑定目录：%s"
	case "panel.qq.current_directory_target":
		return "当前目录目标：%s"
	case "panel.qq.current_directory_channel":
		return "当前目录渠道：%s"
	case "panel.qq.waiting_for_pairing":
		return "(等待配对)"
	case "panel.qq.last_error":
		return "最近错误：%s"
	case "panel.qq.occupied_by":
		return "已被占用：%s"
	case "panel.qq.create":
		return "创建"
	case "panel.qq.bot_input":
		return "QQ 机器人：%s"
	case "panel.qq.create_format":
		return "格式：<bot-id> <appid> <appsecret>"
	case "panel.qq.create_example":
		return "示例：qq-main 123456 secret-value"
	case "panel.qq.create_hint":
		return "Enter 创建机器人 • Esc 取消"
	case "panel.qq.actions_hint":
		return "j/k 移动 • Enter 或 b 绑定机器人 • c 绑定渠道 • x 解绑渠道 • u 解绑机器人 • i 创建机器人 • Esc 关闭"
	case "panel.qq.bind_channel":
		return "绑定渠道"
	case "panel.qq.scan_hint":
		return "扫描二维码添加机器人，然后发送一条消息开始配对。"
	case "panel.qq.qr_code":
		return "二维码："
	case "panel.qq.share_link":
		return "分享链接："
	case "panel.qq.message.no_bot":
		return "当前没有可用的 QQ 机器人。"
	case "panel.qq.message.bound_success":
		return "QQ 机器人已绑定到当前工作区。按 c 生成渠道绑定二维码。"
	case "panel.qq.message.share_generated":
		return "已生成 QQ 分享链接。扫描二维码添加机器人，然后发送一条消息开始配对。"
	case "panel.qq.message.unbound":
		return "已解绑 QQ 渠道。"
	case "panel.qq.message.cleared":
		return "已清除当前工作区的 QQ 渠道授权。"
	case "panel.qq.message.added_bot":
		return "已添加 QQ 机器人 %s。"
	case "panel.qq.error.config_unavailable":
		return "配置不可用"
	case "panel.qq.error.config_format":
		return "QQ 机器人配置格式必须为：<bot-id> <appid> <appsecret>"
	case "panel.qq.error.adapter_required":
		return "QQ 适配器名称不能为空"
	case "panel.qq.error.not_configured":
		return "QQ 机器人 %q 未配置"
	case "panel.qq.error.disabled":
		return "QQ 机器人 %q 已禁用"
	case "panel.qq.error.not_qq_adapter":
		return "适配器 %q 不是 QQ 机器人"
	case "panel.qq.error.not_online":
		return "QQ 机器人 %q 未在线"
	case "panel.qq.error.not_online_detail":
		return "QQ 机器人 %q 未在线：%s"
	case "panel.qq.runtime.available":
		return "可用"
	case "panel.qq.runtime.disabled":
		return "已禁用（设置 im.enabled: true 并重启 ggcode）"
	case "panel.qq.runtime.not_started":
		return "未启动（重启 ggcode 以初始化 IM 运行时）"
	case "panel.qq.status.not_started":
		return "未启动"
	case "panel.qq.status.unknown":
		return "未知"
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
  /qq                打开 QQ 绑定面板
  /telegram          打开 Telegram 绑定面板
  /pc                打开 PC 绑定面板
  /discord           打开 Discord 绑定面板
  /feishu            打开飞书绑定面板
  /slack             打开 Slack 绑定面板
  /dingtalk          打开钉钉绑定面板
  /im                打开统一 IM 渠道面板
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
  Esc                取消补全 / 在空闲 shell 模式下退出命令模式
  ↑/↓                 浏览命令历史（或补全）
  PgUp/PgDn          滚动对话输出
  Ctrl+C             取消当前活动；空闲时先清空输入，再次按下退出
  Ctrl+D             立即退出
  $ / !              进入 shell 模式

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
	case "command.skill_agent_only":
		return "技能 %s 只能由 agent 调用。"
	case "command.harness_owner_promoted":
		return "已为 owner %[2]s 推进 %[1]d 个 harness 任务。"
	case "command.harness_review_approved":
		return "已批准 harness 任务 %s。"
	case "command.harness_review_rejected":
		return "已拒绝 harness 任务 %s。"
	case "command.harness_promoted_many":
		return "已推进 %d 个 harness 任务。"
	case "command.harness_promoted_one":
		return "已推进 harness 任务 %s。"
	case "command.harness_task_queued_detail":
		return "已加入 harness 队列：%s。\n- 目标：%s"
	case "command.harness_tasks_empty":
		return "还没有记录任何 harness 任务。"
	case "command.harness_run_start":
		return "正在启动跟踪型 harness 运行...\n可使用 /harness monitor 或 Tasks/Monitor 视图查看实时状态。"
	case "command.harness_rerun_start":
		return "正在启动跟踪型 harness 重跑...\n可使用 /harness monitor 或 Tasks/Monitor 视图查看实时状态。"
	case "command.harness_rerun_invalid_status":
		return "Harness 任务 %s 当前是 %s；只有失败任务才能重跑。"
	case "command.harness_status_starting_run":
		return "正在启动 harness 运行..."
	case "command.harness_status_starting_rerun":
		return "正在启动 harness 重跑..."
	case "command.harness_spinner_running":
		return "正在运行 harness"
	case "command.harness_cancelled":
		return "Harness 运行已取消。"
	default:
		if v, ok := lookupModuleCatalog(LangZhCN, key); ok {
			return v
		}
		return key
	}
}
