package tui

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/harness"
	"github.com/topcheer/ggcode/internal/memory"
	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/safego"
)

func (m *Model) updateAutoComplete() {
	// Check for slash command
	if active, prefix := DetectSlashCommand(m.input.Value(), inputCursor(&m.input)); active {
		m.refreshCommands()
		matches := CompleteSlashCommand("/"+prefix, m.customCmds)
		if len(matches) > 0 {
			m.autoCompleteActive = true
			m.autoCompleteKind = "slash"
			m.autoCompleteItems = matches
			// Reset index if the filtered list changed
			if m.autoCompleteIndex >= len(matches) {
				m.autoCompleteIndex = 0
			}
			return
		}
	}

	// Check for @mention
	if active, prefix := DetectMention(m.input.Value(), inputCursor(&m.input)); active {
		workDir, _ := os.Getwd()
		matches := CompleteMention(prefix, workDir)
		if len(matches) > 0 {
			m.autoCompleteActive = true
			m.autoCompleteKind = "mention"
			m.autoCompleteWorkDir = workDir
			m.autoCompleteItems = matches
			if m.autoCompleteIndex >= len(matches) {
				m.autoCompleteIndex = 0
			}
			return
		}
	}

	// No autocomplete active
	m.autoCompleteActive = false
	m.autoCompleteItems = nil
}

func (m *Model) applyAutoComplete() tea.Cmd {
	if m.autoCompleteIndex >= len(m.autoCompleteItems) {
		return nil
	}
	selected := m.autoCompleteItems[m.autoCompleteIndex]

	value := m.input.Value()
	cursor := inputCursor(&m.input)

	var replacement string
	if m.autoCompleteKind == "slash" {
		if m.loading {
			if shouldExecuteWhileBusy(selected) {
				m.input.SetValue("")
				m.autoCompleteActive = false
				m.autoCompleteItems = nil
				m.autoCompleteIndex = 0
				m.history = append(m.history, selected)
				m.historyIdx = len(m.history)
				return m.handleCommand(selected)
			}
			m.input.SetValue(selected)
			m.autoCompleteActive = false
			m.autoCompleteItems = nil
			m.autoCompleteIndex = 0
			return nil
		}
		m.input.SetValue("")
		m.autoCompleteActive = false
		m.autoCompleteItems = nil
		m.history = append(m.history, selected)
		m.historyIdx = len(m.history)
		return m.handleCommand(selected)
	}

	if m.autoCompleteKind == "mention" {
		// Replace from the "@" to cursor with the selected path
		atPos := cursor - 1
		for atPos >= 0 && value[atPos] != '@' {
			atPos--
		}
		replacement = "@" + selected + " "
		value = value[:atPos] + replacement + value[cursor:]
	}

	m.input.SetValue(value)
	m.autoCompleteActive = false
	m.autoCompleteItems = nil
	m.autoCompleteIndex = 0
	return nil
}

func (m *Model) submitText(text string, addToHistory bool) tea.Cmd {
	// Notify Knight that user is active (resets idle timer)
	if m.knight != nil && strings.TrimSpace(text) != "" {
		m.knight.NotifyActivity()
	}
	text = m.stripPendingImagePlaceholder(text)
	if addToHistory {
		if text != "" {
			m.history = append(m.history, text)
			m.historyIdx = len(m.history)
		}
	}
	debug.Log("tui", "handleCommand: %s", text)
	return m.handleCommand(text)
}

// shouldExecuteWhileBusy returns true for commands that should run immediately
// even when the agent is loading (instead of being queued as pending submissions).
// Built-in slash commands that only open panels or change settings are safe;
// custom commands (which may start a new agent run) and /harness subcommands
// (which may start runs) are excluded.
func shouldExecuteWhileBusy(text string) bool {
	t := strings.TrimSpace(text)
	if !strings.HasPrefix(t, "/") {
		return false
	}
	parts := strings.Fields(t)
	if len(parts) == 0 {
		return false
	}
	cmd := parts[0]
	switch cmd {
	// Panel / UI commands — always safe
	case "/lang", "/model", "/provider", "/impersonate",
		"/qq", "/telegram", "/tg", "/pc", "/discord",
		"/feishu", "/lark", "/slack", "/dingtalk", "/ding", "/im",
		"/skills", "/sessions", "/mcp",
		"/checkpoints", "/memory", "/todo", "/plugins", "/config", "/status",
		"/help", "/?":
		return true
	// Harness: only the bare command (opens panel) is safe
	case "/harness":
		return len(parts) == 1 || (len(parts) == 2 && strings.EqualFold(parts[1], "panel"))
	}
	return false
}

func (m *Model) handleCommand(text string) tea.Cmd {
	if m.knight != nil && strings.TrimSpace(text) != "" {
		m.knight.NotifyActivity()
	}
	if shellCommand, ok := parseShellCommand(text); ok {
		m.setShellMode(true)
		return m.submitShellCommand(shellCommand, true)
	}

	// Slash commands
	if strings.HasPrefix(text, "/") {
		m.refreshCommands()
		parts := strings.Fields(text)
		cmd := strings.ToLower(parts[0])
		switch cmd {
		case "/exit", "/quit":
			m.quitting = true
			return tea.Quit
		case "/clear":
			m.resetConversationView()
			return nil
		case "/help", "/?":
			m.chatWriteSystem(nextSystemID(), m.helpText())
			return nil
		case "/model":
			if len(parts) > 1 {
				if err := m.config.SetActiveSelection(m.config.Vendor, m.config.Endpoint, parts[1]); err == nil {
					if err := m.reloadActiveProvider(); err == nil {
						m.chatWriteSystem(nextSystemID(), m.t("command.model_switched", parts[1], m.config.Vendor))
					} else {
						m.chatWriteSystem(nextSystemID(), m.t("command.model_failed", err))
					}
				} else {
					m.chatWriteSystem(nextSystemID(), m.t("command.model_failed", err))
				}
			} else {
				resolved, err := m.config.ResolveActiveEndpoint()
				if err != nil {
					m.chatWriteSystem(nextSystemID(), m.t("command.model_failed", err))
				} else {
					m.chatWriteSystem(nextSystemID(), m.t("command.model_current", resolved.Model, resolved.VendorName, strings.Join(uniqueStrings(append([]string(nil), resolved.Models...)), ", ")))
				}
				return m.openModelPanel()
			}
			return nil
		case "/impersonate":
			m.openImpersonatePanel()
			return nil
		case "/provider":
			if len(parts) > 1 {
				newVendor := parts[1]
				endpoints := m.config.EndpointNames(newVendor)
				if len(endpoints) == 0 {
					m.chatWriteSystem(nextSystemID(), m.t("command.provider_unknown", newVendor, m.vendorNames()))
					return nil
				}
				endpoint := endpoints[0]
				if len(parts) > 2 {
					endpoint = parts[2]
				}
				if err := m.config.SetActiveSelection(newVendor, endpoint, ""); err == nil {
					if err := m.reloadActiveProvider(); err == nil {
						m.chatWriteSystem(nextSystemID(), m.t("command.provider_switched", newVendor, m.config.Model))
					} else {
						m.chatWriteSystem(nextSystemID(), m.t("command.provider_failed", err))
					}
				} else {
					m.chatWriteSystem(nextSystemID(), m.t("command.provider_failed", err))
				}
			} else {
				if summary := m.providerCommandSummary(); summary != "" {
					m.chatWriteSystem(nextSystemID(), summary)
				}
				m.openProviderPanel()
			}
			return nil
		case "/qq":
			return m.handleQQCommand()
		case "/telegram", "/tg":
			m.openTGPanel()
			return nil
		case "/pc":
			m.openPCPanel()
			return nil
		case "/discord":
			m.openDiscordPanel()
			return nil
		case "/feishu", "/lark":
			m.openFeishuPanel()
			return nil
		case "/slack":
			m.openSlackPanel()
			return nil
		case "/dingtalk", "/ding":
			m.openDingtalkPanel()
			return nil
		case "/im":
			m.openIMPanel()
			return nil
		case "/allow":
			if len(parts) > 1 {
				if m.policy != nil {
					m.policy.SetOverride(parts[1], permission.Allow)
					m.chatWriteSystem(nextSystemID(), m.t("command.allow_set", parts[1]))
				}
			} else {
				m.chatWriteSystem(nextSystemID(), m.t("command.usage.allow"))
			}
			return nil
		case "/sessions":
			m.openInspectorPanel(inspectorPanelSessions)
			return nil
		case "/resume":
			if len(parts) > 1 {
				return m.resumeSession(parts[1])
			}
			m.openInspectorPanel(inspectorPanelSessions)
			return nil
		case "/export":
			if len(parts) > 1 {
				return m.exportSession(parts[1])
			}
			m.openInspectorPanel(inspectorPanelSessions)
			return nil
		case "/plugins":
			return m.handlePluginsCommand()
		case "/image":
			return m.handleImageCommand(parts)
		case "/mcp":
			return m.handleMCPCommand()
		case "/skills":
			m.openSkillsPanel()
			return nil
		case "/mode":
			return m.handleModeCommand(parts)
		case "/init":
			return m.handleInitCommand()
		case "/harness":
			return m.handleHarnessCommand(parts)
		case "/lang":
			return m.handleLangCommand(parts)
		case "/memory":
			return m.handleMemoryCommand(parts)
		case "/undo":
			return m.handleUndoCommand()
		case "/checkpoints":
			return m.handleCheckpointsCommand()
		case "/compact":
			return m.handleCompactCommand()
		case "/todo":
			return m.handleTodoCommand(parts)
		case "/bug":
			return m.handleBugCommand()
		case "/config":
			return m.handleConfigCommand(parts)
		case "/status":
			return m.handleStatusCommand()
		case "/knight":
			return m.handleKnightCommand(parts)
		case "/update":
			return m.handleUpdateCommand()
		case "/restart":
			return m.handleRestartCommand()
		default:
			// Check custom commands
			if cmdName := strings.TrimPrefix(cmd, "/"); cmdName != "" {
				if custom, ok := m.customCmds[cmdName]; ok {
					if !custom.UserInvocable {
						m.chatWriteSystem(nextSystemID(), m.t("command.skill_agent_only", custom.SlashName()))
						return nil
					}
					if m.commandMgr != nil {
						m.commandMgr.RecordUsage(cmdName)
					}
					vars := map[string]string{
						"DIR":  workingDirFromModel(m),
						"ARGS": strings.TrimSpace(strings.TrimPrefix(text, parts[0])),
					}
					expanded := custom.Expand(vars)
					m.chatWriteSystem(nextSystemID(), m.t("command.custom", cmdName))
					m.chatWriteSystem(nextSystemID(), expanded)
					m.loading = true
					// Reset status bar state
					m.statusActivity = m.t("status.thinking")
					m.statusToolName = ""
					m.statusToolArg = ""
					m.statusToolCount = 0
					return tea.Batch(m.startLoadingSpinner(m.statusActivity), m.startAgent(expanded))
				}
			}
			m.chatWriteSystem(nextSystemID(), m.t("command.unknown", text))
			m.chatWriteSystem(nextSystemID(), m.t("command.help_hint"))
			return nil
		}
	}

	// Regular message → check auto-run routing before starting agent
	displayText := text
	if m.pendingImage != nil {
		displayText = strings.TrimSpace(m.pendingImage.placeholder + " " + text)
	}

	// Auto-run routing: if harness.auto_run is configured, decide whether
	// this input should be routed to harness instead of the normal agent.
	if autoRunResult, err := m.checkAutoRun(text); err == nil && autoRunResult != nil {
		switch autoRunResult.Decision {
		case harness.RouteSuggest:
			m.pendingAutoRun = autoRunResult
			m.pendingAutoRunText = text
			m.chatWriteSystem(nextChatID(), autoRunResult.Message)
			m.chatListScrollToBottom()
			return nil
		case harness.RouteHarness:
			return m.handleAutoRun(text, autoRunResult)
		}
	}

	m.chatWriteUser(nextChatID(), displayText)
	m.chatListScrollToBottom()

	// Save original user message to session
	m.appendUserMessage(text)

	m.streamBuffer = &bytes.Buffer{}
	m.shellBuffer = nil
	m.streamPrefixWritten = false
	m.loading = true
	// Reset status bar state
	m.statusActivity = m.t("status.thinking")
	m.statusToolName = ""
	m.statusToolArg = ""
	m.statusToolCount = 0
	// ExpandMentions runs asynchronously inside startAgentWithExpand to avoid blocking UI
	return tea.Batch(m.startLoadingSpinner(m.statusActivity), m.startAgentWithExpand(text))
}

func (m *Model) handleInitCommand() tea.Cmd {
	workDir, _ := os.Getwd()
	targetPath, _, err := memory.ResolveProjectMemoryInitTarget(workDir)
	if err != nil {
		m.chatWriteSystem(nextSystemID(), m.t("init.resolve_failed", err))
		return nil
	}
	existed := false
	if _, err := os.Stat(targetPath); err == nil {
		existed = true
	}
	content, err := memory.GenerateProjectMemory(filepath.Dir(targetPath))
	if err != nil {
		m.chatWriteSystem(nextSystemID(), m.t("init.generate_failed", err))
		return nil
	}
	prompt := buildInitPrompt(targetPath, existed, content)

	m.chatWriteUser(nextChatID(), "/init")
	m.appendUserMessage("/init")

	m.streamBuffer = &bytes.Buffer{}
	m.streamPrefixWritten = false
	m.loading = true
	m.statusActivity = m.t("init.collecting")
	m.statusToolName = ""
	m.statusToolArg = ""
	m.statusToolCount = 0

	return tea.Batch(m.startLoadingSpinner(m.statusActivity), m.startAgent(prompt))
}

// checkAutoRun evaluates whether the user input should be routed to harness
// based on the harness.auto_run configuration. Returns nil if auto-run is
// disabled or the input should go to the normal agent.
func (m *Model) checkAutoRun(text string) (*harness.AutoRunResult, error) {
	if m.config == nil {
		return nil, nil
	}
	mode := m.config.Harness.AutoRunMode()
	if mode == "off" {
		return nil, nil
	}
	// In strict mode, apply write guard immediately regardless of route outcome.
	// This ensures the main agent cannot write to the project even if the input
	// is not routed to harness.
	if mode == "strict" {
		m.applyStrictWriteGuard()
	}
	workDir, _ := os.Getwd()
	ctx := harness.RouteContext{
		Input:      text,
		WorkingDir: workDir,
	}
	return harness.ShouldAutoRun(m.config, text, ctx)
}

// handleAutoRun processes a harness auto-run decision by directly executing
// a harness task. This skips the context prompt and goes straight to run.
func (m *Model) handleAutoRun(text string, result *harness.AutoRunResult) tea.Cmd {
	if result.Project == nil {
		m.chatWriteSystem(nextChatID(), "harness auto-run: no project available. Run /harness init first.")
		m.chatListScrollToBottom()
		return nil
	}

	// Use the config from auto-run result (may have strict overrides)
	// Fall back to loading from disk if not provided
	project := *result.Project
	cfg := result.Config
	if cfg == nil {
		loadedCfg, err := harness.LoadConfig(project.ConfigPath)
		if err != nil {
			m.chatWriteSystem(nextChatID(), fmt.Sprintf("harness auto-run: failed to load config: %v", err))
			m.chatListScrollToBottom()
			return nil
		}
		cfg = loadedCfg
	}

	// Show auto-run message
	m.chatWriteUser(nextChatID(), text)
	m.chatWriteSystem(nextSystemID(), fmt.Sprintf("🔀 Harness auto-run: %s", text))
	m.chatListScrollToBottom()

	// Skip context prompt — go directly to harness run execution
	// with an empty context list (auto-init'd projects have no contexts)
	return m.executeAutoHarnessRun(text, project, cfg)
}

// executeAutoHarnessRun runs a harness task directly, skipping the context
// selection prompt that manual /harness run uses.
func (m *Model) executeAutoHarnessRun(goal string, project harness.Project, cfg *harness.Config) tea.Cmd {
	m.harnessRunProject = &project
	m.harnessRunGoal = strings.TrimSpace(goal)
	m.harnessRunTaskID = ""
	m.harnessRunLogPath = ""
	m.harnessRunLogOffset = 0
	m.harnessRunLastDetail = ""
	m.harnessRunRemainder = ""
	m.harnessRunLiveTail = ""

	ctx, cancel := context.WithCancel(context.Background())
	m.cancelFunc = cancel
	m.loading = true
	m.runCanceled = false
	m.runFailed = false
	m.statusActivity = m.t("command.harness_status_starting_run")
	m.statusToolName = ""
	m.statusToolArg = ""
	m.statusToolCount = 0
	m.streamBuffer = &bytes.Buffer{}
	m.streamPrefixWritten = false

	opts := harness.RunTaskOptions{
		ContextName: "",
		ContextPath: "",
	}

	if m.program == nil {
		return func() tea.Msg {
			svc := harness.NewRunService()
			result := svc.Run(ctx, harness.RunServiceInput{
				Project: project,
				Config:  cfg,
				Goal:    goal,
				Runner:  harness.BinaryRunner{},
				Options: opts,
			})
			return harnessRunResultMsg{Summary: result.Summary, Err: result.Error, CTA: result.CTA, CTAMessage: result.CTAMessage}
		}
	}

	startSpinner := m.spinner.Start(m.t("command.harness_spinner_running"))
	safego.Go("tui.commands.autoHarnessRun", func() {
		svc := harness.NewRunService()
		result := svc.Run(ctx, harness.RunServiceInput{
			Project: project,
			Config:  cfg,
			Goal:    goal,
			Runner:  harness.BinaryRunner{},
			Options: opts,
		})
		m.program.Send(harnessRunResultMsg{Summary: result.Summary, Err: result.Error, CTA: result.CTA, CTAMessage: result.CTAMessage})
	})
	return tea.Batch(startSpinner, m.pollHarnessRunProgress())
}

// handleHarnessReviewApprove creates a tea.Cmd that approves a harness task
// and displays the result. Used by the one-key review CTA.
func (m *Model) handleHarnessReviewApprove(taskID string) tea.Cmd {
	return func() tea.Msg {
		workDir, _ := os.Getwd()
		project, _, err := loadHarnessForTUI(workDir)
		if err != nil {
			return harnessReviewResultMsg{Err: fmt.Errorf("load harness: %w", err), TaskID: taskID}
		}
		task, err := harness.ApproveTaskReview(project, taskID, "approved via auto-review CTA")
		if err != nil {
			return harnessReviewResultMsg{Err: err, TaskID: taskID}
		}
		return harnessReviewResultMsg{Task: task, TaskID: taskID}
	}
}

// applyStrictWriteGuard adds Deny rules for write tools to the permission
// policy when strict mode is active. This prevents the main agent from
// directly modifying project files.
//
// Guard exemption: BinaryRunner spawns a subprocess with its own policy,
// so the worker is NOT affected. If subagent mode (in-process) is used
// in the future, the worker must use a separate ConfigPolicy or call
// ClearOverride() for the tools it needs.
func (m *Model) applyStrictWriteGuard() {
	cp, ok := m.policy.(*permission.ConfigPolicy)
	if !ok {
		return
	}
	// Deny all file-writing tools for the main agent in strict mode.
	// The harness worker agent runs in a worktree and is not affected.
	// run_command is included because it can be used to bypass file write
	// restrictions (e.g., `echo > file`, `sed -i`).
	writeTools := []string{
		"write_file",
		"edit_file",
		"multi_edit_file",
		"notebook_edit",
		"run_command",
		"git_add",
		"git_commit",
		"git_stash",
	}
	for _, tool := range writeTools {
		cp.SetOverride(tool, permission.Deny)
	}
	debug.Log("auto-run", "strict write guard enabled: denied %v", writeTools)
}
