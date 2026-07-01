package tui

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/topcheer/ggcode/internal/image"
	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/safego"
	"github.com/topcheer/ggcode/internal/session"
	"github.com/topcheer/ggcode/internal/tunnel"
)

// handleClearChat creates a new session, clears the conversation view,
// and notifies any connected mobile client.
func (m *Model) handleClearChat() {
	// Save current session first.
	if m.session != nil && m.sessionStore != nil {
		oldSes := m.session
		oldStore := m.sessionStore
		safego.Go("tui.clearChat.sessionSave", func() { _ = oldStore.Save(oldSes) })
	}

	// Create new session.
	vendor, endpoint, model := "", "", ""
	if m.config != nil {
		vendor = m.config.Vendor
		endpoint = m.config.Endpoint
		model = m.config.Model
	}
	ses := session.NewSession(vendor, endpoint, model)
	if m.sessionStore != nil {
		m.sessionStore.Save(ses)
	}
	m.SetSession(ses, m.sessionStore)

	// Clear agent conversation state.
	if m.agent != nil {
		m.agent.Clear()
	}

	// Clear local view.
	m.resetConversationView()

	// Notify mobile client.
	m.publishTunnelSnapshotForCurrentSession(true)

	m.chatWriteSystem(nextSystemID(), m.t("session.new", ses.ID))
}

// handleUnshare stops the active tunnel/sharing session.
func (m *Model) handleUnshare() {
	if m.tunnelSession == nil && !m.tunnelStarting {
		m.chatWriteSystem(nextSystemID(), m.t("tunnel.not_active"))
		return
	}
	m.closeTunnelGracefullyAsync(2 * time.Second)
	m.chatWriteSystem(nextSystemID(), m.t("tunnel.stopped"))
}

func (m *Model) resetConversationView() {
	m.chatReset()
	m.streamBuffer = nil
	m.streamPrefixWritten = false
	m.setLoading(false)
	m.statusActivity = ""
	m.statusToolName = ""
	m.statusToolArg = ""
	m.statusToolCount = 0
	m.autoCompleteActive = false
	m.autoCompleteItems = nil
	m.autoCompleteIndex = 0
	m.exitConfirmPending = false
	m.clearPendingSubmissions()
	m.runCanceled = false
	m.runFailed = false
	m.spinner.Stop()
	m.chatListScrollToBottom()
}

func (m *Model) handleApproval(d permission.Decision) tea.Cmd {
	pa := m.pendingApproval
	requestID := m.tunnelPendingApprovalID
	m.pendingApproval = nil
	m.tunnelPendingApprovalID = ""
	if pa == nil || pa.Response == nil {
		return nil
	}
	m.pushTunnelApprovalResult(requestID, tunnelDecisionString(d))
	safego.Go("tui.commands.approvalRespond", func() {
		select {
		case pa.Response <- d:
		default:
			// Receiver already gone; drop to avoid goroutine leak.
		}
	})
	return nil
}

func (m *Model) handleApprovalAllowAlways() tea.Cmd {
	pa := m.pendingApproval
	requestID := m.tunnelPendingApprovalID
	m.pendingApproval = nil
	m.tunnelPendingApprovalID = ""
	if pa != nil && m.policy != nil {
		m.policy.SetOverride(pa.ToolName, permission.Allow)
		present := describeTool(m.currentLanguage(), pa.ToolName, pa.Input)
		toolLine := formatToolInline(present.DisplayName, present.Detail)
		if m.currentLanguage() == LangZhCN {
			m.chatWriteSystem(nextSystemID(), fmt.Sprintf("\u2713 已总是允许：%s", toolLine))
		} else {
			m.chatWriteSystem(nextSystemID(), fmt.Sprintf("\u2713 Always allow: %s", toolLine))
		}
	}
	if pa != nil && pa.Response != nil {
		safego.Go("tui.commands.approvalAlwaysAllow", func() {
			select {
			case pa.Response <- permission.Allow:
			default:
			}
		})
	}
	m.pushTunnelApprovalResult(requestID, tunnel.DecisionAlwaysAllow)
	return nil
}

func (m *Model) handleDiffConfirm(approved bool) tea.Cmd {
	pd := m.pendingDiffConfirm
	m.pendingDiffConfirm = nil
	if pd == nil || pd.Response == nil {
		return nil
	}
	safego.Go("tui.commands.diffConfirm", func() {
		select {
		case pd.Response <- approved:
		default:
		}
	})
	if !approved {
		m.chatWriteSystem(nextSystemID(), m.t("approval.rejected"))
	}
	return nil
}

func (m *Model) handleHarnessCheckpointConfirm(approved bool) tea.Cmd {
	pc := m.pendingHarnessCheckpointConfirm
	m.pendingHarnessCheckpointConfirm = nil
	if pc == nil || pc.Response == nil {
		return nil
	}
	safego.Go("tui.commands.harnessCheckpoint", func() {
		select {
		case pc.Response <- approved:
		default:
		}
	})
	if !approved {
		m.chatWriteSystem(nextSystemID(), m.t("command.harness_cancelled"))
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

func (m *Model) handleLangCommand(parts []string) tea.Cmd {
	if len(parts) == 1 {
		m.openLanguageSelector(false)
		return nil
	}
	raw := strings.TrimSpace(parts[1])
	lang := normalizeLanguage(raw)
	if lang == LangEnglish && !strings.EqualFold(raw, "en") && !strings.EqualFold(raw, "english") {
		m.chatWriteSystem(nextSystemID(), m.t("lang.invalid", raw, supportedLanguageUsage(m.currentLanguage())))
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
			m.chatWriteSystem(nextSystemID(), err.Error())
			return
		}
	}
	m.chatWriteSystem(nextSystemID(), m.t("lang.switch", m.languageLabel()))
}

func workingDirFromModel(m *Model) string {
	dir, err := os.Getwd()
	if err != nil {
		return "."
	}
	return dir
}

func (m *Model) handleImageCommand(parts []string) tea.Cmd {
	if len(parts) < 2 {
		m.chatWriteSystem(nextSystemID(), m.t("image.usage"))
		m.chatWriteSystem(nextSystemID(), m.t("image.formats"))
		return nil
	}
	if strings.EqualFold(parts[1], "paste") {
		return m.handleClipboardPaste()
	}
	path := parts[1]
	return func() tea.Msg {
		img, err := image.ReadFile(path)
		if err != nil {
			return errMsg{err: fmt.Errorf("reading image: %w", err)}
		}
		sourcePath := path
		if absPath, err := filepath.Abs(path); err == nil {
			sourcePath = absPath
		}
		placeholder := image.Placeholder(path, img)
		return imageAttachedMsg{
			placeholder: placeholder,
			img:         img,
			filename:    filepath.Base(path),
			sourcePath:  sourcePath,
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
			// Silently ignore if clipboard doesn't contain an image.
			// This is common (user pressed Ctrl+V with text in clipboard).
			if errors.Is(err, image.ErrClipboardImageUnavailable) {
				// On Windows, show a hint since the terminal likely intercepted
				// the keypress and the clipboard may not have been checked at all.
				if runtime.GOOS == "windows" {
					return systemNotifyMsg{Text: m.t("image.clipboard_no_image_windows")}
				}
				return nil // no-op on macOS/Linux
			}
			// Use systemNotifyMsg instead of errMsg so the actual clipboard
			// error (e.g. "Install wl-clipboard or xclip") is shown as-is.
			// errMsg routes through provider.UserFacingError() which strips
			// clipboard errors to a generic "请求失败，请稍后重试" and kills
			// any active agent run via handleErrMsg.
			return systemNotifyMsg{Text: fmt.Sprintf(m.t("image.clipboard_failed"), err)}
		}
		return msg
	}
}

func (m *Model) handleRestartCommand(text string) tea.Cmd {
	// Check for "debug" argument
	parts := strings.Fields(text)
	debugMode := false
	for _, p := range parts[1:] {
		if strings.EqualFold(p, "debug") {
			debugMode = true
		}
	}
	if debugMode {
		m.restartDebug = true
	}
	m.chatWriteSystem(nextSystemID(), "Restarting ggcode...")
	if debugMode {
		m.chatWriteSystem(nextSystemID(), "  (debug mode enabled: GGCODE_DEBUG=1)")
	}
	m.quitting = true
	m.restartRequested = true
	m.shutdownAll()
	return tea.Quit
}

// buildRestartArgs reconstructs the CLI arguments needed to relaunch ggcode
// with the same configuration and resume the current session.
func (m *Model) buildRestartArgs() []string {
	var args []string

	// Preserve config file from the update service.
	if m.updateSvc != nil && m.updateSvc.ConfigPath != "" {
		args = append(args, "--config", m.updateSvc.ConfigPath)
	}

	// Resume current session.
	if m.session != nil && m.session.ID != "" {
		args = append(args, "--resume", m.session.ID)
	}

	// Preserve bypass mode.
	if m.mode == permission.BypassMode {
		args = append(args, "--bypass")
	}

	return args
}
