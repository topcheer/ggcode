package tui

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/topcheer/ggcode/internal/harness"
	"github.com/topcheer/ggcode/internal/safego"
)

var executeHarnessRun = func(ctx context.Context, project harness.Project, cfg *harness.Config, goal string, opts harness.RunTaskOptions) (*harness.RunSummary, error) {
	return harness.RunTaskWithOptions(ctx, project, cfg, goal, harness.BinaryRunner{}, opts)
}

var executeHarnessRerun = func(ctx context.Context, project harness.Project, cfg *harness.Config, taskID string, opts harness.RunTaskOptions) (*harness.RunSummary, error) {
	return harness.RerunTaskWithOptions(ctx, project, cfg, taskID, harness.BinaryRunner{}, opts)
}

func (m *Model) renderHarnessLiveTail(text string) {
	text = strings.TrimSpace(text)
	if text == m.harnessRunLiveTail {
		return
	}
	if !m.streamPrefixWritten {
		m.streamPrefixWritten = true
		m.nextAssistantID()
		m.chatEnsureAssistant()
	}
	m.harnessRunLiveTail = text
	m.chatListScrollToBottom()
}

func (m *Model) appendHarnessLogChunk(chunk string) {
	if chunk == "" {
		return
	}
	text := m.harnessRunRemainder + chunk
	lastNewline := strings.LastIndex(text, "\n")
	if lastNewline < 0 {
		m.harnessRunRemainder = text
		m.renderHarnessLiveTail(shortenHarnessPaths(m.harnessRunProject, text))
		return
	}
	m.harnessRunRemainder = text[lastNewline+1:]
	formatted := formatHarnessRunLogChunk(m.currentLanguage(), m.harnessRunProject, text[:lastNewline+1])
	if formatted != "" {
		m.appendStreamChunk(formatted)
	}
	m.renderHarnessLiveTail(shortenHarnessPaths(m.harnessRunProject, m.harnessRunRemainder))
}

func (m *Model) flushHarnessLogRemainder() {
	if strings.TrimSpace(m.harnessRunRemainder) == "" {
		m.harnessRunRemainder = ""
		m.renderHarnessLiveTail("")
		return
	}
	formatted := formatHarnessRunLogChunk(m.currentLanguage(), m.harnessRunProject, m.harnessRunRemainder+"\n")
	m.harnessRunRemainder = ""
	if formatted != "" {
		m.appendStreamChunk(formatted)
	}
	m.renderHarnessLiveTail("")
}

func (m *Model) appendHarnessProgressDetail(detail string) {
	detail = strings.TrimSpace(detail)
	if detail == "" {
		return
	}
	if m.harnessRunProject != nil {
		detail = normalizeHarnessDetail(m.currentLanguage(), *m.harnessRunProject, detail)
	}
	chunk := formatHarnessStructuredLine(detail)
	if chunk == "" {
		chunk = "→ " + detail + "\n"
	}
	if m.streamBuffer != nil && m.streamBuffer.Len() > 0 && !strings.HasSuffix(m.streamBuffer.String(), "\n\n") {
		chunk = "\n" + chunk
	}
	m.appendStreamChunk(chunk)
}

func trimHarnessRunOutputSection(rendered string) string {
	if idx := strings.Index(rendered, "\n\nOutput:\n"); idx >= 0 {
		return rendered[:idx]
	}
	return rendered
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
		return m.beginHarnessInitPrompt(strings.Join(parts, " "), goal, false)
	case "check":
		project, cfg, err := loadHarnessForTUI(workDir)
		if err != nil {
			m.chatWriteSystem(nextSystemID(), err.Error())
			return nil
		}
		report, err := harness.CheckProject(context.Background(), project, cfg, harness.CheckOptions{RunCommands: true})
		if err != nil {
			m.chatWriteSystem(nextSystemID(), err.Error())
			return nil
		}
		rendered := harness.FormatCheckReport(report)
		if report.Passed {
			m.chatWriteSystem(nextSystemID(), rendered)
		} else {
			m.chatWriteSystem(nextSystemID(), rendered)
		}
		return nil
	case "doctor":
		project, cfg, err := loadHarnessForTUI(workDir)
		if err != nil {
			m.chatWriteSystem(nextSystemID(), err.Error())
			return nil
		}
		report, err := harness.Doctor(project, cfg)
		if err != nil {
			m.chatWriteSystem(nextSystemID(), err.Error())
			return nil
		}
		m.chatWriteSystem(nextSystemID(), harness.FormatDoctorReport(report))
		return nil
	case "monitor":
		project, _, err := loadHarnessForTUI(workDir)
		if err != nil {
			m.chatWriteSystem(nextSystemID(), err.Error())
			return nil
		}
		report, err := harness.BuildMonitorReport(project, harness.MonitorOptions{})
		if err != nil {
			m.chatWriteSystem(nextSystemID(), err.Error())
			return nil
		}
		m.chatWriteSystem(nextSystemID(), harness.FormatMonitorReport(report))
		return nil
	case "gc":
		project, cfg, err := loadHarnessForTUI(workDir)
		if err != nil {
			m.chatWriteSystem(nextSystemID(), err.Error())
			return nil
		}
		report, err := harness.RunGC(project, cfg, time.Now().UTC())
		if err != nil {
			m.chatWriteSystem(nextSystemID(), err.Error())
			return nil
		}
		m.chatWriteSystem(nextSystemID(), harness.FormatGCReport(report))
		return nil
	case "contexts":
		project, cfg, err := loadHarnessForTUI(workDir)
		if err != nil {
			m.chatWriteSystem(nextSystemID(), err.Error())
			return nil
		}
		report, err := harness.BuildContextReport(project, cfg)
		if err != nil {
			m.chatWriteSystem(nextSystemID(), err.Error())
			return nil
		}
		m.chatWriteSystem(nextSystemID(), harness.FormatContextReport(report))
		return nil
	case "inbox":
		project, cfg, err := loadHarnessForTUI(workDir)
		if err != nil {
			m.chatWriteSystem(nextSystemID(), err.Error())
			return nil
		}
		if len(parts) == 2 {
			inbox, err := harness.BuildOwnerInbox(project, cfg)
			if err != nil {
				m.chatWriteSystem(nextSystemID(), err.Error())
				return nil
			}
			m.chatWriteSystem(nextSystemID(), harness.FormatOwnerInbox(inbox))
			return nil
		}
		if len(parts) < 4 {
			m.chatWriteSystem(nextSystemID(), m.t("command.harness_usage"))
			return nil
		}
		switch parts[2] {
		case "promote":
			tasks, err := harness.PromoteApprovedTasksForOwner(context.Background(), project, cfg, parts[3], strings.TrimSpace(strings.Join(parts[4:], " ")))
			if err != nil {
				m.chatWriteSystem(nextSystemID(), err.Error())
				return nil
			}
			m.chatWriteSystem(nextSystemID(), m.t("command.harness_owner_promoted", len(tasks), parts[3]))
			return nil
		case "retry":
			summary, err := harness.RetryFailedTasksForOwner(context.Background(), project, cfg, parts[3], harness.BinaryRunner{})
			if err != nil {
				m.chatWriteSystem(nextSystemID(), err.Error())
				return nil
			}
			m.chatWriteSystem(nextSystemID(), harness.FormatQueueSummary(summary))
			return nil
		}
		m.chatWriteSystem(nextSystemID(), m.t("command.harness_usage"))
		return nil
	case "review":
		project, _, err := loadHarnessForTUI(workDir)
		if err != nil {
			m.chatWriteSystem(nextSystemID(), err.Error())
			return nil
		}
		if len(parts) == 2 {
			tasks, err := harness.ListReviewableTasks(project)
			if err != nil {
				m.chatWriteSystem(nextSystemID(), err.Error())
				return nil
			}
			m.chatWriteSystem(nextSystemID(), harness.FormatReviewList(tasks))
			return nil
		}
		if len(parts) < 4 {
			m.chatWriteSystem(nextSystemID(), m.t("command.harness_usage"))
			return nil
		}
		note := strings.TrimSpace(strings.Join(parts[4:], " "))
		switch parts[2] {
		case "approve":
			task, err := harness.ApproveTaskReview(project, parts[3], note)
			if err != nil {
				m.chatWriteSystem(nextSystemID(), err.Error())
				return nil
			}
			m.chatWriteSystem(nextSystemID(), m.t("command.harness_review_approved", task.ID))
			return nil
		case "reject":
			task, err := harness.RejectTaskReview(project, parts[3], note)
			if err != nil {
				m.chatWriteSystem(nextSystemID(), err.Error())
				return nil
			}
			m.chatWriteSystem(nextSystemID(), m.t("command.harness_review_rejected", task.ID))
			return nil
		}
		m.chatWriteSystem(nextSystemID(), m.t("command.harness_usage"))
		return nil
	case "promote":
		project, _, err := loadHarnessForTUI(workDir)
		if err != nil {
			m.chatWriteSystem(nextSystemID(), err.Error())
			return nil
		}
		if len(parts) == 2 {
			tasks, err := harness.ListPromotableTasks(project)
			if err != nil {
				m.chatWriteSystem(nextSystemID(), err.Error())
				return nil
			}
			m.chatWriteSystem(nextSystemID(), harness.FormatPromotionList(tasks))
			return nil
		}
		if len(parts) < 4 || parts[2] != "apply" {
			m.chatWriteSystem(nextSystemID(), m.t("command.harness_usage"))
			return nil
		}
		note := strings.TrimSpace(strings.Join(parts[4:], " "))
		if parts[3] == "all" {
			tasks, err := harness.PromoteApprovedTasks(context.Background(), project, note)
			if err != nil {
				m.chatWriteSystem(nextSystemID(), err.Error())
				return nil
			}
			m.chatWriteSystem(nextSystemID(), m.t("command.harness_promoted_many", len(tasks)))
			return nil
		}
		task, err := harness.PromoteTask(context.Background(), project, parts[3], note)
		if err != nil {
			m.chatWriteSystem(nextSystemID(), err.Error())
			return nil
		}
		m.chatWriteSystem(nextSystemID(), m.t("command.harness_promoted_one", task.ID))
		return nil
	case "release":
		project, cfg, err := loadHarnessForTUI(workDir)
		if err != nil {
			m.chatWriteSystem(nextSystemID(), err.Error())
			return nil
		}
		if len(parts) == 2 {
			plan, err := harness.BuildReleasePlan(project, cfg)
			if err != nil {
				m.chatWriteSystem(nextSystemID(), err.Error())
				return nil
			}
			m.chatWriteSystem(nextSystemID(), harness.FormatReleasePlan(plan))
			return nil
		}
		switch parts[2] {
		case "waves":
			if len(parts) < 4 {
				m.chatWriteSystem(nextSystemID(), m.t("command.harness_usage"))
				return nil
			}
			waves, err := harness.BuildReleaseWavePlan(project, cfg, harness.ReleasePlanOptions{}, parts[3])
			if err != nil {
				m.chatWriteSystem(nextSystemID(), err.Error())
				return nil
			}
			m.chatWriteSystem(nextSystemID(), harness.FormatReleaseWavePlan(waves))
			return nil
		case "apply":
			if len(parts) >= 5 && parts[3] == "waves" {
				waves, err := harness.BuildReleaseWavePlan(project, cfg, harness.ReleasePlanOptions{}, parts[4])
				if err != nil {
					m.chatWriteSystem(nextSystemID(), err.Error())
					return nil
				}
				waves, err = harness.ApplyReleaseWavePlan(project, waves, "", "")
				if err != nil {
					m.chatWriteSystem(nextSystemID(), err.Error())
					return nil
				}
				m.chatWriteSystem(nextSystemID(), harness.FormatReleaseWavePlan(waves))
				return nil
			}
			plan, err := harness.BuildReleasePlan(project, cfg)
			if err != nil {
				m.chatWriteSystem(nextSystemID(), err.Error())
				return nil
			}
			plan, err = harness.ApplyReleasePlan(project, plan, "")
			if err != nil {
				m.chatWriteSystem(nextSystemID(), err.Error())
				return nil
			}
			m.chatWriteSystem(nextSystemID(), harness.FormatReleasePlan(plan))
			return nil
		case "rollouts":
			rollouts, err := harness.ListReleaseWaveRollouts(project)
			if err != nil {
				m.chatWriteSystem(nextSystemID(), err.Error())
				return nil
			}
			m.chatWriteSystem(nextSystemID(), harness.FormatReleaseWaveRollouts(rollouts))
			return nil
		case "advance":
			if len(parts) < 4 {
				m.chatWriteSystem(nextSystemID(), m.t("command.harness_usage"))
				return nil
			}
			rollout, err := harness.AdvanceReleaseWaveRollout(project, parts[3])
			if err != nil {
				m.chatWriteSystem(nextSystemID(), err.Error())
				return nil
			}
			m.chatWriteSystem(nextSystemID(), harness.FormatReleaseWavePlan(rollout))
			return nil
		case "pause":
			if len(parts) < 4 {
				m.chatWriteSystem(nextSystemID(), m.t("command.harness_usage"))
				return nil
			}
			rollout, err := harness.PauseReleaseWaveRollout(project, parts[3], strings.TrimSpace(strings.Join(parts[4:], " ")))
			if err != nil {
				m.chatWriteSystem(nextSystemID(), err.Error())
				return nil
			}
			m.chatWriteSystem(nextSystemID(), harness.FormatReleaseWavePlan(rollout))
			return nil
		case "resume":
			if len(parts) < 4 {
				m.chatWriteSystem(nextSystemID(), m.t("command.harness_usage"))
				return nil
			}
			rollout, err := harness.ResumeReleaseWaveRollout(project, parts[3], strings.TrimSpace(strings.Join(parts[4:], " ")))
			if err != nil {
				m.chatWriteSystem(nextSystemID(), err.Error())
				return nil
			}
			m.chatWriteSystem(nextSystemID(), harness.FormatReleaseWavePlan(rollout))
			return nil
		case "abort":
			if len(parts) < 4 {
				m.chatWriteSystem(nextSystemID(), m.t("command.harness_usage"))
				return nil
			}
			rollout, err := harness.AbortReleaseWaveRollout(project, parts[3], strings.TrimSpace(strings.Join(parts[4:], " ")))
			if err != nil {
				m.chatWriteSystem(nextSystemID(), err.Error())
				return nil
			}
			m.chatWriteSystem(nextSystemID(), harness.FormatReleaseWavePlan(rollout))
			return nil
		case "approve", "reject":
			if len(parts) < 4 {
				m.chatWriteSystem(nextSystemID(), m.t("command.harness_usage"))
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
				m.chatWriteSystem(nextSystemID(), err.Error())
				return nil
			}
			m.chatWriteSystem(nextSystemID(), harness.FormatReleaseWavePlan(rollout))
			return nil
		}
		m.chatWriteSystem(nextSystemID(), m.t("command.harness_usage"))
		return nil
	case "queue":
		if len(parts) < 3 {
			m.chatWriteSystem(nextSystemID(), m.t("command.harness_queue_usage"))
			return nil
		}
		project, _, err := loadHarnessForTUI(workDir)
		if err != nil {
			m.chatWriteSystem(nextSystemID(), err.Error())
			return nil
		}
		task, err := harness.EnqueueTask(project, strings.TrimSpace(strings.Join(parts[2:], " ")), "tui")
		if err != nil {
			m.chatWriteSystem(nextSystemID(), err.Error())
			return nil
		}
		m.chatWriteSystem(nextSystemID(), m.t("command.harness_task_queued_detail", task.ID, task.Goal))
		return nil
	case "tasks":
		project, _, err := loadHarnessForTUI(workDir)
		if err != nil {
			m.chatWriteSystem(nextSystemID(), err.Error())
			return nil
		}
		tasks, err := harness.ListTasks(project)
		if err != nil {
			m.chatWriteSystem(nextSystemID(), err.Error())
			return nil
		}
		if len(tasks) == 0 {
			m.chatWriteSystem(nextSystemID(), m.t("command.harness_tasks_empty"))
			return nil
		}
		m.chatWriteSystem(nextSystemID(), harness.FormatTaskList(tasks))
		return nil
	case "run-queued":
		project, cfg, err := loadHarnessForTUI(workDir)
		if err != nil {
			m.chatWriteSystem(nextSystemID(), err.Error())
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
		opts.ConfirmDirtyWorkspace = m.newHarnessCheckpointConfirmer()
		queueSummary, err := harness.RunQueuedTasks(context.Background(), project, cfg, nil, opts)
		if err != nil {
			m.chatWriteSystem(nextSystemID(), err.Error())
			return nil
		}
		m.chatWriteSystem(nextSystemID(), harness.FormatQueueSummary(queueSummary))
		return nil
	case "run":
		if len(parts) < 3 {
			m.chatWriteSystem(nextSystemID(), m.t("command.harness_run_usage"))
			return nil
		}
		project, cfg, err := loadHarnessForTUI(workDir)
		if err != nil {
			m.chatWriteSystem(nextSystemID(), err.Error())
			return nil
		}
		return m.beginHarnessRunPrompt(strings.Join(parts, " "), strings.TrimSpace(strings.Join(parts[2:], " ")), project, cfg, false)
	case "rerun":
		if len(parts) != 3 {
			m.chatWriteSystem(nextSystemID(), m.t("command.harness_rerun_usage"))
			return nil
		}
		project, cfg, err := loadHarnessForTUI(workDir)
		if err != nil {
			m.chatWriteSystem(nextSystemID(), err.Error())
			return nil
		}
		task, err := harness.LoadTask(project, parts[2])
		if err != nil {
			m.chatWriteSystem(nextSystemID(), err.Error())
			return nil
		}
		return m.runTrackedHarnessRerun(strings.Join(parts, " "), project, cfg, task)
	default:
		m.chatWriteSystem(nextSystemID(), m.t("command.harness_usage"))
		return nil
	}
}

func (m *Model) runTrackedHarnessGoal(commandText, goal string, project harness.Project, cfg *harness.Config, opts harness.RunTaskOptions) tea.Cmd {
	goal = strings.TrimSpace(goal)
	if goal == "" {
		m.chatWriteSystem(nextSystemID(), m.t("command.harness_run_usage"))
		return nil
	}
	m.chatWriteUser(nextChatID(), strings.TrimSpace(commandText))
	m.appendUserMessage(strings.TrimSpace(commandText))
	m.chatWriteSystem(nextSystemID(), m.t("command.harness_run_start"))
	m.chatListScrollToBottom()

	ctx, cancel := context.WithCancel(context.Background())
	m.cancelFunc = cancel
	m.loading = true
	m.runCanceled = false
	m.runFailed = false
	m.statusActivity = m.t("command.harness_status_starting_run")
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
	m.harnessRunRemainder = ""
	m.harnessRunLiveTail = ""
	m.streamBuffer = &bytes.Buffer{}
	m.streamPrefixWritten = false
	opts.ConfirmDirtyWorkspace = m.newHarnessCheckpointConfirmer()
	startSpinner := m.spinner.Start(m.t("command.harness_spinner_running"))
	if m.program == nil {
		return func() tea.Msg {
			summary, err := executeHarnessRun(ctx, project, cfg, goal, opts)
			return harnessRunResultMsg{Summary: summary, Err: err}
		}
	}
	safego.Go("tui.commands.harnessRun", func() {
		summary, err := executeHarnessRun(ctx, project, cfg, goal, opts)
		m.program.Send(harnessRunResultMsg{Summary: summary, Err: err})
	})
	return tea.Batch(startSpinner, m.pollHarnessRunProgress())
}

func (m *Model) runTrackedHarnessRerun(commandText string, project harness.Project, cfg *harness.Config, task *harness.Task) tea.Cmd {
	if task == nil {
		m.chatWriteSystem(nextSystemID(), m.t("command.harness_rerun_usage"))
		return nil
	}
	if task.Status != harness.TaskFailed {
		m.chatWriteSystem(nextSystemID(), m.t("command.harness_rerun_invalid_status", task.ID, localizeHarnessTaskStatus(m.currentLanguage(), task.Status)))
		return nil
	}
	m.chatWriteUser(nextChatID(), strings.TrimSpace(commandText))
	m.appendUserMessage(strings.TrimSpace(commandText))
	m.chatWriteSystem(nextSystemID(), m.t("command.harness_rerun_start"))
	m.chatListScrollToBottom()

	ctx, cancel := context.WithCancel(context.Background())
	m.cancelFunc = cancel
	m.loading = true
	m.runCanceled = false
	m.runFailed = false
	m.statusActivity = m.t("command.harness_status_starting_rerun")
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
	m.harnessRunRemainder = ""
	m.harnessRunLiveTail = ""
	m.streamBuffer = &bytes.Buffer{}
	m.streamPrefixWritten = false
	opts := harness.RunTaskOptions{ConfirmDirtyWorkspace: m.newHarnessCheckpointConfirmer()}
	startSpinner := m.spinner.Start(m.t("command.harness_spinner_running"))
	if m.program == nil {
		return func() tea.Msg {
			summary, err := executeHarnessRerun(ctx, project, cfg, task.ID, opts)
			return harnessRunResultMsg{Summary: summary, Err: err}
		}
	}
	safego.Go("tui.commands.harnessRerun", func() {
		summary, err := executeHarnessRerun(ctx, project, cfg, task.ID, opts)
		m.program.Send(harnessRunResultMsg{Summary: summary, Err: err})
	})
	return tea.Batch(startSpinner, m.pollHarnessRunProgress())
}

func (m *Model) newHarnessCheckpointConfirmer() harness.ConfirmDirtyWorkspaceFunc {
	var (
		asked    bool
		approved bool
	)
	return func(checkpoint harness.DirtyWorkspaceCheckpoint) (bool, error) {
		if asked {
			return approved, nil
		}
		asked = true
		ok, err := m.requestHarnessCheckpointConfirm(checkpoint)
		if err != nil {
			return false, err
		}
		approved = ok
		return approved, nil
	}
}

func (m *Model) requestHarnessCheckpointConfirm(checkpoint harness.DirtyWorkspaceCheckpoint) (bool, error) {
	if m.program == nil {
		return true, nil
	}
	resp := make(chan bool, 1)
	m.program.Send(HarnessCheckpointConfirmMsg{
		Checkpoint: checkpoint,
		Response:   resp,
	})
	return <-resp, nil
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
		return readHarnessRunProgress(m.currentLanguage(), *project, goal, taskID, logPath, logOffset)
	})
}

func readHarnessRunProgress(lang Language, project harness.Project, goal, taskID, logPath string, logOffset int64) harnessRunProgressMsg {
	msg := harnessRunProgressMsg{
		TaskID:    strings.TrimSpace(taskID),
		LogPath:   strings.TrimSpace(logPath),
		LogOffset: logOffset,
	}
	goal = strings.TrimSpace(goal)
	if msg.TaskID != "" {
		task, err := harness.LoadTask(project, msg.TaskID)
		if err == nil && task != nil {
			msg = populateHarnessRunProgress(lang, project, msg, task)
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
		msg = populateHarnessRunProgress(lang, project, msg, task)
		return msg
	}
	msg.Activity = tr(lang, "command.harness_status_starting_run")
	return msg
}

func populateHarnessRunProgress(lang Language, project harness.Project, msg harnessRunProgressMsg, task *harness.Task) harnessRunProgressMsg {
	if task == nil {
		return msg
	}
	msg.TaskID = task.ID
	msg.Activity = formatHarnessRunActivity(lang, project, task)
	msg.Detail = formatHarnessRunDetail(lang, project, task)
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

func formatHarnessRunActivity(lang Language, project harness.Project, task *harness.Task) string {
	if task == nil {
		return tr(lang, "command.harness_status_starting_run")
	}
	parts := []string{tr(lang, "harness.activity.status", localizeHarnessTaskStatus(lang, task.Status))}
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
		parts = append(parts, humanizeHarnessProgress(lang, project, progress))
	}
	return strings.Join(parts, " • ")
}

func formatHarnessRunDetail(lang Language, project harness.Project, task *harness.Task) string {
	if task == nil {
		return ""
	}
	if progress := strings.TrimSpace(task.WorkerProgress); progress != "" {
		return humanizeHarnessProgress(lang, project, progress)
	}
	if phase := strings.TrimSpace(task.WorkerPhase); phase != "" {
		return "🪜 " + tr(lang, "harness.log.phase") + " · " + phase
	}
	if status := strings.TrimSpace(task.WorkerStatus); status != "" {
		return "🤖 " + tr(lang, "harness.log.worker") + " · " + status
	}
	return ""
}

func humanizeHarnessProgress(lang Language, project harness.Project, progress string) string {
	progress = strings.TrimSpace(progress)
	if progress == "" {
		return ""
	}
	if rendered, structured := formatHarnessLogLine(lang, &project, progress); structured {
		return strings.TrimSpace(rendered)
	}
	return normalizeHarnessDetail(lang, project, progress)
}

func normalizeHarnessDetail(lang Language, project harness.Project, detail string) string {
	detail = strings.TrimSpace(detail)
	switch {
	case strings.HasPrefix(detail, "running "):
		name, args := splitHarnessToolCall(strings.TrimSpace(strings.TrimPrefix(detail, "running ")))
		label, icon := humanizeHarnessTool(lang, name)
		summary := summarizeHarnessToolArgs(&project, name, args)
		if summary == "" {
			return fmt.Sprintf("%s %s", icon, label)
		}
		return fmt.Sprintf("%s %s · %s", icon, label, summary)
	case strings.HasPrefix(detail, "result "):
		return formatHarnessToolResult(&project, strings.TrimSpace(strings.TrimPrefix(detail, "result ")))
	default:
		return shortenHarnessPaths(&project, detail)
	}
}

func formatHarnessRunLogChunk(lang Language, project *harness.Project, chunk string) string {
	if chunk == "" {
		return ""
	}
	lines := strings.SplitAfter(chunk, "\n")
	var b strings.Builder
	lastStructured := false
	for _, line := range lines {
		if line == "" {
			continue
		}
		hasNewline := strings.HasSuffix(line, "\n")
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		rendered, structured := formatHarnessLogLine(lang, project, trimmed)
		if rendered == "" {
			continue
		}
		if structured {
			if b.Len() > 0 && !strings.HasSuffix(b.String(), "\n\n") {
				b.WriteString("\n")
			}
			b.WriteString(rendered)
			if !strings.HasSuffix(rendered, "\n\n") {
				b.WriteString("\n\n")
			}
		} else {
			if lastStructured && !strings.HasSuffix(b.String(), "\n") {
				b.WriteString("\n")
			}
			b.WriteString(rendered)
			if hasNewline {
				b.WriteString("\n")
			}
		}
		lastStructured = structured
	}
	return b.String()
}

func formatHarnessLogLine(lang Language, project *harness.Project, line string) (string, bool) {
	line = strings.TrimSpace(line)
	if line == "" {
		return "", false
	}
	switch {
	case strings.HasPrefix(line, "tool: "):
		name, args := splitHarnessToolCall(strings.TrimSpace(strings.TrimPrefix(line, "tool: ")))
		label, icon := humanizeHarnessTool(lang, name)
		summary := summarizeHarnessToolArgs(project, name, args)
		if summary == "" {
			return fmt.Sprintf("%s %s", icon, label), true
		}
		return fmt.Sprintf("%s %s · %s", icon, label, summary), true
	case strings.HasPrefix(line, "tool result: "):
		return formatHarnessToolResult(project, strings.TrimSpace(strings.TrimPrefix(line, "tool result: "))), true
	case strings.HasPrefix(line, "phase: "):
		return "🪜 " + tr(lang, "harness.log.phase") + " · " + shortenHarnessPaths(project, strings.TrimSpace(strings.TrimPrefix(line, "phase: "))), true
	case strings.HasPrefix(line, "worker: "):
		return "🤖 " + tr(lang, "harness.log.worker") + " · " + shortenHarnessPaths(project, strings.TrimSpace(strings.TrimPrefix(line, "worker: "))), true
	default:
		return shortenHarnessPaths(project, line), false
	}
}

func formatHarnessStructuredLine(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	return text + "\n\n"
}

func harnessLogChunkContainsDetail(lang Language, project *harness.Project, chunk, detail string) bool {
	chunk = strings.TrimSpace(chunk)
	detail = strings.TrimSpace(detail)
	if chunk == "" || detail == "" {
		return false
	}
	if project != nil {
		detail = normalizeHarnessDetail(lang, *project, detail)
	}
	needle := strings.TrimSpace(formatHarnessStructuredLine(detail))
	if needle == "" {
		return false
	}
	return strings.Contains(formatHarnessRunLogChunk(lang, project, chunk), needle)
}

func splitHarnessToolCall(text string) (string, string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", ""
	}
	name, rest, found := strings.Cut(text, " ")
	if !found {
		return name, ""
	}
	return name, strings.TrimSpace(rest)
}

func humanizeHarnessTool(lang Language, name string) (string, string) {
	switch name {
	case "read_file", "view":
		return tr(lang, "harness.tool.read_file"), "📖"
	case "write_file", "edit_file", "apply_patch":
		return tr(lang, "harness.tool.write_file"), "✍️"
	case "list_directory", "glob":
		return tr(lang, "harness.tool.browse_files"), "📂"
	case "grep", "rg":
		return tr(lang, "harness.tool.search_code"), "🔎"
	case "bash", "run_command", "start_command", "wait_command", "read_command", "write_command_input", "stop_command":
		return tr(lang, "harness.tool.run_command"), "⚙️"
	case "web_fetch":
		return tr(lang, "harness.tool.fetch_web_page"), "🌐"
	case "task", "read_agent", "list_agents":
		return tr(lang, "harness.tool.run_subagent"), "🤖"
	case "todo_write", "todo_read", "sql":
		return tr(lang, "harness.tool.update_task_state"), "🗂️"
	default:
		return titleizeHarnessText(strings.ReplaceAll(name, "_", " ")), "🧰"
	}
}

func localizeHarnessTaskStatus(lang Language, status harness.TaskStatus) string {
	switch status {
	case harness.TaskQueued:
		if lang == LangZhCN {
			return "排队中"
		}
		return "queued"
	case harness.TaskRunning:
		if lang == LangZhCN {
			return "运行中"
		}
		return "running"
	case harness.TaskCompleted:
		if lang == LangZhCN {
			return "已完成"
		}
		return "completed"
	case harness.TaskFailed:
		if lang == LangZhCN {
			return "失败"
		}
		return "failed"
	case harness.TaskBlocked:
		if lang == LangZhCN {
			return "阻塞"
		}
		return "blocked"
	case harness.TaskAbandoned:
		if lang == LangZhCN {
			return "已放弃"
		}
		return "abandoned"
	default:
		return string(status)
	}
}

func summarizeHarnessToolArgs(project *harness.Project, name, args string) string {
	args = strings.TrimSpace(shortenHarnessPaths(project, args))
	if args == "" {
		return ""
	}
	switch name {
	case "read_file", "write_file", "edit_file", "view":
		fields := strings.Fields(args)
		if len(fields) > 0 {
			return fields[0]
		}
	}
	return truncateHarnessText(args, 96)
}

func formatHarnessToolResult(project *harness.Project, result string) string {
	result = strings.TrimSpace(shortenHarnessPaths(project, result))
	if result == "" {
		return "✅ Result"
	}
	lower := strings.ToLower(result)
	switch {
	case strings.HasPrefix(lower, "error"):
		return "❌ " + result
	case strings.HasPrefix(result, "Successfully "):
		return "✅ " + strings.TrimPrefix(result, "Successfully ")
	default:
		return "✅ Result · " + truncateHarnessText(result, 110)
	}
}

func shortenHarnessPaths(project *harness.Project, text string) string {
	root := ""
	if project != nil {
		root = strings.TrimSpace(project.RootDir)
	}
	if root == "" {
		return text
	}
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return text
	}
	for i, field := range fields {
		fields[i] = shortenHarnessPathToken(root, field)
	}
	return strings.Join(fields, " ")
}

func shortenHarnessPathToken(root, token string) string {
	trimmed := strings.Trim(token, `"'()[]{}<>,`)
	idx := strings.Index(token, trimmed)
	if trimmed == "" || idx < 0 || !filepath.IsAbs(trimmed) {
		return token
	}
	prefix := token[:idx]
	suffix := token[idx+len(trimmed):]
	rel, err := filepath.Rel(root, trimmed)
	if err != nil {
		return token
	}
	if rel == "." {
		trimmed = "."
	} else if rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		trimmed = rel
	}
	return prefix + trimmed + suffix
}

func truncateHarnessText(text string, limit int) string {
	text = strings.TrimSpace(text)
	if limit <= 0 || len(text) <= limit {
		return text
	}
	return strings.TrimSpace(text[:limit-1]) + "…"
}

func titleizeHarnessText(text string) string {
	parts := strings.Fields(strings.TrimSpace(text))
	for i, part := range parts {
		if part == "" {
			continue
		}
		parts[i] = strings.ToUpper(part[:1]) + part[1:]
	}
	return strings.Join(parts, " ")
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
	if strings.TrimSpace(result.ScaffoldCommit) != "" {
		b.WriteString("- git: created scaffold commit ")
		b.WriteString(shortHarnessCommit(result.ScaffoldCommit))
		b.WriteString("\n")
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
	if result.Config != nil && len(result.Config.Contexts) > 0 {
		b.WriteString("- contexts:\n")
		for _, contextCfg := range result.Config.Contexts {
			label := firstNonEmptyHarness(contextCfg.Path, contextCfg.Name)
			if label == "" {
				continue
			}
			b.WriteString("  - ")
			b.WriteString(label)
			b.WriteString("\n")
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func shortHarnessCommit(commit string) string {
	commit = strings.TrimSpace(commit)
	if len(commit) > 12 {
		return commit[:12]
	}
	return commit
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
