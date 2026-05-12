package tui

import (
	"bytes"
	"context"
	"io"
	"os"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/topcheer/ggcode/internal/harness"
	"github.com/topcheer/ggcode/internal/safego"
	"github.com/topcheer/ggcode/internal/util"
)

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
	data, err := util.ReadAll(file, util.ReadLimitGeneral)
	if err != nil {
		return "", offset
	}
	if len(data) == 0 {
		return "", offset
	}
	return string(data), offset + int64(len(data))
}
