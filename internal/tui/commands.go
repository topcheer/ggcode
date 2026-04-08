package tui

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/diff"
	"github.com/topcheer/ggcode/internal/harness"
	"github.com/topcheer/ggcode/internal/image"
	"github.com/topcheer/ggcode/internal/memory"
	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/version"
	"runtime"
)

var executeHarnessRun = func(ctx context.Context, project harness.Project, cfg *harness.Config, goal string) (*harness.RunSummary, error) {
	return harness.RunTask(ctx, project, cfg, goal, harness.BinaryRunner{})
}

var executeHarnessRerun = func(ctx context.Context, project harness.Project, cfg *harness.Config, taskID string) (*harness.RunSummary, error) {
	return harness.RerunTask(ctx, project, cfg, taskID, harness.BinaryRunner{})
}

func (m *Model) updateAutoComplete() {
	// Check for slash command
	if active, prefix := DetectSlashCommand(m.input); active {
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
	if active, prefix := DetectMention(m.input); active {
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
	cursor := m.input.Position()

	var replacement string
	if m.autoCompleteKind == "slash" {
		if m.loading {
			if shouldAllowBusyHarnessPanel(selected) {
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

func shouldAllowBusyHarnessPanel(text string) bool {
	parts := strings.Fields(strings.TrimSpace(text))
	if len(parts) == 0 {
		return false
	}
	if parts[0] != "/harness" {
		return false
	}
	return len(parts) == 1 || (len(parts) == 2 && strings.EqualFold(parts[1], "panel"))
}

func (m *Model) ensureOutputEndsWithNewline() {
	if m.output == nil || m.output.Len() == 0 {
		return
	}
	if strings.HasSuffix(m.output.String(), "\n") {
		return
	}
	m.output.WriteString("\n")
}

func (m *Model) ensureOutputHasBlankLine() {
	if m.output == nil || m.output.Len() == 0 {
		return
	}
	switch {
	case strings.HasSuffix(m.output.String(), "\n\n"):
		return
	case strings.HasSuffix(m.output.String(), "\n"):
		m.output.WriteString("\n")
	default:
		m.output.WriteString("\n\n")
	}
}

func (m *Model) appendStreamChunk(chunk string) {
	if chunk == "" {
		return
	}
	m.closeToolActivityGroup()
	m.flushGroupedActivitiesToOutput()
	if !m.streamPrefixWritten {
		m.ensureOutputHasBlankLine()
		m.streamStartPos = m.output.Len()
		m.output.WriteString(bulletStyle.Render("● "))
		m.streamPrefixWritten = true
	}
	if m.streamBuffer != nil {
		m.streamBuffer.WriteString(chunk)
	}
	m.output.WriteString(chunk)
	m.trimOutput()
	m.syncConversationViewport()
	m.viewport.GotoBottom()
}

func (m *Model) appendHarnessProgressDetail(detail string) {
	detail = strings.TrimSpace(detail)
	if detail == "" {
		return
	}
	prefix := "→ "
	chunk := prefix + detail
	if m.streamBuffer != nil && m.streamBuffer.Len() > 0 && !strings.HasSuffix(m.streamBuffer.String(), "\n") {
		chunk = "\n" + chunk
	}
	m.appendStreamChunk(chunk + "\n")
}

func (m *Model) renderStreamBuffer() {
	if m.streamBuffer == nil || m.streamBuffer.Len() == 0 {
		return
	}
	rendered, err := m.mdRenderer.Render(m.streamBuffer.String())
	if err != nil {
		rendered = m.streamBuffer.String()
	}
	rendered = trimLeadingRenderedSpacing(rendered)
	m.output.Truncate(m.streamStartPos)
	m.output.WriteString(bulletStyle.Render("● "))
	m.output.WriteString(rendered)
	m.streamBuffer.Reset()
}

func trimHarnessRunOutputSection(rendered string) string {
	if idx := strings.Index(rendered, "\n\nOutput:\n"); idx >= 0 {
		return rendered[:idx]
	}
	return rendered
}

func (m *Model) handleCommand(text string) tea.Cmd {
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
			m.output.WriteString(m.styles.assistant.Render(m.helpText()))
			m.output.WriteString("\n\n")
			return nil
		case "/model":
			if len(parts) > 1 {
				if err := m.config.SetActiveSelection(m.config.Vendor, m.config.Endpoint, parts[1]); err == nil {
					if err := m.reloadActiveProvider(); err == nil {
						m.output.WriteString(m.t("command.model_switched", parts[1], m.config.Vendor))
					} else {
						m.output.WriteString(m.styles.error.Render(m.t("command.model_failed", err)))
					}
				} else {
					m.output.WriteString(m.styles.error.Render(m.t("command.model_failed", err)))
				}
			} else {
				resolved, err := m.config.ResolveActiveEndpoint()
				if err != nil {
					m.output.WriteString(m.styles.error.Render(m.t("command.model_failed", err)))
				} else {
					m.output.WriteString(m.t("command.model_current", resolved.Model, resolved.VendorName))
				}
				return m.openModelPanel()
			}
			return nil
		case "/provider":
			if len(parts) > 1 {
				newVendor := parts[1]
				endpoints := m.config.EndpointNames(newVendor)
				if len(endpoints) == 0 {
					m.output.WriteString(m.styles.error.Render(m.t("command.provider_unknown", newVendor, m.vendorNames())))
					return nil
				}
				if err := m.config.SetActiveSelection(newVendor, endpoints[0], ""); err == nil {
					if err := m.reloadActiveProvider(); err == nil {
						m.output.WriteString(m.t("command.provider_switched", newVendor, m.config.Model))
					} else {
						m.output.WriteString(m.styles.error.Render(m.t("command.provider_failed", err)))
					}
				} else {
					m.output.WriteString(m.styles.error.Render(m.t("command.provider_failed", err)))
				}
			} else {
				m.openProviderPanel()
			}
			return nil
		case "/allow":
			if len(parts) > 1 {
				if m.policy != nil {
					m.policy.SetOverride(parts[1], permission.Allow)
					m.output.WriteString(m.t("command.allow_set", parts[1]))
				}
			} else {
				m.output.WriteString(m.t("command.usage.allow"))
			}
			return nil
		case "/sessions":
			return m.listSessions()
		case "/resume":
			if len(parts) > 1 {
				return m.resumeSession(parts[1])
			}
			m.output.WriteString(m.t("command.usage.resume"))
			return nil
		case "/export":
			if len(parts) > 1 {
				return m.exportSession(parts[1])
			}
			m.output.WriteString(m.t("command.usage.export"))
			return nil
		case "/plugins":
			return m.handlePluginsCommand()
		case "/image":
			return m.handleImageCommand(parts)
		case "/fullscreen":
			return m.handleFullscreenCommand()
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
		case "/agents":
			return m.handleAgentsCommand(parts)
		case "/agent":
			return m.handleAgentDetailCommand(parts)
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
		case "/update":
			return m.handleUpdateCommand()
		default:
			// Check custom commands
			if cmdName := strings.TrimPrefix(cmd, "/"); cmdName != "" {
				if custom, ok := m.customCmds[cmdName]; ok {
					if !custom.UserInvocable {
						m.output.WriteString(m.styles.error.Render(fmt.Sprintf("Skill %s can only be invoked by the agent.", custom.SlashName())))
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
					m.output.WriteString(m.styles.user.Render(m.t("command.custom", cmdName)))
					m.output.WriteString(expanded)
					m.output.WriteString("\n\n")
					m.loading = true
					// Reset status bar state
					m.statusActivity = m.t("status.thinking")
					m.statusToolName = ""
					m.statusToolArg = ""
					m.statusToolCount = 0
					m.resetActivityGroups()
					return m.startAgent(expanded)
				}
			}
			m.output.WriteString(m.styles.error.Render(m.t("command.unknown", text)))
			m.output.WriteString(m.styles.prompt.Render(m.t("command.help_hint")))
			return nil
		}
	}

	// Regular message → start agent
	// Expand @mentions
	workDir, _ := os.Getwd()
	expandedMsg, expandErr := ExpandMentions(text, workDir)
	if expandErr != nil {
		m.output.WriteString(m.styles.error.Render(m.t("command.mention_error", expandErr)))
		m.output.WriteString("\n\n")
	}

	displayText := text
	if m.pendingImage != nil {
		displayText = strings.TrimSpace(m.pendingImage.placeholder + " " + text)
	}
	m.ensureOutputEndsWithNewline()
	m.output.WriteString(m.styles.user.Render("❯ "))
	m.output.WriteString(displayText)
	m.output.WriteString("\n")

	// Save original user message to session
	m.appendUserMessage(text)

	m.streamBuffer = &bytes.Buffer{}
	m.streamStartPos = m.output.Len()
	m.streamPrefixWritten = false
	m.loading = true
	// Reset status bar state
	m.statusActivity = m.t("status.thinking")
	m.statusToolName = ""
	m.statusToolArg = ""
	m.statusToolCount = 0
	m.resetActivityGroups()
	return m.startAgent(expandedMsg)
}

func (m *Model) handleInitCommand() tea.Cmd {
	workDir, _ := os.Getwd()
	targetPath, _, err := memory.ResolveProjectMemoryInitTarget(workDir)
	if err != nil {
		m.output.WriteString(m.styles.error.Render(m.t("init.resolve_failed", err)))
		return nil
	}
	existed := false
	if _, err := os.Stat(targetPath); err == nil {
		existed = true
	}
	content, err := memory.GenerateProjectMemory(filepath.Dir(targetPath))
	if err != nil {
		m.output.WriteString(m.styles.error.Render(m.t("init.generate_failed", err)))
		return nil
	}
	prompt := buildInitPrompt(targetPath, existed, content)

	m.output.WriteString(m.styles.user.Render("❯ /init"))
	m.output.WriteString("\n")
	m.appendUserMessage("/init")

	m.streamBuffer = &bytes.Buffer{}
	m.streamStartPos = m.output.Len()
	m.streamPrefixWritten = false
	m.loading = true
	m.statusActivity = m.t("init.collecting")
	m.statusToolName = ""
	m.statusToolArg = ""
	m.statusToolCount = 0
	m.resetActivityGroups()

	return m.startAgent(prompt)
}

func (m *Model) handleHarnessCommand(parts []string) tea.Cmd {
	if len(parts) < 2 {
		m.openHarnessPanel()
		return nil
	}

	workDir, _ := os.Getwd()
	switch parts[1] {
	case "panel":
		m.openHarnessPanel()
		return nil
	case "init":
		goal := strings.TrimSpace(strings.Join(parts[2:], " "))
		result, err := harness.Init(workDir, harness.InitOptions{Goal: goal})
		if err != nil {
			m.output.WriteString(m.styles.error.Render(err.Error()))
			m.output.WriteString("\n")
			return nil
		}
		m.output.WriteString(m.styles.assistant.Render(formatHarnessInitResult(result)))
		m.output.WriteString("\n")
		return nil
	case "check":
		project, cfg, err := loadHarnessForTUI(workDir)
		if err != nil {
			m.output.WriteString(m.styles.error.Render(err.Error()))
			m.output.WriteString("\n")
			return nil
		}
		report, err := harness.CheckProject(context.Background(), project, cfg, harness.CheckOptions{RunCommands: true})
		if err != nil {
			m.output.WriteString(m.styles.error.Render(err.Error()))
			m.output.WriteString("\n")
			return nil
		}
		rendered := harness.FormatCheckReport(report)
		if report.Passed {
			m.output.WriteString(m.styles.assistant.Render(rendered))
		} else {
			m.output.WriteString(m.styles.error.Render(rendered))
		}
		m.output.WriteString("\n")
		return nil
	case "doctor":
		project, cfg, err := loadHarnessForTUI(workDir)
		if err != nil {
			m.output.WriteString(m.styles.error.Render(err.Error()))
			m.output.WriteString("\n")
			return nil
		}
		report, err := harness.Doctor(project, cfg)
		if err != nil {
			m.output.WriteString(m.styles.error.Render(err.Error()))
			m.output.WriteString("\n")
			return nil
		}
		m.output.WriteString(m.styles.assistant.Render(harness.FormatDoctorReport(report)))
		m.output.WriteString("\n")
		return nil
	case "monitor":
		project, _, err := loadHarnessForTUI(workDir)
		if err != nil {
			m.output.WriteString(m.styles.error.Render(err.Error()))
			m.output.WriteString("\n")
			return nil
		}
		report, err := harness.BuildMonitorReport(project, harness.MonitorOptions{})
		if err != nil {
			m.output.WriteString(m.styles.error.Render(err.Error()))
			m.output.WriteString("\n")
			return nil
		}
		m.output.WriteString(m.styles.assistant.Render(harness.FormatMonitorReport(report)))
		m.output.WriteString("\n")
		return nil
	case "gc":
		project, cfg, err := loadHarnessForTUI(workDir)
		if err != nil {
			m.output.WriteString(m.styles.error.Render(err.Error()))
			m.output.WriteString("\n")
			return nil
		}
		report, err := harness.RunGC(project, cfg, time.Now().UTC())
		if err != nil {
			m.output.WriteString(m.styles.error.Render(err.Error()))
			m.output.WriteString("\n")
			return nil
		}
		m.output.WriteString(m.styles.assistant.Render(harness.FormatGCReport(report)))
		m.output.WriteString("\n")
		return nil
	case "contexts":
		project, cfg, err := loadHarnessForTUI(workDir)
		if err != nil {
			m.output.WriteString(m.styles.error.Render(err.Error()))
			m.output.WriteString("\n")
			return nil
		}
		report, err := harness.BuildContextReport(project, cfg)
		if err != nil {
			m.output.WriteString(m.styles.error.Render(err.Error()))
			m.output.WriteString("\n")
			return nil
		}
		m.output.WriteString(m.styles.assistant.Render(harness.FormatContextReport(report)))
		m.output.WriteString("\n")
		return nil
	case "inbox":
		project, cfg, err := loadHarnessForTUI(workDir)
		if err != nil {
			m.output.WriteString(m.styles.error.Render(err.Error()))
			m.output.WriteString("\n")
			return nil
		}
		if len(parts) == 2 {
			inbox, err := harness.BuildOwnerInbox(project, cfg)
			if err != nil {
				m.output.WriteString(m.styles.error.Render(err.Error()))
				m.output.WriteString("\n")
				return nil
			}
			m.output.WriteString(m.styles.assistant.Render(harness.FormatOwnerInbox(inbox)))
			m.output.WriteString("\n")
			return nil
		}
		if len(parts) < 4 {
			m.output.WriteString(m.styles.assistant.Render(m.t("command.harness_usage")))
			m.output.WriteString("\n")
			return nil
		}
		switch parts[2] {
		case "promote":
			tasks, err := harness.PromoteApprovedTasksForOwner(context.Background(), project, cfg, parts[3], strings.TrimSpace(strings.Join(parts[4:], " ")))
			if err != nil {
				m.output.WriteString(m.styles.error.Render(err.Error()))
				m.output.WriteString("\n")
				return nil
			}
			m.output.WriteString(m.styles.assistant.Render(fmt.Sprintf("Promoted %d harness task(s) for owner %s.", len(tasks), parts[3])))
			m.output.WriteString("\n")
			return nil
		case "retry":
			summary, err := harness.RetryFailedTasksForOwner(context.Background(), project, cfg, parts[3], harness.BinaryRunner{})
			if err != nil {
				m.output.WriteString(m.styles.error.Render(err.Error()))
				m.output.WriteString("\n")
				return nil
			}
			m.output.WriteString(m.styles.assistant.Render(harness.FormatQueueSummary(summary)))
			m.output.WriteString("\n")
			return nil
		}
		m.output.WriteString(m.styles.assistant.Render(m.t("command.harness_usage")))
		m.output.WriteString("\n")
		return nil
	case "review":
		project, _, err := loadHarnessForTUI(workDir)
		if err != nil {
			m.output.WriteString(m.styles.error.Render(err.Error()))
			m.output.WriteString("\n")
			return nil
		}
		if len(parts) == 2 {
			tasks, err := harness.ListReviewableTasks(project)
			if err != nil {
				m.output.WriteString(m.styles.error.Render(err.Error()))
				m.output.WriteString("\n")
				return nil
			}
			m.output.WriteString(m.styles.assistant.Render(harness.FormatReviewList(tasks)))
			m.output.WriteString("\n")
			return nil
		}
		if len(parts) < 4 {
			m.output.WriteString(m.styles.assistant.Render(m.t("command.harness_usage")))
			m.output.WriteString("\n")
			return nil
		}
		note := strings.TrimSpace(strings.Join(parts[4:], " "))
		switch parts[2] {
		case "approve":
			task, err := harness.ApproveTaskReview(project, parts[3], note)
			if err != nil {
				m.output.WriteString(m.styles.error.Render(err.Error()))
				m.output.WriteString("\n")
				return nil
			}
			m.output.WriteString(m.styles.assistant.Render(fmt.Sprintf("Approved harness task %s.", task.ID)))
			m.output.WriteString("\n")
			return nil
		case "reject":
			task, err := harness.RejectTaskReview(project, parts[3], note)
			if err != nil {
				m.output.WriteString(m.styles.error.Render(err.Error()))
				m.output.WriteString("\n")
				return nil
			}
			m.output.WriteString(m.styles.assistant.Render(fmt.Sprintf("Rejected harness task %s.", task.ID)))
			m.output.WriteString("\n")
			return nil
		}
		m.output.WriteString(m.styles.assistant.Render(m.t("command.harness_usage")))
		m.output.WriteString("\n")
		return nil
	case "promote":
		project, _, err := loadHarnessForTUI(workDir)
		if err != nil {
			m.output.WriteString(m.styles.error.Render(err.Error()))
			m.output.WriteString("\n")
			return nil
		}
		if len(parts) == 2 {
			tasks, err := harness.ListPromotableTasks(project)
			if err != nil {
				m.output.WriteString(m.styles.error.Render(err.Error()))
				m.output.WriteString("\n")
				return nil
			}
			m.output.WriteString(m.styles.assistant.Render(harness.FormatPromotionList(tasks)))
			m.output.WriteString("\n")
			return nil
		}
		if len(parts) < 4 || parts[2] != "apply" {
			m.output.WriteString(m.styles.assistant.Render(m.t("command.harness_usage")))
			m.output.WriteString("\n")
			return nil
		}
		note := strings.TrimSpace(strings.Join(parts[4:], " "))
		if parts[3] == "all" {
			tasks, err := harness.PromoteApprovedTasks(context.Background(), project, note)
			if err != nil {
				m.output.WriteString(m.styles.error.Render(err.Error()))
				m.output.WriteString("\n")
				return nil
			}
			m.output.WriteString(m.styles.assistant.Render(fmt.Sprintf("Promoted %d harness task(s).", len(tasks))))
			m.output.WriteString("\n")
			return nil
		}
		task, err := harness.PromoteTask(context.Background(), project, parts[3], note)
		if err != nil {
			m.output.WriteString(m.styles.error.Render(err.Error()))
			m.output.WriteString("\n")
			return nil
		}
		m.output.WriteString(m.styles.assistant.Render(fmt.Sprintf("Promoted harness task %s.", task.ID)))
		m.output.WriteString("\n")
		return nil
	case "release":
		project, cfg, err := loadHarnessForTUI(workDir)
		if err != nil {
			m.output.WriteString(m.styles.error.Render(err.Error()))
			m.output.WriteString("\n")
			return nil
		}
		if len(parts) == 2 {
			plan, err := harness.BuildReleasePlan(project, cfg)
			if err != nil {
				m.output.WriteString(m.styles.error.Render(err.Error()))
				m.output.WriteString("\n")
				return nil
			}
			m.output.WriteString(m.styles.assistant.Render(harness.FormatReleasePlan(plan)))
			m.output.WriteString("\n")
			return nil
		}
		switch parts[2] {
		case "waves":
			if len(parts) < 4 {
				m.output.WriteString(m.styles.assistant.Render(m.t("command.harness_usage")))
				m.output.WriteString("\n")
				return nil
			}
			waves, err := harness.BuildReleaseWavePlan(project, cfg, harness.ReleasePlanOptions{}, parts[3])
			if err != nil {
				m.output.WriteString(m.styles.error.Render(err.Error()))
				m.output.WriteString("\n")
				return nil
			}
			m.output.WriteString(m.styles.assistant.Render(harness.FormatReleaseWavePlan(waves)))
			m.output.WriteString("\n")
			return nil
		case "apply":
			if len(parts) >= 5 && parts[3] == "waves" {
				waves, err := harness.BuildReleaseWavePlan(project, cfg, harness.ReleasePlanOptions{}, parts[4])
				if err != nil {
					m.output.WriteString(m.styles.error.Render(err.Error()))
					m.output.WriteString("\n")
					return nil
				}
				waves, err = harness.ApplyReleaseWavePlan(project, waves, "", "")
				if err != nil {
					m.output.WriteString(m.styles.error.Render(err.Error()))
					m.output.WriteString("\n")
					return nil
				}
				m.output.WriteString(m.styles.assistant.Render(harness.FormatReleaseWavePlan(waves)))
				m.output.WriteString("\n")
				return nil
			}
			plan, err := harness.BuildReleasePlan(project, cfg)
			if err != nil {
				m.output.WriteString(m.styles.error.Render(err.Error()))
				m.output.WriteString("\n")
				return nil
			}
			plan, err = harness.ApplyReleasePlan(project, plan, "")
			if err != nil {
				m.output.WriteString(m.styles.error.Render(err.Error()))
				m.output.WriteString("\n")
				return nil
			}
			m.output.WriteString(m.styles.assistant.Render(harness.FormatReleasePlan(plan)))
			m.output.WriteString("\n")
			return nil
		case "rollouts":
			rollouts, err := harness.ListReleaseWaveRollouts(project)
			if err != nil {
				m.output.WriteString(m.styles.error.Render(err.Error()))
				m.output.WriteString("\n")
				return nil
			}
			m.output.WriteString(m.styles.assistant.Render(harness.FormatReleaseWaveRollouts(rollouts)))
			m.output.WriteString("\n")
			return nil
		case "advance":
			if len(parts) < 4 {
				m.output.WriteString(m.styles.assistant.Render(m.t("command.harness_usage")))
				m.output.WriteString("\n")
				return nil
			}
			rollout, err := harness.AdvanceReleaseWaveRollout(project, parts[3])
			if err != nil {
				m.output.WriteString(m.styles.error.Render(err.Error()))
				m.output.WriteString("\n")
				return nil
			}
			m.output.WriteString(m.styles.assistant.Render(harness.FormatReleaseWavePlan(rollout)))
			m.output.WriteString("\n")
			return nil
		case "pause":
			if len(parts) < 4 {
				m.output.WriteString(m.styles.assistant.Render(m.t("command.harness_usage")))
				m.output.WriteString("\n")
				return nil
			}
			rollout, err := harness.PauseReleaseWaveRollout(project, parts[3], strings.TrimSpace(strings.Join(parts[4:], " ")))
			if err != nil {
				m.output.WriteString(m.styles.error.Render(err.Error()))
				m.output.WriteString("\n")
				return nil
			}
			m.output.WriteString(m.styles.assistant.Render(harness.FormatReleaseWavePlan(rollout)))
			m.output.WriteString("\n")
			return nil
		case "resume":
			if len(parts) < 4 {
				m.output.WriteString(m.styles.assistant.Render(m.t("command.harness_usage")))
				m.output.WriteString("\n")
				return nil
			}
			rollout, err := harness.ResumeReleaseWaveRollout(project, parts[3], strings.TrimSpace(strings.Join(parts[4:], " ")))
			if err != nil {
				m.output.WriteString(m.styles.error.Render(err.Error()))
				m.output.WriteString("\n")
				return nil
			}
			m.output.WriteString(m.styles.assistant.Render(harness.FormatReleaseWavePlan(rollout)))
			m.output.WriteString("\n")
			return nil
		case "abort":
			if len(parts) < 4 {
				m.output.WriteString(m.styles.assistant.Render(m.t("command.harness_usage")))
				m.output.WriteString("\n")
				return nil
			}
			rollout, err := harness.AbortReleaseWaveRollout(project, parts[3], strings.TrimSpace(strings.Join(parts[4:], " ")))
			if err != nil {
				m.output.WriteString(m.styles.error.Render(err.Error()))
				m.output.WriteString("\n")
				return nil
			}
			m.output.WriteString(m.styles.assistant.Render(harness.FormatReleaseWavePlan(rollout)))
			m.output.WriteString("\n")
			return nil
		case "approve", "reject":
			if len(parts) < 4 {
				m.output.WriteString(m.styles.assistant.Render(m.t("command.harness_usage")))
				m.output.WriteString("\n")
				return nil
			}
			waveOrder := 0
			noteStart := 4
			if len(parts) > 4 {
				if parsed, err := strconv.Atoi(parts[4]); err == nil {
					waveOrder = parsed
					noteStart = 5
				}
			}
			note := strings.TrimSpace(strings.Join(parts[noteStart:], " "))
			var (
				rollout *harness.ReleaseWavePlan
				err     error
			)
			if parts[2] == "approve" {
				rollout, err = harness.ApproveReleaseWaveGate(project, parts[3], waveOrder, note)
			} else {
				rollout, err = harness.RejectReleaseWaveGate(project, parts[3], waveOrder, note)
			}
			if err != nil {
				m.output.WriteString(m.styles.error.Render(err.Error()))
				m.output.WriteString("\n")
				return nil
			}
			m.output.WriteString(m.styles.assistant.Render(harness.FormatReleaseWavePlan(rollout)))
			m.output.WriteString("\n")
			return nil
		}
		m.output.WriteString(m.styles.assistant.Render(m.t("command.harness_usage")))
		m.output.WriteString("\n")
		return nil
	case "queue":
		if len(parts) < 3 {
			m.output.WriteString(m.styles.error.Render(m.t("command.harness_queue_usage")))
			m.output.WriteString("\n")
			return nil
		}
		project, _, err := loadHarnessForTUI(workDir)
		if err != nil {
			m.output.WriteString(m.styles.error.Render(err.Error()))
			m.output.WriteString("\n")
			return nil
		}
		task, err := harness.EnqueueTask(project, strings.TrimSpace(strings.Join(parts[2:], " ")), "tui")
		if err != nil {
			m.output.WriteString(m.styles.error.Render(err.Error()))
			m.output.WriteString("\n")
			return nil
		}
		m.output.WriteString(m.styles.assistant.Render(fmt.Sprintf("Queued harness task %s.\n- goal: %s", task.ID, task.Goal)))
		m.output.WriteString("\n")
		return nil
	case "tasks":
		project, _, err := loadHarnessForTUI(workDir)
		if err != nil {
			m.output.WriteString(m.styles.error.Render(err.Error()))
			m.output.WriteString("\n")
			return nil
		}
		tasks, err := harness.ListTasks(project)
		if err != nil {
			m.output.WriteString(m.styles.error.Render(err.Error()))
			m.output.WriteString("\n")
			return nil
		}
		if len(tasks) == 0 {
			m.output.WriteString(m.styles.assistant.Render("No harness tasks recorded."))
			m.output.WriteString("\n")
			return nil
		}
		m.output.WriteString(m.styles.assistant.Render(harness.FormatTaskList(tasks)))
		m.output.WriteString("\n")
		return nil
	case "run-queued":
		project, cfg, err := loadHarnessForTUI(workDir)
		if err != nil {
			m.output.WriteString(m.styles.error.Render(err.Error()))
			m.output.WriteString("\n")
			return nil
		}
		opts := harness.QueueRunOptions{}
		for _, part := range parts[2:] {
			switch strings.ToLower(part) {
			case "all":
				opts.All = true
			case "retry":
				opts.RetryFailed = true
			case "resume":
				opts.ResumeInterrupted = true
			}
		}
		queueSummary, err := harness.RunQueuedTasks(context.Background(), project, cfg, nil, opts)
		if err != nil {
			m.output.WriteString(m.styles.error.Render(err.Error()))
			m.output.WriteString("\n")
			return nil
		}
		m.output.WriteString(m.styles.assistant.Render(harness.FormatQueueSummary(queueSummary)))
		m.output.WriteString("\n")
		return nil
	case "run":
		if len(parts) < 3 {
			m.output.WriteString(m.styles.error.Render(m.t("command.harness_run_usage")))
			m.output.WriteString("\n")
			return nil
		}
		project, cfg, err := loadHarnessForTUI(workDir)
		if err != nil {
			m.output.WriteString(m.styles.error.Render(err.Error()))
			m.output.WriteString("\n")
			return nil
		}
		return m.runTrackedHarnessGoal(strings.Join(parts, " "), strings.TrimSpace(strings.Join(parts[2:], " ")), project, cfg)
	case "rerun":
		if len(parts) != 3 {
			m.output.WriteString(m.styles.error.Render(m.t("command.harness_rerun_usage")))
			m.output.WriteString("\n")
			return nil
		}
		project, cfg, err := loadHarnessForTUI(workDir)
		if err != nil {
			m.output.WriteString(m.styles.error.Render(err.Error()))
			m.output.WriteString("\n")
			return nil
		}
		task, err := harness.LoadTask(project, parts[2])
		if err != nil {
			m.output.WriteString(m.styles.error.Render(err.Error()))
			m.output.WriteString("\n")
			return nil
		}
		return m.runTrackedHarnessRerun(strings.Join(parts, " "), project, cfg, task)
	default:
		m.output.WriteString(m.styles.assistant.Render(m.t("command.harness_usage")))
		m.output.WriteString("\n")
		return nil
	}
}

func (m *Model) runTrackedHarnessGoal(commandText, goal string, project harness.Project, cfg *harness.Config) tea.Cmd {
	goal = strings.TrimSpace(goal)
	if goal == "" {
		m.output.WriteString(m.styles.error.Render(m.t("command.harness_run_usage")))
		m.output.WriteString("\n")
		return nil
	}
	m.output.WriteString(m.styles.user.Render("❯ "))
	m.output.WriteString(strings.TrimSpace(commandText))
	m.output.WriteString("\n")
	m.appendUserMessage(strings.TrimSpace(commandText))
	m.ensureOutputHasBlankLine()
	m.output.WriteString(m.styles.assistant.Render("Starting tracked harness run...\nUse /harness monitor or the Tasks/Monitor views for live state."))
	m.output.WriteString("\n")
	m.syncConversationViewport()
	m.viewport.GotoBottom()

	ctx, cancel := context.WithCancel(context.Background())
	m.cancelFunc = cancel
	m.loading = true
	m.runCanceled = false
	m.runFailed = false
	m.statusActivity = "Starting harness run..."
	m.statusToolName = ""
	m.statusToolArg = ""
	m.statusToolCount = 0
	m.resetActivityGroups()
	m.harnessRunProject = &project
	m.harnessRunGoal = goal
	m.harnessRunTaskID = ""
	m.harnessRunLogPath = ""
	m.harnessRunLogOffset = 0
	m.harnessRunLastDetail = ""
	m.streamBuffer = &bytes.Buffer{}
	m.streamStartPos = m.output.Len()
	m.streamPrefixWritten = false
	startSpinner := m.spinner.Start("Running harness")
	if m.program == nil {
		return func() tea.Msg {
			summary, err := executeHarnessRun(ctx, project, cfg, goal)
			return harnessRunResultMsg{Summary: summary, Err: err}
		}
	}
	go func() {
		summary, err := executeHarnessRun(ctx, project, cfg, goal)
		m.program.Send(harnessRunResultMsg{Summary: summary, Err: err})
	}()
	return tea.Batch(startSpinner, m.pollHarnessRunProgress())
}

func (m *Model) runTrackedHarnessRerun(commandText string, project harness.Project, cfg *harness.Config, task *harness.Task) tea.Cmd {
	if task == nil {
		m.output.WriteString(m.styles.error.Render(m.t("command.harness_rerun_usage")))
		m.output.WriteString("\n")
		return nil
	}
	if task.Status != harness.TaskFailed {
		m.output.WriteString(m.styles.error.Render(fmt.Sprintf("Harness task %s is %s; only failed tasks can be rerun.", task.ID, task.Status)))
		m.output.WriteString("\n")
		return nil
	}
	m.output.WriteString(m.styles.user.Render("❯ "))
	m.output.WriteString(strings.TrimSpace(commandText))
	m.output.WriteString("\n")
	m.appendUserMessage(strings.TrimSpace(commandText))
	m.ensureOutputHasBlankLine()
	m.output.WriteString(m.styles.assistant.Render("Starting tracked harness rerun...\nUse /harness monitor or the Tasks/Monitor views for live state."))
	m.output.WriteString("\n")
	m.syncConversationViewport()
	m.viewport.GotoBottom()

	ctx, cancel := context.WithCancel(context.Background())
	m.cancelFunc = cancel
	m.loading = true
	m.runCanceled = false
	m.runFailed = false
	m.statusActivity = "Starting harness rerun..."
	m.statusToolName = ""
	m.statusToolArg = ""
	m.statusToolCount = 0
	m.resetActivityGroups()
	m.harnessRunProject = &project
	m.harnessRunGoal = task.Goal
	m.harnessRunTaskID = task.ID
	m.harnessRunLogPath = strings.TrimSpace(task.LogPath)
	m.harnessRunLogOffset = 0
	m.harnessRunLastDetail = ""
	m.streamBuffer = &bytes.Buffer{}
	m.streamStartPos = m.output.Len()
	m.streamPrefixWritten = false
	startSpinner := m.spinner.Start("Running harness")
	if m.program == nil {
		return func() tea.Msg {
			summary, err := executeHarnessRerun(ctx, project, cfg, task.ID)
			return harnessRunResultMsg{Summary: summary, Err: err}
		}
	}
	go func() {
		summary, err := executeHarnessRerun(ctx, project, cfg, task.ID)
		m.program.Send(harnessRunResultMsg{Summary: summary, Err: err})
	}()
	return tea.Batch(startSpinner, m.pollHarnessRunProgress())
}

func (m *Model) pollHarnessRunProgress() tea.Cmd {
	project := m.harnessRunProject
	goal := m.harnessRunGoal
	taskID := m.harnessRunTaskID
	logPath := m.harnessRunLogPath
	logOffset := m.harnessRunLogOffset
	if !m.loading || project == nil {
		return nil
	}
	return tea.Tick(time.Second, func(time.Time) tea.Msg {
		return readHarnessRunProgress(*project, goal, taskID, logPath, logOffset)
	})
}

func readHarnessRunProgress(project harness.Project, goal, taskID, logPath string, logOffset int64) harnessRunProgressMsg {
	msg := harnessRunProgressMsg{
		TaskID:    strings.TrimSpace(taskID),
		LogPath:   strings.TrimSpace(logPath),
		LogOffset: logOffset,
	}
	goal = strings.TrimSpace(goal)
	if msg.TaskID != "" {
		task, err := harness.LoadTask(project, msg.TaskID)
		if err == nil && task != nil {
			msg = populateHarnessRunProgress(msg, task)
			return msg
		}
	}
	tasks, err := harness.ListTasks(project)
	if err != nil {
		return msg
	}
	for _, task := range tasks {
		if task == nil || strings.TrimSpace(task.Goal) != goal {
			continue
		}
		msg = populateHarnessRunProgress(msg, task)
		return msg
	}
	msg.Activity = "Starting harness run..."
	return msg
}

func populateHarnessRunProgress(msg harnessRunProgressMsg, task *harness.Task) harnessRunProgressMsg {
	if task == nil {
		return msg
	}
	msg.TaskID = task.ID
	msg.Activity = formatHarnessRunActivity(task)
	msg.Detail = formatHarnessRunDetail(task)
	if path := strings.TrimSpace(task.LogPath); path != "" {
		msg.LogPath = path
	}
	if msg.LogPath != "" {
		msg.LogChunk, msg.LogOffset = readHarnessRunLogChunk(msg.LogPath, msg.LogOffset)
	}
	return msg
}

func readHarnessRunLogChunk(path string, offset int64) (string, int64) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", offset
	}
	file, err := os.Open(path)
	if err != nil {
		return "", offset
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return "", offset
	}
	if info.Size() < offset {
		offset = 0
	}
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		return "", offset
	}
	data, err := io.ReadAll(file)
	if err != nil {
		return "", offset
	}
	if len(data) == 0 {
		return "", offset
	}
	return string(data), offset + int64(len(data))
}

func formatHarnessRunActivity(task *harness.Task) string {
	if task == nil {
		return "Starting harness run..."
	}
	parts := []string{"Harness " + string(task.Status)}
	if task.ID != "" {
		parts = append(parts, task.ID)
	}
	if status := strings.TrimSpace(task.WorkerStatus); status != "" {
		parts = append(parts, status)
	}
	if phase := strings.TrimSpace(task.WorkerPhase); phase != "" {
		parts = append(parts, phase)
	}
	if progress := strings.TrimSpace(task.WorkerProgress); progress != "" {
		parts = append(parts, progress)
	}
	return strings.Join(parts, " • ")
}

func formatHarnessRunDetail(task *harness.Task) string {
	if task == nil {
		return ""
	}
	if progress := strings.TrimSpace(task.WorkerProgress); progress != "" {
		return humanizeHarnessProgress(progress)
	}
	if phase := strings.TrimSpace(task.WorkerPhase); phase != "" {
		return "phase: " + phase
	}
	if status := strings.TrimSpace(task.WorkerStatus); status != "" {
		return "worker: " + status
	}
	return ""
}

func humanizeHarnessProgress(progress string) string {
	progress = strings.TrimSpace(progress)
	if progress == "" {
		return ""
	}
	switch {
	case strings.HasPrefix(progress, "tool: "):
		return "running " + strings.TrimSpace(strings.TrimPrefix(progress, "tool: "))
	case strings.HasPrefix(progress, "tool result: error"):
		return strings.TrimSpace(strings.TrimPrefix(progress, "tool result: "))
	case strings.HasPrefix(progress, "tool result: "):
		return "result " + strings.TrimSpace(strings.TrimPrefix(progress, "tool result: "))
	default:
		return progress
	}
}

func loadHarnessForTUI(workDir string) (harness.Project, *harness.Config, error) {
	project, err := harness.Discover(workDir)
	if err != nil {
		return harness.Project{}, nil, err
	}
	cfg, err := harness.LoadConfig(project.ConfigPath)
	if err != nil {
		return harness.Project{}, nil, err
	}
	return project, cfg, nil
}

func formatHarnessInitResult(result *harness.InitResult) string {
	if result == nil {
		return "Harness init did not produce a result."
	}
	var b strings.Builder
	b.WriteString("Harness initialized.\n")
	if result.GitInitialized {
		b.WriteString("- git: initialized repository\n")
	}
	for _, path := range result.CreatedPaths {
		b.WriteString("- created: ")
		b.WriteString(path)
		b.WriteString("\n")
	}
	for _, path := range result.Overwritten {
		b.WriteString("- overwritten: ")
		b.WriteString(path)
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func buildInitPrompt(targetPath string, existed bool, bootstrap string) string {
	action := "create"
	if existed {
		action = "update"
	}
	return fmt.Sprintf(`Analyze the current repository and %s the project memory file at %s.

Before writing anything, inspect the repository with tools so the user can see an explicit knowledge-collection flow. Do not skip straight to writing the file. Read the relevant project files, confirm the architecture, tooling, validation commands, major directories, and durable conventions, then write the final GGCODE.md.

Requirements:
- The output file must be %s.
- Collect repository knowledge first with tool calls; do not answer with only prose.
- The file should contain current project facts and durable guidance, not an empty template.
- Keep the document concise, practical, and easy for future agents to follow.
- Overwrite the existing file if it already exists.

Bootstrap snapshot collected locally to help you start, but you must verify and improve it with repo inspection before writing:

%s`, action, targetPath, targetPath, bootstrap)
}

func (m *Model) resetConversationView() {
	m.output.Reset()
	m.streamBuffer = nil
	m.streamStartPos = 0
	m.streamPrefixWritten = false
	m.loading = false
	m.statusActivity = ""
	m.statusToolName = ""
	m.statusToolArg = ""
	m.statusToolCount = 0
	m.resetActivityGroups()
	m.autoCompleteActive = false
	m.autoCompleteItems = nil
	m.autoCompleteIndex = 0
	m.exitConfirmPending = false
	m.pendingSubmissions = nil
	m.runCanceled = false
	m.runFailed = false
	m.spinner.Stop()
	m.viewport.SetContent("")
	m.viewport.GotoBottom()
}

func (m *Model) listSessions() tea.Cmd {
	return func() tea.Msg {
		if m.sessionStore == nil {
			return streamMsg(m.t("session.store_missing"))
		}
		sessions, err := m.sessionStore.List()
		if err != nil {
			return streamMsg(m.t("session.list_failed", err))
		}
		if len(sessions) == 0 {
			return streamMsg(m.t("session.none"))
		}
		var b strings.Builder
		b.WriteString(m.t("session.list.title"))
		for i, s := range sessions {
			title := s.Title
			if title == "" {
				title = m.t("session.untitled")
			}
			updated := s.UpdatedAt.Format(time.RFC3339)
			b.WriteString(m.t("session.list.item", i+1, s.ID, title, updated))
		}
		b.WriteString(m.t("session.list.hint"))
		return streamMsg(b.String())
	}
}

func (m *Model) resumeSession(id string) tea.Cmd {
	return func() tea.Msg {
		if m.sessionStore == nil {
			return streamMsg(m.t("session.store_missing"))
		}
		ses, err := m.sessionStore.Load(id)
		if err != nil {
			return streamMsg(m.t("session.resume_failed", id, err))
		}
		// Restore messages into agent
		for _, msg := range ses.Messages {
			m.agent.AddMessage(msg)
		}
		m.session = ses
		m.rebuildConversationFromMessages(ses.Messages)
		title := ses.Title
		if title == "" {
			title = m.t("session.untitled")
		}
		return streamMsg(m.t("session.resume", ses.ID, title, len(ses.Messages)))
	}
}

func (m *Model) exportSession(id string) tea.Cmd {
	return func() tea.Msg {
		if m.sessionStore == nil {
			return streamMsg(m.t("session.store_missing"))
		}
		md, err := m.sessionStore.ExportMarkdown(id)
		if err != nil {
			return streamMsg(m.t("session.export_failed", err))
		}
		filename := fmt.Sprintf("session-%s.md", id)
		if err := os.WriteFile(filename, []byte(md), 0644); err != nil {
			return streamMsg(m.t("session.write_failed", err))
		}
		return streamMsg(m.t("session.exported", id, filename))
	}
}

func (m *Model) handleApproval(d permission.Decision) tea.Cmd {
	pa := m.pendingApproval
	m.pendingApproval = nil
	if pa == nil || pa.Response == nil {
		return nil
	}
	go func() {
		pa.Response <- d
	}()
	return nil
}

func (m *Model) handleApprovalAllowAlways() tea.Cmd {
	pa := m.pendingApproval
	m.pendingApproval = nil
	if pa != nil && m.policy != nil {
		m.policy.SetOverride(pa.ToolName, permission.Allow)
		present := describeTool(m.currentLanguage(), pa.ToolName, pa.Input)
		toolLine := formatToolInline(present.DisplayName, present.Detail)
		if m.currentLanguage() == LangZhCN {
			m.output.WriteString(fmt.Sprintf("\u2713 已总是允许：%s\n\n", toolLine))
		} else {
			m.output.WriteString(fmt.Sprintf("\u2713 Always allow: %s\n\n", toolLine))
		}
	}
	if pa != nil && pa.Response != nil {
		go func() {
			pa.Response <- permission.Allow
		}()
	}
	return nil
}

func (m *Model) handleDiffConfirm(approved bool) tea.Cmd {
	pd := m.pendingDiffConfirm
	m.pendingDiffConfirm = nil
	if pd == nil || pd.Response == nil {
		return nil
	}
	go func() {
		pd.Response <- approved
	}()
	if !approved {
		m.output.WriteString(m.styles.error.Render(m.t("approval.rejected")))
	}
	return nil
}

func (m Model) handleHistoryUp() (tea.Model, tea.Cmd) {
	if m.historyIdx > 0 {
		m.historyIdx--
		m.input.SetValue(m.history[m.historyIdx])
	}
	return m, nil
}

func (m Model) handleHistoryDown() (tea.Model, tea.Cmd) {
	if m.historyIdx < len(m.history)-1 {
		m.historyIdx++
		m.input.SetValue(m.history[m.historyIdx])
	} else {
		m.historyIdx = len(m.history)
		m.input.SetValue("")
	}
	return m, nil
}

func (m Model) handleModeSwitch() (tea.Model, tea.Cmd) {
	m.mode = m.mode.Next()
	// Update policy mode
	if cp, ok := m.policy.(*permission.ConfigPolicy); ok {
		cp.SetMode(m.mode)
	}
	return m, nil
}

func (m *Model) handleModeCommand(parts []string) tea.Cmd {
	if len(parts) > 1 {
		newMode := permission.ParsePermissionMode(parts[1])
		m.mode = newMode
		if cp, ok := m.policy.(*permission.ConfigPolicy); ok {
			cp.SetMode(newMode)
		}
	} else {
		m.output.WriteString(m.t("mode.current", m.mode))
	}
	return nil
}

func (m *Model) handleLangCommand(parts []string) tea.Cmd {
	if len(parts) == 1 {
		m.openLanguageSelector(false)
		return nil
	}
	raw := strings.TrimSpace(parts[1])
	lang := normalizeLanguage(raw)
	if lang == LangEnglish && !strings.EqualFold(raw, "en") && !strings.EqualFold(raw, "english") {
		m.output.WriteString(m.styles.error.Render(m.t("lang.invalid", raw, supportedLanguageUsage(m.currentLanguage()))))
		return nil
	}
	m.applyLanguageChange(lang)
	return nil
}

func (m *Model) applyLanguageSelection(lang Language) tea.Cmd {
	m.langOptions = nil
	m.langCursor = 0
	m.languagePromptRequired = false
	m.applyLanguageChange(lang)
	return nil
}

func (m *Model) openLanguageSelector(required bool) {
	m.langOptions = languageOptionsFor(m.currentLanguage())
	m.langCursor = 0
	m.languagePromptRequired = required
	for i, opt := range m.langOptions {
		if opt.lang == m.currentLanguage() {
			m.langCursor = i
			break
		}
	}
}

func (m *Model) applyLanguageChange(lang Language) {
	m.setLanguage(string(lang))
	if m.config != nil {
		if err := m.config.SaveLanguagePreference(string(m.currentLanguage())); err != nil {
			m.output.WriteString(m.styles.error.Render(err.Error() + "\n"))
			return
		}
	}
	m.output.WriteString(m.t("lang.switch", m.languageLabel()))
}

func (m *Model) handleUndoCommand() tea.Cmd {
	return func() tea.Msg {
		cpMgr := m.agent.CheckpointManager()
		if cpMgr == nil {
			return streamMsg(m.t("checkpoint.disabled"))
		}
		cp, err := cpMgr.Undo()
		if err != nil {
			return streamMsg(m.t("checkpoint.undo_failed", err))
		}
		// Show diff (new -> old)
		diffText := diff.UnifiedDiff(cp.NewContent, cp.OldContent, 3)
		var b strings.Builder
		b.WriteString(m.t("checkpoint.undid", cp.ToolCall, displayToolFileTarget(cp.FilePath), cp.ID))
		b.WriteString(FormatDiff(diffText))
		b.WriteString("\n")
		return streamMsg(b.String())
	}
}

func (m *Model) handleCheckpointsCommand() tea.Cmd {
	return func() tea.Msg {
		cpMgr := m.agent.CheckpointManager()
		if cpMgr == nil {
			return streamMsg(m.t("checkpoint.disabled"))
		}
		ps := cpMgr.List()
		if len(ps) == 0 {
			return streamMsg(m.t("checkpoint.none"))
		}
		var b strings.Builder
		b.WriteString(m.t("checkpoint.list.title", len(ps)))
		for i, cp := range ps {
			b.WriteString(m.t("checkpoint.list.item", i+1, cp.ID, displayToolFileTarget(cp.FilePath), cp.ToolCall, cp.Timestamp.Format("15:04:05")))
		}
		b.WriteString(m.t("checkpoint.list.hint"))
		return streamMsg(b.String())
	}
}

func workingDirFromModel(m *Model) string {
	dir, err := os.Getwd()
	if err != nil {
		return "."
	}
	return dir
}

func (m *Model) handleMemoryCommand(parts []string) tea.Cmd {
	sub := ""
	if len(parts) > 1 {
		sub = strings.ToLower(parts[1])
	}
	switch sub {
	case "list":
		if m.autoMem == nil {
			m.output.WriteString(m.styles.prompt.Render(m.t("memory.auto_unavailable")))
			return nil
		}
		keys, err := m.autoMem.List()
		if err != nil {
			m.output.WriteString(m.styles.error.Render(m.t("memory.list_failed", err)))
			return nil
		}
		if len(keys) == 0 {
			m.output.WriteString(m.styles.prompt.Render(m.t("memory.none")))
			return nil
		}
		m.output.WriteString(m.styles.title.Render(m.t("memory.auto_title")))
		for _, k := range keys {
			m.output.WriteString(fmt.Sprintf("  - %s\n", k))
		}
		m.output.WriteString("\n")
	case "clear":
		if m.autoMem == nil {
			m.output.WriteString(m.styles.prompt.Render(m.t("memory.auto_unavailable")))
			return nil
		}
		if err := m.autoMem.Clear(); err != nil {
			m.output.WriteString(m.styles.error.Render(m.t("memory.clear_failed", err)))
			return nil
		}
		m.output.WriteString(m.styles.assistant.Render(m.t("memory.cleared")))
	default:
		m.output.WriteString(m.styles.title.Render(m.t("memory.title")))
		if len(m.projMemFiles) > 0 {
			m.output.WriteString(m.styles.assistant.Render(m.t("memory.project")))
			for _, f := range m.projMemFiles {
				m.output.WriteString(fmt.Sprintf("  %s\n", f))
			}
			m.output.WriteString("\n")
		} else {
			m.output.WriteString(m.styles.prompt.Render(m.t("memory.project_none")))
		}
		if len(m.autoMemFiles) > 0 {
			m.output.WriteString(m.styles.assistant.Render(m.t("memory.auto")))
			for _, f := range m.autoMemFiles {
				m.output.WriteString(fmt.Sprintf("  %s\n", f))
			}
			m.output.WriteString("\n")
		} else {
			m.output.WriteString(m.styles.prompt.Render(m.t("memory.auto_none")))
		}
		m.output.WriteString(m.styles.prompt.Render(m.t("memory.usage")))
	}
	return nil
}

func (m *Model) handleCompactCommand() tea.Cmd {
	return func() tea.Msg {
		cm := m.agent.ContextManager()
		if cm == nil {
			return streamMsg(m.t("compact.unavailable"))
		}
		if err := cm.Summarize(context.Background(), m.agent.Provider()); err != nil {
			return streamMsg(m.t("compact.failed", err))
		}
		return streamMsg(m.t("compact.done"))
	}
}

func (m *Model) handleTodoCommand(parts []string) tea.Cmd {
	if len(parts) > 1 && strings.ToLower(parts[1]) == "clear" {
		// Clear todos
		todopath := func() string { d, _ := os.UserHomeDir(); return filepath.Join(d, ".ggcode", "todos.json") }()
		if err := os.WriteFile(todopath, []byte("[]\n"), 0644); err != nil {
			return func() tea.Msg {
				return streamMsg(m.t("todo.clear_failed", err))
			}
		}
		m.output.WriteString(m.styles.assistant.Render(m.t("todo.cleared")))
		return nil
	}
	return func() tea.Msg {
		todopath := func() string { d, _ := os.UserHomeDir(); return filepath.Join(d, ".ggcode", "todos.json") }()
		data, err := os.ReadFile(todopath)
		if err != nil {
			if os.IsNotExist(err) {
				return streamMsg(m.t("todo.none"))
			}
			return streamMsg(m.t("todo.read_failed", err))
		}
		// Pretty print JSON
		var raw interface{}
		if err := json.Unmarshal(data, &raw); err != nil {
			return streamMsg(m.t("todo.parse_failed", err))
		}
		pretty, _ := json.MarshalIndent(raw, "", "  ")
		return streamMsg(m.t("todo.title", string(pretty)))
	}
}

func (m *Model) handleBugCommand() tea.Cmd {
	return func() tea.Msg {
		var b strings.Builder
		b.WriteString(m.t("bug.title"))

		// Version info
		b.WriteString(m.t("bug.version", version.Display()))
		b.WriteString(m.t("bug.os", runtime.GOOS, runtime.GOARCH))
		b.WriteString(m.t("bug.go", runtime.Version()))

		// Config info
		if m.config != nil {
			b.WriteString(m.t("bug.provider", m.config.Vendor))
			b.WriteString(m.t("bug.model", m.config.Model))
		}

		// Session info
		if m.session != nil {
			b.WriteString(m.t("bug.session", m.session.ID, len(m.session.Messages)))
		}

		// MCP info
		if len(m.mcpServers) > 0 {
			b.WriteString(m.t("bug.mcp", len(m.mcpServers)))
		}

		// Recent errors from output
		output := m.output.String()
		if idx := strings.LastIndex(output, "Error:"); idx >= 0 {
			end := idx + 500
			if end > len(output) {
				end = len(output)
			}
			b.WriteString(m.t("bug.last_error", output[idx:end]))
		}

		b.WriteString(m.t("bug.hint"))
		return streamMsg(b.String())
	}
}

func (m *Model) handleConfigCommand(parts []string) tea.Cmd {
	if len(parts) > 1 && strings.ToLower(parts[1]) == "set" {
		if len(parts) < 4 {
			m.output.WriteString(m.styles.error.Render(m.t("config.usage")))
			return nil
		}
		key := parts[2]
		value := parts[3]
		if m.config == nil {
			m.output.WriteString(m.styles.error.Render(m.t("config.not_loaded")))
			return nil
		}
		switch key {
		case "model":
			if err := m.config.SetActiveSelection(m.config.Vendor, m.config.Endpoint, value); err != nil {
				m.output.WriteString(m.styles.error.Render(m.t("command.model_failed", err)))
				return nil
			}
			if err := m.reloadActiveProvider(); err != nil {
				m.output.WriteString(m.styles.error.Render(m.t("command.model_failed", err)))
				return nil
			}
			m.output.WriteString(m.t("config.model_set", value))
		case "vendor":
			endpoints := m.config.EndpointNames(value)
			if len(endpoints) == 0 {
				m.output.WriteString(m.styles.error.Render(m.t("command.provider_unknown", value, m.vendorNames())))
				return nil
			}
			if err := m.config.SetActiveSelection(value, endpoints[0], ""); err != nil {
				m.output.WriteString(m.styles.error.Render(m.t("command.provider_failed", err)))
				return nil
			}
			if err := m.reloadActiveProvider(); err != nil {
				m.output.WriteString(m.styles.error.Render(m.t("command.provider_failed", err)))
				return nil
			}
			m.output.WriteString(m.t("config.provider_set", value))
		case "endpoint":
			if err := m.config.SetActiveSelection(m.config.Vendor, value, ""); err != nil {
				m.output.WriteString(m.styles.error.Render(m.t("command.provider_failed", err)))
				return nil
			}
			if err := m.reloadActiveProvider(); err != nil {
				m.output.WriteString(m.styles.error.Render(m.t("command.provider_failed", err)))
				return nil
			}
			m.output.WriteString(m.t("config.provider_set", value))
		case "language":
			m.applyLanguageChange(normalizeLanguage(value))
		default:
			m.output.WriteString(m.styles.error.Render(m.t("config.unknown_key", key)))
		}
		return nil
	}
	// Show current config
	var b strings.Builder
	b.WriteString(m.styles.title.Render(m.t("config.title")))
	if m.config != nil {
		b.WriteString(fmt.Sprintf("  Vendor:      %s\n", m.config.Vendor))
		b.WriteString(fmt.Sprintf("  Endpoint:    %s\n", m.config.Endpoint))
		b.WriteString(fmt.Sprintf("  Model:       %s\n", m.config.Model))
		b.WriteString(fmt.Sprintf("  Language:    %s\n", m.languageLabel()))
		if resolved, err := m.config.ResolveActiveEndpoint(); err == nil {
			if resolved.ContextWindow > 0 {
				b.WriteString(fmt.Sprintf("  Context:     %d\n", resolved.ContextWindow))
			}
			if resolved.MaxTokens > 0 {
				b.WriteString(fmt.Sprintf("  MaxTokens:   %d\n", resolved.MaxTokens))
			}
		}
		if len(m.config.Vendors) > 0 {
			b.WriteString(fmt.Sprintf("  Vendors:     %v\n", m.vendorNames()))
		}
		b.WriteString(fmt.Sprintf("  MCP Servers: %d\n", len(m.config.MCPServers)))
	}
	b.WriteString(m.styles.prompt.Render(m.t("config.usage")))
	m.output.WriteString(b.String())
	return nil
}

func (m *Model) handleStatusCommand() tea.Cmd {
	var b strings.Builder
	b.WriteString(m.styles.title.Render(m.t("status.title")))
	b.WriteString(fmt.Sprintf("  Version:     %s\n", version.Display()))
	b.WriteString(fmt.Sprintf("  Vendor:      %s\n", m.config.Vendor))
	b.WriteString(fmt.Sprintf("  Endpoint:    %s\n", m.config.Endpoint))
	b.WriteString(fmt.Sprintf("  Model:       %s\n", m.config.Model))
	b.WriteString(fmt.Sprintf("  Language:    %s\n", m.languageLabel()))
	b.WriteString(fmt.Sprintf("  Mode:        %s\n", m.mode))
	b.WriteString(fmt.Sprintf("  Fullscreen:  %v\n", m.fullscreen))

	if m.session != nil {
		b.WriteString(fmt.Sprintf("  Session:     %s\n", m.session.ID))
		b.WriteString(fmt.Sprintf("  Messages:    %d\n", len(m.session.Messages)))
	}

	if m.subAgentMgr != nil {
		n := m.subAgentMgr.RunningCount()
		b.WriteString(fmt.Sprintf("  Agents:      %d running\n", n))
	}

	connected := 0
	for _, srv := range m.mcpServers {
		if srv.Connected {
			connected++
		}
	}
	b.WriteString(fmt.Sprintf("  MCP Servers: %d connected (%d total)\n", connected, len(m.mcpServers)))
	b.WriteString(fmt.Sprintf("  Update:      %s\n", m.updateStatusSummary()))
	b.WriteString("\n")
	m.output.WriteString(b.String())
	return nil
}

func (m *Model) reloadActiveProvider() error {
	if err := m.config.Save(); err != nil {
		return err
	}
	if err := m.tryActivateCurrentSelection(); err != nil {
		return err
	}
	m.syncSessionSelection()
	return nil
}

func (m *Model) tryActivateCurrentSelection() error {
	if m.config == nil {
		return fmt.Errorf("config not loaded")
	}
	resolved, err := m.config.ResolveActiveEndpoint()
	if err != nil {
		return err
	}
	if resolved.APIKey == "" {
		return fmt.Errorf("no api key configured for vendor %q endpoint %q", resolved.VendorID, resolved.EndpointID)
	}
	prov, err := provider.NewProvider(resolved)
	if err != nil {
		return err
	}
	if m.agent != nil {
		m.agent.SetProvider(prov)
		if resolved.ContextWindow > 0 {
			m.agent.ContextManager().SetMaxTokens(resolved.ContextWindow)
		}
	}
	return nil
}

func (m *Model) syncSessionSelection() {
	if m.session == nil || m.config == nil {
		return
	}
	m.session.Vendor = m.config.Vendor
	m.session.Endpoint = m.config.Endpoint
	m.session.Model = m.config.Model
	if m.sessionStore != nil {
		_ = m.sessionStore.Save(m.session)
	}
}

func (m *Model) handlePluginsCommand() tea.Cmd {
	if m.pluginMgr == nil {
		m.output.WriteString(m.styles.prompt.Render(m.t("plugins.unavailable")))
		return nil
	}
	results := m.pluginMgr.Results()
	if len(results) == 0 {
		m.output.WriteString(m.styles.prompt.Render(m.t("plugins.none")))
		return nil
	}
	m.output.WriteString(m.styles.title.Render(m.t("plugins.title")))
	for _, r := range results {
		status := "\u2713"
		style := m.styles.assistant
		if !r.Success {
			status = "\u2717"
			style = m.styles.error
		}
		m.output.WriteString(style.Render(fmt.Sprintf("  %s %s", status, r.Name)))
		if r.Error != nil {
			m.output.WriteString(style.Render(fmt.Sprintf(" - %v", r.Error)))
		}
		m.output.WriteString("\n")
		for _, tn := range r.Tools {
			m.output.WriteString(fmt.Sprintf("    - %s\n", tn))
		}
	}
	m.output.WriteString("\n")
	return nil
}

func (m *Model) handleMCPCommand() tea.Cmd {
	if len(m.mcpServers) == 0 {
		m.output.WriteString(m.styles.prompt.Render(m.t("mcp.none")))
		return nil
	}
	m.openMCPPanel()
	return nil
}

func (m *Model) handleImageCommand(parts []string) tea.Cmd {
	if len(parts) < 2 {
		m.output.WriteString(m.styles.error.Render(m.t("image.usage")))
		m.output.WriteString(m.styles.prompt.Render(m.t("image.formats")))
		return nil
	}
	path := parts[1]
	return func() tea.Msg {
		img, err := image.ReadFile(path)
		if err != nil {
			return errMsg{err: fmt.Errorf("reading image: %w", err)}
		}
		placeholder := image.Placeholder(path, img)
		return imageAttachedMsg{
			placeholder: placeholder,
			img:         img,
			filename:    path,
		}
	}
}

func (m *Model) handleClipboardPaste() tea.Cmd {
	return func() tea.Msg {
		loader := m.clipboardLoader
		if loader == nil {
			loader = loadClipboardImage
		}
		msg, err := loader()
		if err != nil {
			return errMsg{err: fmt.Errorf(m.t("image.clipboard_failed"), err)}
		}
		return msg
	}
}

func (m *Model) handleFullscreenCommand() tea.Cmd {
	m.fullscreen = !m.fullscreen
	state := "off"
	if m.fullscreen {
		state = m.t("fullscreen.on")
	} else {
		state = m.t("fullscreen.off")
	}
	m.output.WriteString(m.t("fullscreen.state", state))
	return nil
}

func (m *Model) handleAgentsCommand(parts []string) tea.Cmd {
	if m.subAgentMgr == nil {
		m.output.WriteString(m.styles.error.Render(m.t("agents.unavailable")))
		return nil
	}
	agents := m.subAgentMgr.List()
	if len(agents) == 0 {
		m.output.WriteString(m.t("agents.none"))
		return nil
	}
	m.output.WriteString(m.t("agents.title", len(agents)))
	for _, sa := range agents {
		duration := ""
		if !sa.EndedAt.IsZero() && !sa.StartedAt.IsZero() {
			duration = fmt.Sprintf(" (%v)", sa.EndedAt.Sub(sa.StartedAt).Round(1e9))
		}
		m.output.WriteString(m.t("agents.item", sa.ID, sa.Status, duration, truncateStr(sa.Task, 60)))
	}
	m.output.WriteString(m.t("agents.hint"))
	return nil
}

func (m *Model) handleAgentDetailCommand(parts []string) tea.Cmd {
	if m.subAgentMgr == nil {
		m.output.WriteString(m.styles.error.Render(m.t("agents.unavailable")))
		return nil
	}
	if len(parts) < 2 {
		m.output.WriteString(m.t("agent.usage"))
		return nil
	}
	if parts[1] == "cancel" && len(parts) >= 3 {
		if m.subAgentMgr.Cancel(parts[2]) {
			m.output.WriteString(m.t("agent.cancelled", parts[2]))
		} else {
			m.output.WriteString(m.styles.error.Render(m.t("agent.cancel_failed", parts[2])))
		}
		return nil
	}
	sa, ok := m.subAgentMgr.Get(parts[1])
	if !ok {
		m.output.WriteString(m.styles.error.Render(m.t("agent.not_found", parts[1])))
		return nil
	}
	m.output.WriteString(m.t("agent.title", sa.ID, sa.Status, sa.Task))
	if sa.Result != "" {
		m.output.WriteString(m.t("agent.result", sa.Result))
	}
	if sa.Error != nil {
		m.output.WriteString(m.t("agent.error", sa.Error))
	}
	m.output.WriteString("\n")
	return nil
}
