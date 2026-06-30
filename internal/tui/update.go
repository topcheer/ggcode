package tui

import (
	"context"
	"errors"
	"fmt"
	"github.com/topcheer/ggcode/internal/util"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/topcheer/ggcode/internal/update"
	"github.com/topcheer/ggcode/internal/version"
)

const updateCheckInterval = time.Hour

func (m *Model) checkForUpdateCmd() tea.Cmd {
	if m.updateSvc == nil {
		return nil
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		result, err := m.updateSvc.Check(ctx)
		return updateCheckResultMsg{Result: result, Err: err}
	}
}

func (m *Model) scheduleUpdateCheckCmd() tea.Cmd {
	if m.updateSvc == nil {
		return nil
	}
	return tea.Tick(updateCheckInterval, func(time.Time) tea.Msg {
		return updateCheckTickMsg{}
	})
}

func (m *Model) handleUpdateCommand() tea.Cmd {
	if m.updateSvc == nil {
		m.chatWriteSystem(nextSystemID(), m.t("update.unavailable"))
		return nil
	}
	m.setLoading(true)
	m.statusActivity = m.t("update.preparing")
	m.statusToolName = ""
	m.statusToolArg = ""
	m.statusToolCount = 0
	return tea.Batch(
		m.startLoadingSpinner(m.statusActivity),
		func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			resumeID := ""
			if m.session != nil {
				resumeID = strings.TrimSpace(m.session.ID)
			}
			prepared, err := m.updateSvc.Prepare(ctx, resumeID)
			return updatePrepareResultMsg{Prepared: prepared, Err: err}
		},
	)
}

func (m Model) renderSidebarUpdateSection() string {
	if !m.shouldShowUpdateSection() {
		return ""
	}
	width := max(12, m.sidebarWidth()-4)
	rows := []string{m.renderSidebarSectionTitle(m.t("panel.update"))}
	rows = append(rows, m.renderSidebarDetailRow(m.t("label.version"), version.Display(), width))
	if latest := strings.TrimSpace(m.updateInfo.LatestVersion); latest != "" {
		rows = append(rows, m.renderSidebarDetailRow(m.t("label.latest"), latest, width))
	}
	if m.updateInfo.HasUpdate {
		rows = append(rows, util.Truncate(m.t("update.sidebar_hint"), width))
	} else {
		rows = append(rows, util.Truncate(m.t("update.up_to_date"), width))
	}
	return strings.Join(rows, "\n")
}

func (m Model) shouldShowUpdateSection() bool {
	if strings.TrimSpace(version.Display()) == "" {
		return false
	}
	return m.updateInfo.HasUpdate
}

func (m Model) updateStatusSummary() string {
	if strings.TrimSpace(m.updateError) != "" {
		return m.t("update.check_failed", m.updateError)
	}
	if m.updateInfo.HasUpdate {
		return m.t("update.available", m.updateInfo.LatestVersion)
	}
	if latest := strings.TrimSpace(m.updateInfo.LatestVersion); latest != "" {
		return m.t("update.current", version.Display(), latest)
	}
	return m.t("update.unknown")
}

func (m *Model) handlePreparedUpdate(msg updatePrepareResultMsg) (tea.Model, tea.Cmd) {
	m.setLoading(false)
	m.spinner.Stop()
	m.statusActivity = ""
	m.statusToolName = ""
	m.statusToolArg = ""
	m.statusToolCount = 0
	if msg.Err != nil {
		if errors.Is(msg.Err, update.ErrAlreadyUpToDate) {
			m.chatWriteSystem(nextSystemID(), m.t("update.up_to_date"))
			return m, nil
		}
		m.chatWriteSystem(nextSystemID(), m.t("update.failed", msg.Err))
		return m, nil
	}
	// Use the unified restart helper. The helper will:
	// 1. Wait for this process to exit
	// 2. Replace the binary with the staged version
	// 3. Reset the terminal
	// 4. Launch the new ggcode
	// This avoids all orphaned-process-group and terminal-control issues.
	m.chatWriteSystem(nextSystemID(), m.t("update.restarting"))
	m.updatePrepared = &msg.Prepared
	m.restartRequested = true
	// Detect other installations and warn if they might shadow this one.
	if others := update.FindOtherInstalls(m.updateSvc.ExecPath); len(others) > 0 {
		m.chatWriteSystem(nextSystemID(), m.t("update.other_installs", update.FormatOtherInstalls(others)))
	}
	// Windows: warn if both perUser and perMachine installations exist.
	if dual := update.DetectDualScopeWindows(); dual != nil {
		m.chatWriteSystem(nextSystemID(), m.t("update.dual_scope", dual.UserPath, dual.MachinePath))
	}
	// Show package manager hint before quitting.
	if pm := update.PackageManagerHint(m.updateSvc.ExecPath); pm != "" {
		m.chatWriteSystem(nextSystemID(), m.t("update.pm_hint."+pm))
	}
	m.quitting = true
	return m, tea.Quit
}

func (m *Model) applyUpdateCheckResult(msg updateCheckResultMsg) {
	if msg.Err != nil {
		m.updateError = msg.Err.Error()
		return
	}
	m.updateError = ""
	m.updateInfo = msg.Result
}

func formatUpdateVersionLine(current, latest string) string {
	if strings.TrimSpace(latest) == "" {
		return current
	}
	return fmt.Sprintf("%s -> %s", current, latest)
}
