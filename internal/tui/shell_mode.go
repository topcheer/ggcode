package tui

import (
	"bytes"
	"context"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	toolpkg "github.com/topcheer/ggcode/internal/tool"
)

const (
	shellCommandPollInterval = 100 * time.Millisecond
	shellCommandTailLines    = 400
)

type shellCommandStreamMsg struct {
	RunID int
	Text  string
}

type shellCommandDoneMsg struct {
	RunID   int
	Status  toolpkg.CommandJobStatus
	ErrText string
}

func parseShellCommand(text string) (string, bool) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return "", false
	}
	switch trimmed[0] {
	case '$', '!':
		return strings.TrimSpace(trimmed[1:]), true
	default:
		return "", false
	}
}

func (m *Model) setShellMode(enabled bool) {
	m.shellMode = enabled
	m.syncComposerMode()
}

func (m *Model) syncComposerMode() {
	if m.shellMode {
		m.input.Prompt = "$ "
		if m.currentLanguage() == LangZhCN {
			m.input.Placeholder = "输入 shell 命令，Esc 退出命令模式"
		} else {
			m.input.Placeholder = "Type a shell command, Esc exits command mode"
		}
		return
	}
	m.input.Prompt = "❯ "
	m.input.Placeholder = m.t("input.placeholder")
}

func shellStatusActivity(lang Language) string {
	if lang == LangZhCN {
		return "执行命令"
	}
	return "Running command"
}

func (m *Model) submitShellCommand(command string, addToHistory bool) tea.Cmd {
	command = strings.TrimSpace(command)
	if command == "" {
		m.output.WriteString(m.styles.error.Render("Shell command is empty."))
		m.output.WriteString("\n")
		return nil
	}
	if addToHistory {
		m.history = append(m.history, "$ "+command)
		m.historyIdx = len(m.history)
	}
	m.ensureOutputEndsWithNewline()
	m.output.WriteString(m.renderConversationUserEntry("$ ", command))
	m.output.WriteString("\n")
	m.appendUserMessage("$ " + command)
	m.loading = true
	m.runCanceled = false
	m.runFailed = false
	m.statusActivity = shellStatusActivity(m.currentLanguage())
	m.statusToolName = ""
	m.statusToolArg = command
	m.statusToolCount = 0
	m.streamBuffer = nil
	m.shellBuffer = &bytes.Buffer{}
	m.streamPrefixWritten = false
	m.resetActivityGroups()
	return tea.Batch(m.startLoadingSpinner(m.statusActivity), m.startShellCommand(command))
}

func (m *Model) appendShellChunk(chunk string) {
	if chunk == "" {
		return
	}
	if m.shellBuffer == nil {
		m.shellBuffer = &bytes.Buffer{}
	}
	if m.shellBuffer.Len() == 0 {
		m.ensureOutputEndsWithNewline()
	}
	m.shellBuffer.WriteString(chunk)
	m.output.WriteString(chunk)
	m.trimOutput()
	m.syncConversationViewport()
	if m.viewport.AutoFollow() {
		m.viewport.GotoBottom()
	}
}

func (m *Model) startShellCommand(command string) tea.Cmd {
	ctx, cancel := context.WithCancel(context.Background())
	m.cancelFunc = cancel
	m.activeShellRunID++
	runID := m.activeShellRunID

	return func() tea.Msg {
		go func() {
			defer cancel()
			workDir, _ := os.Getwd()
			manager := toolpkg.NewCommandJobManager(workDir)
			snapshot, err := manager.Start(ctx, command, 0)
			if err != nil {
				if m.program != nil {
					m.program.Send(shellCommandDoneMsg{RunID: runID, Status: toolpkg.CommandJobFailed, ErrText: err.Error()})
				}
				return
			}

			sinceLine := 0
			if snapshot != nil {
				sinceLine = snapshot.TotalLines
			}
			for {
				current, err := manager.Wait(context.Background(), snapshot.ID, shellCommandPollInterval, shellCommandTailLines, sinceLine)
				if err != nil {
					if m.program != nil {
						m.program.Send(shellCommandDoneMsg{RunID: runID, Status: toolpkg.CommandJobFailed, ErrText: err.Error()})
					}
					return
				}
				if len(current.Lines) > 0 {
					text := strings.Join(current.Lines, "\n")
					if !strings.HasSuffix(text, "\n") {
						text += "\n"
					}
					if m.program != nil {
						m.program.Send(shellCommandStreamMsg{RunID: runID, Text: text})
					}
				}
				sinceLine = current.TotalLines
				if !current.Running {
					if m.program != nil {
						m.program.Send(shellCommandDoneMsg{RunID: runID, Status: current.Status, ErrText: current.ErrText})
					}
					return
				}
			}
		}()
		return nil
	}
}
