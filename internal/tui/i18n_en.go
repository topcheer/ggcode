package tui

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
	case "cron.firing":
		return "⏰ Cron job triggered"
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
	case "panel.session_usage":
		return "Session usage"
	case "panel.metrics":
		return "Metrics"
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
	case "label.context":
		return "context"
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
	case "label.total":
		return "total"
	case "label.cost":
		return "est. cost"
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
	case "label.output":
		return "output"
	case "label.cache_read":
		return "cache read"
	case "label.cache_write":
		return "cache write"
	case "label.cache_hit":
		return "cache hit"
	case "label.turns":
		return "turns"
	case "label.avg_ttft":
		return "avg ttft"
	case "label.p95_ttft":
		return "p95 ttft"
	case "label.avg_duration":
		return "avg dur"
	case "label.p95_duration":
		return "p95 dur"
	case "label.avg_think":
		return "avg think"
	case "label.fail_rate":
		return "fail rate"
	case "label.slow_tools":
		return "slow tools"
	case "label.recent_turns":
		return "recent turns"
	case "label.file":
		return "file"
	case "label.directory":
		return "directory"
	case "context.unavailable":
		return "No context data yet"
	case "metrics.empty":
		return "No metrics yet"
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
		return "Ctrl+V / Ctrl+Shift+V paste image"
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
	case "hint.image_attached_count":
		return "%d image(s) attached"
	case "hint.follow_panel":
		return "Ctrl+N follow"
	case "hint.unfollow_panel":
		return "Ctrl+N unfollow"
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
		return "Compressing context..."
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

	case "exit.confirm":
		return "Press Ctrl-C again to exit.\n\n"
	case "cancel.confirm":
		return "Press Ctrl-C or Esc again to cancel the running agent.\n\n"
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
		return "Type a message... ($ shell, # chat)"
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
	case "command.retry_empty":
		return "No previous submission to retry."
	case "command.retry_busy":
		return "Agent is busy. Wait for the current run to finish before retrying."
	case "command.edit_empty":
		return "No previous submission to edit."
	case "command.edit_busy":
		return "Agent is busy. Wait for the current run to finish before editing."
	case "command.edit_ready":
		return "Loaded last submission — edit and press Enter to send."
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
	case "files.disabled":
		return "Checkpointing not enabled.\n\n"
	case "files.none":
		return "No files modified by agent in this session.\n\n"
	case "files.title":
		return "Files modified by agent (%d files, %d edits):\n\n"
	case "files.item":
		return "  %s  %d edits  last: %s%s\n"
	case "files.hint":
		return "\nUse /undo to revert the most recent edit, /checkpoints for details.\n\n"
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
	case "compact.done_with_stats":
		return "Conversation history compacted (%d → %d tokens).\n\n"
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
		return "Usage: /config set <key> <value>\n\nKeys: model, vendor, endpoint, language, apikey [--vendor]\n\nEndpoints: /config add-endpoint <name> <base_url> [--protocol openai] [--apikey sk-xxx]\n          /config remove-endpoint <name>\n\n"
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
	case "update.pm_hint.brew":
		return "Update installed. Note: ggcode was installed via Homebrew.\nRun `brew upgrade ggcode` to keep Homebrew in sync.\n\n"
	case "update.pm_hint.scoop":
		return "Update installed. Note: ggcode was installed via Scoop.\nRun `scoop update ggcode` to keep Scoop in sync.\n\n"
	case "update.pm_hint.winget":
		return "Update installed. Note: ggcode was installed via winget.\nRun `winget upgrade ggcode` to keep winget in sync.\n\n"
	case "update.pm_hint.snap":
		return "Update installed. Note: ggcode was installed via Snap.\nRun `sudo snap refresh ggcode` to keep Snap in sync.\n\n"
	case "update.other_installs":
		return "Other ggcode installations detected on this system:\n%s\nIf a different ggcode appears first in PATH, consider updating it too or adjusting PATH order.\n\n"
	case "update.dual_scope":
		return "Warning: Both user and system-wide ggcode installations found:\n  User: %s\n  System: %s\nThis may cause PATH conflicts. Consider uninstalling one from Settings > Apps.\n\n"
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
		return "Usage: /image <path/to/file.png> or /image paste\n"
	case "image.formats":
		return "Supported formats: PNG, JPEG, GIF, WebP (max 20MB)\n\n"
	case "image.attached":
		return "Image attached: %s\n"
	case "image.attached_hint":
		return "Send a message to include the image, or /image to attach another.\n\n"
	case "image.clipboard_failed":
		return "Could not paste an image from the clipboard: %v"
	case "image.clipboard_no_image_windows":
		return "No image found in clipboard. On Windows, Ctrl+V is often intercepted by the terminal. Try Ctrl+Shift+V or /image paste instead."
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
	case "slash.files":
		return "Show files modified by agent"
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
	case "slash.wechat":
		return "Manage WeChat channel binding"
	case "slash.wecom":
		return "Manage WeCom (Enterprise WeChat) channel binding"
	case "slash.mattermost":
		return "Manage Mattermost channel binding"
	case "slash.matrix":
		return "Manage Matrix channel binding"
	case "slash.signal":
		return "Manage Signal channel binding"
	case "slash.irc":
		return "Manage IRC channel binding"
	case "slash.nostr":
		return "Manage Nostr channel binding"
	case "slash.twitch":
		return "Manage Twitch channel binding"
	case "slash.whatsapp":
		return "Manage WhatsApp channel binding"
	case "slash.impersonate":
		return "Impersonate a CLI tool for shell prompt display"
	case "slash.knight":
		return "Manage autonomous background agent"
	case "slash.stream":
		return "Configure streaming output mode"
	case "slash.diff":
		return "Show git diff in chat (supports --cached, <file>, --stat)"
	case "slash.hooks":
		return "Show configured hooks (all events, types, match patterns)"
	case "slash.cost":
		return "Show session token usage and estimated cost"
	case "slash.review":
		return "AI code review of current changes (bugs, security, races)"
	case "slash.copy":
		return "Copy last assistant response to clipboard"
	case "slash.context":
		return "Show context window usage breakdown (tokens, messages, capacity)"
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
	case "panel.qq.entry.active":
		return "Active"
	case "panel.qq.entry.bound_other":
		return "Bound: %s"
	case "panel.qq.entry.muted":
		return "Muted"
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
	case "regenerate.busy":
		return "Cannot regenerate while the agent is running. Press Ctrl+C to cancel first."
	case "regenerate.no_agent":
		return "Agent not initialized."
	case "regenerate.no_context":
		return "Context manager not available."
	case "regenerate.no_response":
		return "No assistant response to regenerate."
	case "branch.busy":
		return "Cannot branch while the agent is running. Press Ctrl+C to cancel first."
	case "branch.no_session":
		return "No active session to branch."
	case "branch.empty":
		return "Session has no messages to branch."
	case "branch.save_failed":
		return "Failed to create branched session: %v"
	case "branch.success":
		return "Branched to new session %s (from: %s). Original session is preserved."
	case "help.text":
		return `Available commands:

Session & History:
  /help, /?          Show this help message
  /sessions          List all saved sessions
  /resume <id>       Resume a previous session
  /export <id>       Export session to markdown file
  /clear             Clear conversation history
  /compact           Compress conversation history (manual)
  /undo              Undo the last file edit (checkpoint rollback)
  /checkpoints       List all file edit checkpoints
  /regenerate        Discard last response and regenerate (alias: /regen)
  /branch            Fork current conversation into a new session (alias: /fork)

Model & Provider:
  /model [name]      Open model panel or switch directly
  /provider [vendor] Open provider manager
  /mode <mode>       Set agent mode (supervised|plan|auto|bypass|autopilot)

Development:
  /diff [opts]       Show git diff in chat (--cached, --stat, <file>)
  /review [opts]     AI code review of current changes (--cached, --staged)
  /copy              Copy last assistant response to clipboard
  /cost              Show session token usage and estimated cost
  /context           Show context window usage breakdown
  /hooks             Show configured hooks
  /init              Generate GGCODE.md from the current project
  /harness ...       Run harness control-plane commands
  /todo              View todo list
  /todo clear        Clear todo list

Integrations:
  /im                Open unified IM channels panel
  /mcp               Show connected MCP servers and tools
  /plugins           List loaded plugins and their tools
  /skills            Browse available skills
  /memory            Show loaded memory files
  /agents            List sub-agents

System:
  /lang [code]       Choose or switch interface language
  /config            Show current configuration
  /config set <k> <v> Set a config value
  /status            Show current status
  /update            Update ggcode to the latest release
  /restart           Restart ggcode (picks up latest binary)
  /bug               Report a bug with diagnostics
  /exit, /quit       Exit ggcode

Keyboard shortcuts:
  Tab                Cycle autocomplete or approval choices
  Shift+Tab          Reverse cycle autocomplete, otherwise toggle permission mode
  Ctrl+R             Toggle sidebar
  Ctrl+N/P           New/previous session
  Ctrl+T             Open tunnel (mobile sharing)
  Enter              Send message / apply current selection
  Esc                Cancel autocomplete / exit idle shell mode
  Up/Down            Browse command history (or autocomplete)
  PgUp/PgDn          Scroll conversation output
  Ctrl+C             Cancel current activity, otherwise clear input then press again to exit
  Ctrl+D             Exit immediately
  Ctrl+A / Ctrl+E    Move cursor to start / end of line
  Ctrl+K             Delete from cursor to end of line
  Ctrl+U             Delete from start of line to cursor
  Ctrl+W             Delete word before cursor
  Ctrl+Backspace     Remove last attached image
  Shift+Enter        Insert newline (Ctrl+J or Alt+Enter in tmux)
  $ / !              Enter shell mode
  #                  Enter LAN Chat quick-send mode

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
	case "tunnel.stopped":
		return "Tunnel stopped."
	case "tunnel.not_active":
		return "No active sharing session."
	case "tunnel.mobile_connected":
		return "Mobile client connected."
	default:
		if v, ok := lookupModuleCatalog(LangEnglish, key); ok {
			return v
		}
		return key
	}
}
