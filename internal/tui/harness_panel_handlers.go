package tui

import (
	"context"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/topcheer/ggcode/internal/harness"
)

func (m *Model) handleHarnessPanelKey(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	panel := m.harnessPanel
	if panel == nil {
		return *m, nil
	}
	if panel.focus == harnessPanelFocusInput {
		switch msg.String() {
		case "esc", "ctrl+c":
			m.closeHarnessPanel()
			return *m, nil
		case "shift+tab", "left", "h":
			panel.actionInput.Blur()
			if len(m.harnessPanelItems()) > 0 {
				panel.focus = harnessPanelFocusItem
			} else {
				panel.focus = harnessPanelFocusSection
			}
			return *m, m.pollHarnessPanelAutoRefresh()
		case "tab", "right", "l":
			panel.actionInput.Blur()
			panel.focus = harnessPanelFocusSection
			return *m, m.pollHarnessPanelAutoRefresh()
		case "enter":
			return *m, m.runHarnessPanelPrimaryAction()
		}
		var cmd tea.Cmd
		panel.actionInput, cmd = panel.actionInput.Update(msg)
		return *m, tea.Batch(cmd, m.pollHarnessPanelAutoRefresh())
	}
	switch msg.String() {
	case "esc", "ctrl+c":
		m.closeHarnessPanel()
		return *m, nil
	case "tab", "right", "l":
		switch {
		case len(m.harnessPanelItems()) > 0:
			panel.focus = harnessPanelFocusItem
		case harnessPanelNeedsInput(panel.selectedSection):
			panel.actionInput.Focus()
			panel.focus = harnessPanelFocusInput
		}
		return *m, m.pollHarnessPanelAutoRefresh()
	case "shift+tab", "left", "h":
		panel.focus = harnessPanelFocusSection
		return *m, m.pollHarnessPanelAutoRefresh()
	case "up", "k":
		if panel.focus == harnessPanelFocusItem {
			m.moveHarnessItem(-1)
		} else {
			m.moveHarnessSection(-1)
		}
		return *m, m.pollHarnessPanelAutoRefresh()
	case "down", "j":
		if panel.focus == harnessPanelFocusItem {
			m.moveHarnessItem(1)
		} else {
			m.moveHarnessSection(1)
		}
		return *m, m.pollHarnessPanelAutoRefresh()
	case "r":
		panel.message = ""
		if cmd := m.refreshHarnessPanel(); cmd != nil {
			return *m, tea.Batch(cmd, m.pollHarnessPanelAutoRefresh())
		}
		return *m, m.pollHarnessPanelAutoRefresh()
	case "i":
		if panel.loadErr != "" || panel.selectedSection == harnessSectionInit {
			return *m, m.runHarnessPanelPrimaryAction()
		}
		return *m, m.pollHarnessPanelAutoRefresh()
	case "enter":
		return *m, m.runHarnessPanelPrimaryAction()
	case "a":
		return *m, m.runHarnessPanelSecondaryAction("a")
	case "f":
		return *m, m.runHarnessPanelSecondaryAction("f")
	case "g":
		return *m, m.runHarnessPanelSecondaryAction("g")
	case "p":
		return *m, m.runHarnessPanelSecondaryAction("p")
	case "s":
		return *m, m.runHarnessPanelSecondaryAction("s")
	case "x":
		return *m, m.runHarnessPanelSecondaryAction("x")
	default:
		if harnessPanelNeedsInput(panel.selectedSection) && isHarnessPanelInputKey(msg) {
			panel.actionInput.Focus()
			panel.focus = harnessPanelFocusInput
			var cmd tea.Cmd
			panel.actionInput, cmd = panel.actionInput.Update(msg)
			return *m, tea.Batch(cmd, m.pollHarnessPanelAutoRefresh())
		}
		return *m, m.pollHarnessPanelAutoRefresh()
	}
}

func (m *Model) moveHarnessSection(delta int) {
	panel := m.harnessPanel
	titles := harnessSectionTitles(m.currentLanguage())
	if panel == nil || len(titles) == 0 {
		return
	}
	panel.selectedSection = (panel.selectedSection + delta + len(titles)) % len(titles)
	panel.selectedItem = 0
	m.updateHarnessPanelInputState()
	m.syncHarnessPanelSelection()
}

func (m *Model) moveHarnessItem(delta int) {
	panel := m.harnessPanel
	if panel == nil {
		return
	}
	items := m.harnessPanelItems()
	if len(items) == 0 {
		panel.focus = harnessPanelFocusSection
		return
	}
	panel.selectedItem = (panel.selectedItem + delta + len(items)) % len(items)
}

func (m *Model) pollHarnessPanelAutoRefresh() tea.Cmd {
	if !m.shouldAutoRefreshHarnessTask() {
		return nil
	}
	return tea.Tick(harnessPanelAutoRefreshInterval, func(time.Time) tea.Msg {
		return harnessPanelAutoRefreshMsg{}
	})
}

func (m *Model) shouldAutoRefreshHarnessTask() bool {
	panel := m.harnessPanel
	if panel == nil || panel.selectedSection != harnessSectionTasks {
		return false
	}
	task := m.selectedHarnessTask()
	if task == nil {
		return false
	}
	return task.Status == harness.TaskQueued || task.Status == harness.TaskRunning
}

func (m *Model) runHarnessPanelPrimaryAction() tea.Cmd {
	panel := m.harnessPanel
	if panel == nil {
		return nil
	}
	if m.harnessPanelBlockedByActiveRun() {
		panel.message = m.t("harness.message.read_only")
		return nil
	}
	if panel.loadErr != "" {
		return m.initHarnessFromPanel()
	}
	switch panel.selectedSection {
	case harnessSectionInit:
		return m.initHarnessFromPanel()
	case harnessSectionCheck:
		return m.runHarnessCheck()
	case harnessSectionMonitor:
		_ = m.refreshHarnessPanel()
		if panel := m.harnessPanel; panel != nil {
			panel.message = m.t("harness.message.monitor_refreshed")
		}
		return m.pollHarnessPanelAutoRefresh()
	case harnessSectionGC:
		return m.runHarnessGC()
	case harnessSectionQueue:
		return m.queueHarnessDraft()
	case harnessSectionRun:
		return m.runHarnessDraft()
	case harnessSectionRunQueued:
		return m.runHarnessQueued(harness.QueueRunOptions{})
	case harnessSectionTasks:
		task := m.selectedHarnessTask()
		if task == nil {
			return nil
		}
		if task.Status != harness.TaskFailed {
			panel.message = m.t("harness.message.rerun_failed_only", task.ID, task.Status)
			return nil
		}
		command := "/harness rerun " + task.ID
		project := *panel.project
		cfg := panel.cfg
		m.closeHarnessPanel()
		return m.runTrackedHarnessRerun(command, project, cfg, task)
	case harnessSectionReview:
		task := m.selectedHarnessReviewTask()
		if task == nil {
			return nil
		}
		updated, err := harness.ApproveTaskReview(*panel.project, task.ID, "")
		if err != nil {
			panel.message = err.Error()
			return nil
		}
		_ = m.refreshHarnessPanel()
		panel.message = m.t("harness.message.review_approved", updated.ID)
	case harnessSectionPromote:
		task := m.selectedHarnessPromoteTask()
		if task == nil {
			return nil
		}
		updated, err := harness.PromoteTask(context.Background(), *panel.project, task.ID, "")
		if err != nil {
			panel.message = err.Error()
			return nil
		}
		_ = m.refreshHarnessPanel()
		panel.message = m.t("harness.message.promoted", updated.ID)
	case harnessSectionRelease:
		if panel.release == nil || len(panel.release.Tasks) == 0 {
			panel.message = m.t("harness.message.no_release_tasks")
			return nil
		}
		applied, err := harness.ApplyReleasePlan(*panel.project, panel.release, "")
		if err != nil {
			panel.message = err.Error()
			return nil
		}
		_ = m.refreshHarnessPanel()
		panel.message = m.t("harness.message.release_applied", applied.BatchID)
	case harnessSectionRollouts:
		rollout := m.selectedHarnessRollout()
		if rollout == nil {
			panel.message = m.t("harness.message.no_rollouts")
			return nil
		}
		updated, err := harness.AdvanceReleaseWaveRollout(*panel.project, rollout.RolloutID)
		if err != nil {
			panel.message = err.Error()
			return nil
		}
		_ = m.refreshHarnessPanel()
		panel.message = m.t("harness.message.rollout_advanced", updated.RolloutID)
	}
	return m.pollHarnessPanelAutoRefresh()
}

func (m *Model) runHarnessPanelSecondaryAction(action string) tea.Cmd {
	panel := m.harnessPanel
	if panel == nil || panel.project == nil {
		return nil
	}
	if m.harnessPanelBlockedByActiveRun() {
		panel.message = m.t("harness.message.read_only")
		return nil
	}
	switch panel.selectedSection {
	case harnessSectionRunQueued:
		switch action {
		case "a":
			return m.runHarnessQueued(harness.QueueRunOptions{All: true})
		case "f":
			return m.runHarnessQueued(harness.QueueRunOptions{All: true, RetryFailed: true})
		case "s":
			return m.runHarnessQueued(harness.QueueRunOptions{ResumeInterrupted: true})
		}
	case harnessSectionInbox:
		entry := m.selectedHarnessInboxEntry()
		if entry == nil {
			return nil
		}
		switch action {
		case "p":
			promoted, err := harness.PromoteApprovedTasksForOwner(context.Background(), *panel.project, panel.cfg, entry.Owner, "")
			if err != nil {
				panel.message = err.Error()
				return nil
			}
			_ = m.refreshHarnessPanel()
			panel.message = m.t("harness.message.owner_promoted", len(promoted), entry.Owner)
		case "f":
			summary, err := harness.RetryFailedTasksForOwner(context.Background(), *panel.project, panel.cfg, entry.Owner, harness.BinaryRunner{})
			if err != nil {
				panel.message = err.Error()
				return nil
			}
			panel.lastQueueRun = summary
			_ = m.refreshHarnessPanel()
			panel.message = m.t("harness.message.owner_retried", entry.Owner)
		}
	case harnessSectionReview:
		if action != "x" {
			return nil
		}
		task := m.selectedHarnessReviewTask()
		if task == nil {
			return nil
		}
		updated, err := harness.RejectTaskReview(*panel.project, task.ID, "")
		if err != nil {
			panel.message = err.Error()
			return nil
		}
		_ = m.refreshHarnessPanel()
		panel.message = m.t("harness.message.review_rejected", updated.ID)
	case harnessSectionRollouts:
		rollout := m.selectedHarnessRollout()
		if rollout == nil {
			return nil
		}
		switch action {
		case "g":
			updated, err := harness.ApproveReleaseWaveGate(*panel.project, rollout.RolloutID, 0, "")
			if err != nil {
				panel.message = err.Error()
				return nil
			}
			_ = m.refreshHarnessPanel()
			panel.message = m.t("harness.message.gate_approved", updated.RolloutID)
		case "p":
			for _, group := range rollout.Groups {
				if group != nil && group.WaveStatus == harness.ReleaseWavePaused {
					updated, err := harness.ResumeReleaseWaveRollout(*panel.project, rollout.RolloutID, "")
					if err != nil {
						panel.message = err.Error()
						return nil
					}
					_ = m.refreshHarnessPanel()
					panel.message = m.t("harness.message.rollout_resumed", updated.RolloutID)
					return m.refreshHarnessPanel()
				}
			}
			updated, err := harness.PauseReleaseWaveRollout(*panel.project, rollout.RolloutID, "")
			if err != nil {
				panel.message = err.Error()
				return nil
			}
			_ = m.refreshHarnessPanel()
			panel.message = m.t("harness.message.rollout_paused", updated.RolloutID)
		case "x":
			updated, err := harness.AbortReleaseWaveRollout(*panel.project, rollout.RolloutID, "")
			if err != nil {
				panel.message = err.Error()
				return nil
			}
			_ = m.refreshHarnessPanel()
			panel.message = m.t("harness.message.rollout_aborted", updated.RolloutID)
		}
	}
	return m.refreshHarnessPanel()
}

func (m *Model) initHarnessFromPanel() tea.Cmd {
	panel := m.harnessPanel
	if panel == nil {
		return nil
	}
	return m.beginHarnessInitPrompt("/harness init", "", true)
}

func (m *Model) runHarnessCheck() tea.Cmd {
	panel := m.harnessPanel
	if panel == nil || panel.project == nil || panel.cfg == nil {
		return nil
	}
	report, err := harness.CheckProject(context.Background(), *panel.project, panel.cfg, harness.CheckOptions{RunCommands: true})
	if err != nil {
		panel.message = err.Error()
		return nil
	}
	panel.lastCheck = report
	if report.Passed {
		panel.message = m.t("harness.message.check_passed")
	} else {
		panel.message = m.t("harness.message.check_failed")
	}
	return nil
}

func (m *Model) runHarnessGC() tea.Cmd {
	panel := m.harnessPanel
	if panel == nil || panel.project == nil || panel.cfg == nil {
		return nil
	}
	report, err := harness.RunGC(*panel.project, panel.cfg, time.Now().UTC())
	if err != nil {
		panel.message = err.Error()
		return nil
	}
	panel.lastGC = report
	_ = m.refreshHarnessPanel()
	if panel := m.harnessPanel; panel != nil {
		panel.lastGC = report
		panel.message = m.t("harness.message.gc_complete")
	}
	return nil
}

func (m *Model) harnessPanelBlockedByActiveRun() bool {
	panel := m.harnessPanel
	if panel == nil {
		return false
	}
	if !m.loading && !m.projectMemoryLoading {
		return false
	}
	switch panel.selectedSection {
	case harnessSectionQueue:
		return false
	default:
		return true
	}
}

func (m *Model) queueHarnessDraft() tea.Cmd {
	panel := m.harnessPanel
	if panel == nil || panel.project == nil {
		return nil
	}
	goal := strings.TrimSpace(panel.actionInput.Value())
	if goal == "" {
		panel.message = m.t("harness.message.queue_goal_required")
		return nil
	}
	task, err := harness.EnqueueTask(*panel.project, goal, "tui")
	if err != nil {
		panel.message = err.Error()
		return nil
	}
	_ = m.refreshHarnessPanel()
	if panel := m.harnessPanel; panel != nil {
		panel.message = m.t("harness.message.queued", task.ID)
		panel.actionInput.SetValue("")
	}
	return nil
}

func (m *Model) runHarnessDraft() tea.Cmd {
	panel := m.harnessPanel
	if panel == nil || panel.project == nil || panel.cfg == nil {
		return nil
	}
	goal := strings.TrimSpace(panel.actionInput.Value())
	if goal == "" {
		panel.message = m.t("harness.message.run_goal_required")
		return nil
	}
	command := "/harness run " + goal
	project := *panel.project
	cfg := panel.cfg
	return m.beginHarnessRunPrompt(command, goal, project, cfg, true)
}

func (m *Model) runHarnessQueued(opts harness.QueueRunOptions) tea.Cmd {
	panel := m.harnessPanel
	if panel == nil || panel.project == nil || panel.cfg == nil {
		return nil
	}
	opts.ConfirmDirtyWorkspace = m.newHarnessCheckpointConfirmer()
	summary, err := harness.RunQueuedTasks(context.Background(), *panel.project, panel.cfg, nil, opts)
	if err != nil {
		panel.message = err.Error()
		return nil
	}
	panel.lastQueueRun = summary
	_ = m.refreshHarnessPanel()
	if panel := m.harnessPanel; panel != nil {
		panel.lastQueueRun = summary
		panel.message = queueRunMessage(m.currentLanguage(), summary, opts)
	}
	return nil
}
func isHarnessPanelInputKey(msg tea.KeyPressMsg) bool {
	if len(msg.Text) > 0 {
		return true
	}
	switch msg.Code {
	case tea.KeySpace, tea.KeyBackspace, tea.KeyDelete:
		return true
	default:
		return false
	}
}
