package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/muesli/reflow/wordwrap"

	"github.com/topcheer/ggcode/internal/harness"
)

type harnessPanelFocus int

const (
	harnessPanelFocusSection harnessPanelFocus = iota
	harnessPanelFocusItem
	harnessPanelFocusInput
)

const (
	harnessSectionInit = iota
	harnessSectionCheck
	harnessSectionDoctor
	harnessSectionMonitor
	harnessSectionGC
	harnessSectionContexts
	harnessSectionTasks
	harnessSectionQueue
	harnessSectionRun
	harnessSectionRunQueued
	harnessSectionInbox
	harnessSectionReview
	harnessSectionPromote
	harnessSectionRelease
	harnessSectionRollouts
)

type harnessPanelState struct {
	focus           harnessPanelFocus
	selectedSection int
	selectedItem    int
	message         string
	loadErr         string
	project         *harness.Project
	cfg             *harness.Config
	doctor          *harness.DoctorReport
	monitor         *harness.MonitorReport
	contexts        *harness.ContextReport
	tasks           []*harness.Task
	inbox           *harness.OwnerInbox
	review          []*harness.Task
	promote         []*harness.Task
	release         *harness.ReleasePlan
	rollouts        []*harness.ReleaseWavePlan
	lastCheck       *harness.CheckReport
	lastGC          *harness.GCReport
	lastQueueRun    *harness.RunQueueSummary
	actionInput     textinput.Model
}

const harnessPanelAutoRefreshInterval = time.Second

func harnessSectionTitles(lang Language) []string {
	return []string{
		tr(lang, "harness.section.init"),
		tr(lang, "harness.section.check"),
		tr(lang, "harness.section.doctor"),
		tr(lang, "harness.section.monitor"),
		tr(lang, "harness.section.gc"),
		tr(lang, "harness.section.contexts"),
		tr(lang, "harness.section.tasks"),
		tr(lang, "harness.section.queue"),
		tr(lang, "harness.section.run"),
		tr(lang, "harness.section.run_queued"),
		tr(lang, "harness.section.inbox"),
		tr(lang, "harness.section.review"),
		tr(lang, "harness.section.promote"),
		tr(lang, "harness.section.release"),
		tr(lang, "harness.section.rollouts"),
	}
}

func (m *Model) openHarnessPanel() {
	m.modelPanel = nil
	m.providerPanel = nil
	m.mcpPanel = nil
	m.skillsPanel = nil
	m.harnessPanel = &harnessPanelState{
		actionInput: newHarnessPanelInput(m.currentLanguage()),
	}
	m.refreshHarnessPanel()
}

func (m *Model) closeHarnessPanel() {
	m.harnessPanel = nil
}

func (m *Model) refreshHarnessPanel() {
	panel := m.harnessPanel
	if panel == nil {
		return
	}
	workDir, _ := os.Getwd()
	project, cfg, err := loadHarnessForTUI(workDir)
	if err != nil {
		panel.loadErr = err.Error()
		panel.project = nil
		panel.cfg = nil
		panel.doctor = nil
		panel.monitor = nil
		panel.contexts = nil
		panel.tasks = nil
		panel.inbox = nil
		panel.review = nil
		panel.promote = nil
		panel.release = nil
		panel.rollouts = nil
		panel.focus = harnessPanelFocusSection
		panel.selectedSection = harnessSectionInit
		panel.selectedItem = 0
		return
	}
	panel.loadErr = ""
	panel.project = &project
	panel.cfg = cfg
	panel.doctor, err = harness.Doctor(project, cfg)
	if err != nil {
		panel.loadErr = err.Error()
		return
	}
	panel.monitor, err = harness.BuildMonitorReport(project, harness.MonitorOptions{})
	if err != nil {
		panel.loadErr = err.Error()
		return
	}
	panel.contexts, err = harness.BuildContextReport(project, cfg)
	if err != nil {
		panel.loadErr = err.Error()
		return
	}
	panel.tasks, err = harness.ListTasks(project)
	if err != nil {
		panel.loadErr = err.Error()
		return
	}
	panel.inbox, err = harness.BuildOwnerInbox(project, cfg)
	if err != nil {
		panel.loadErr = err.Error()
		return
	}
	panel.review, err = harness.ListReviewableTasks(project)
	if err != nil {
		panel.loadErr = err.Error()
		return
	}
	panel.promote, err = harness.ListPromotableTasks(project)
	if err != nil {
		panel.loadErr = err.Error()
		return
	}
	panel.release, err = harness.BuildReleasePlan(project, cfg)
	if err != nil {
		panel.loadErr = err.Error()
		return
	}
	panel.rollouts, err = harness.ListReleaseWaveRollouts(project)
	if err != nil {
		panel.loadErr = err.Error()
		return
	}
	panel.selectedSection = clampHarnessIndex(panel.selectedSection, len(harnessSectionTitles(m.currentLanguage())))
	m.updateHarnessPanelInputState()
	m.syncHarnessPanelSelection()
}

func (m *Model) syncHarnessPanelSelection() {
	panel := m.harnessPanel
	if panel == nil {
		return
	}
	items := m.harnessPanelItems()
	if len(items) == 0 {
		panel.selectedItem = 0
		if panel.focus == harnessPanelFocusItem {
			if harnessPanelNeedsInput(panel.selectedSection) {
				panel.focus = harnessPanelFocusInput
			} else {
				panel.focus = harnessPanelFocusSection
			}
		}
		return
	}
	panel.selectedItem = clampHarnessIndex(panel.selectedItem, len(items))
}

func (m Model) renderHarnessPanel() string {
	panel := m.harnessPanel
	if panel == nil {
		return ""
	}
	if panel.loadErr != "" {
		body := strings.Join([]string{
			lipgloss.NewStyle().Bold(true).Render(m.t("harness.unavailable")),
			"",
			panel.loadErr,
			"",
			m.t("harness.unavailable_intro"),
			m.t("harness.unavailable_step_init"),
			m.t("harness.unavailable_step_refresh"),
			"",
			lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(m.t("harness.hints.unavailable")),
			renderHarnessPanelMessage(panel.message),
		}, "\n")
		return m.renderContextBox("/harness", strings.TrimSpace(body), lipgloss.Color("12"))
	}

	width := m.boxInnerWidth(m.mainColumnWidth())
	leftWidth := m.harnessPanelLeftWidth(width)
	rightWidth := max(36, width-leftWidth-2)
	if leftWidth+2+rightWidth > width {
		leftWidth = max(18, width-rightWidth-2)
	}
	columnHeight := 26

	leftLines := m.renderHarnessPanelNavLines(leftWidth, columnHeight)
	rightLines := m.renderHarnessPanelMainLines(rightWidth, columnHeight)
	body := joinHarnessPanelColumns(leftLines, rightLines, leftWidth, rightWidth, columnHeight)
	if footer := m.renderHarnessPanelFooterLines(width); len(footer) > 0 {
		body += "\n\n" + strings.Join(footer, "\n")
	}
	body = normalizeHarnessPanelBody(body, 35)
	return m.renderContextBox("/harness", body, lipgloss.Color("12"))
}

func renderHarnessPanelMessage(message string) string {
	message = strings.TrimSpace(message)
	if message == "" {
		return ""
	}
	return lipgloss.NewStyle().Foreground(lipgloss.Color("10")).Render(message)
}

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
		m.refreshHarnessPanel()
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
		m.refreshHarnessPanel()
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
		m.refreshHarnessPanel()
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
		m.refreshHarnessPanel()
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
		m.refreshHarnessPanel()
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
		m.refreshHarnessPanel()
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
			m.refreshHarnessPanel()
			panel.message = m.t("harness.message.owner_promoted", len(promoted), entry.Owner)
		case "f":
			summary, err := harness.RetryFailedTasksForOwner(context.Background(), *panel.project, panel.cfg, entry.Owner, harness.BinaryRunner{})
			if err != nil {
				panel.message = err.Error()
				return nil
			}
			panel.lastQueueRun = summary
			m.refreshHarnessPanel()
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
		m.refreshHarnessPanel()
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
			m.refreshHarnessPanel()
			panel.message = m.t("harness.message.gate_approved", updated.RolloutID)
		case "p":
			for _, group := range rollout.Groups {
				if group != nil && group.WaveStatus == harness.ReleaseWavePaused {
					updated, err := harness.ResumeReleaseWaveRollout(*panel.project, rollout.RolloutID, "")
					if err != nil {
						panel.message = err.Error()
						return nil
					}
					m.refreshHarnessPanel()
					panel.message = m.t("harness.message.rollout_resumed", updated.RolloutID)
					return nil
				}
			}
			updated, err := harness.PauseReleaseWaveRollout(*panel.project, rollout.RolloutID, "")
			if err != nil {
				panel.message = err.Error()
				return nil
			}
			m.refreshHarnessPanel()
			panel.message = m.t("harness.message.rollout_paused", updated.RolloutID)
		case "x":
			updated, err := harness.AbortReleaseWaveRollout(*panel.project, rollout.RolloutID, "")
			if err != nil {
				panel.message = err.Error()
				return nil
			}
			m.refreshHarnessPanel()
			panel.message = m.t("harness.message.rollout_aborted", updated.RolloutID)
		}
	}
	return nil
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
	m.refreshHarnessPanel()
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
	m.refreshHarnessPanel()
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
	m.refreshHarnessPanel()
	if panel := m.harnessPanel; panel != nil {
		panel.lastQueueRun = summary
		panel.message = queueRunMessage(m.currentLanguage(), summary, opts)
	}
	return nil
}

func (m *Model) harnessPanelItems() []string {
	panel := m.harnessPanel
	if panel == nil {
		return nil
	}
	switch panel.selectedSection {
	case harnessSectionContexts:
		if panel.contexts == nil {
			return nil
		}
		items := make([]string, 0, len(panel.contexts.Summaries))
		for _, summary := range panel.contexts.Summaries {
			label := firstNonEmptyHarness(summary.Path, summary.Name, m.t("harness.unscoped"))
			items = append(items, truncateString(fmt.Sprintf("%s • %d %s", label, summary.TaskCount, m.t("harness.tasks_count")), 52))
		}
		return items
	case harnessSectionTasks:
		items := make([]string, 0, len(panel.tasks))
		for _, task := range panel.tasks {
			if task == nil {
				continue
			}
			items = append(items, truncateString(fmt.Sprintf("%s • %s • %s", task.ID, task.Status, compactSingleLine(task.Goal)), 52))
		}
		return items
	case harnessSectionInbox:
		if panel.inbox == nil {
			return nil
		}
		items := make([]string, 0, len(panel.inbox.Entries))
		for _, entry := range panel.inbox.Entries {
			items = append(items, truncateString(fmt.Sprintf("%s • %s %d • %s %d", entry.Owner, m.t("harness.review_ready_short"), len(entry.ReviewReady), m.t("harness.promote_ready_short"), len(entry.PromotionReady)), 52))
		}
		return items
	case harnessSectionReview:
		items := make([]string, 0, len(panel.review))
		for _, task := range panel.review {
			items = append(items, truncateString(fmt.Sprintf("%s • %s", task.ID, compactSingleLine(task.Goal)), 52))
		}
		return items
	case harnessSectionPromote:
		items := make([]string, 0, len(panel.promote))
		for _, task := range panel.promote {
			items = append(items, truncateString(fmt.Sprintf("%s • %s", task.ID, compactSingleLine(task.Goal)), 52))
		}
		return items
	case harnessSectionRollouts:
		items := make([]string, 0, len(panel.rollouts))
		for _, rollout := range panel.rollouts {
			items = append(items, truncateString(fmt.Sprintf("%s • %s", rollout.RolloutID, harnessRolloutLabel(m.currentLanguage(), rollout)), 52))
		}
		return items
	default:
		return nil
	}
}

func (m Model) harnessPanelLeftWidth(totalWidth int) int {
	longest := len(m.t("harness.views"))
	for _, title := range harnessSectionTitles(m.currentLanguage()) {
		if len(title) > longest {
			longest = len(title)
		}
	}
	width := longest + 4
	width = max(16, width)
	width = minInt(20, width)
	if totalWidth > 0 {
		width = minInt(width, max(18, totalWidth-38))
	}
	return width
}

func (m *Model) renderHarnessPanelList(items []string, selected int, focused bool, width int) string {
	contentWidth := max(8, width-4)
	clipped := make([]string, 0, len(items))
	for _, item := range items {
		clipped = append(clipped, truncateString(item, contentWidth))
	}
	return m.renderProviderList(clipped, selected, focused)
}

func (m *Model) harnessPanelPreview() string {
	panel := m.harnessPanel
	if panel == nil {
		return ""
	}
	if panel.loadErr != "" {
		return m.t("harness.preview.not_initialized")
	}
	switch panel.selectedSection {
	case harnessSectionInit:
		return renderHarnessProjectSummary(m.currentLanguage(), panel.project, panel.cfg)
	case harnessSectionCheck:
		if panel.lastCheck != nil {
			return harness.FormatCheckReport(panel.lastCheck)
		}
		return m.t("harness.preview.check")
	case harnessSectionDoctor:
		return renderHarnessDoctorPreview(m.currentLanguage(), panel.doctor)
	case harnessSectionMonitor:
		return renderHarnessMonitorPreview(m.currentLanguage(), panel.project, panel.monitor)
	case harnessSectionGC:
		if panel.lastGC != nil {
			return harness.FormatGCReport(panel.lastGC)
		}
		return m.t("harness.preview.gc")
	case harnessSectionContexts:
		if summary := m.selectedHarnessContextSummary(); summary != nil {
			return renderHarnessContextSummary(m.currentLanguage(), summary)
		}
		return harness.FormatContextReport(panel.contexts)
	case harnessSectionTasks:
		if task := m.selectedHarnessTask(); task != nil {
			root := ""
			if panel.project != nil {
				root = panel.project.RootDir
			}
			return renderHarnessTask(m.currentLanguage(), task, root)
		}
		return harness.FormatTaskList(panel.tasks)
	case harnessSectionQueue:
		return renderHarnessDraftPreview(m.currentLanguage(), m.t("harness.section.queue"), panel.actionInput.Value(), m.t("harness.preview.queue_help"))
	case harnessSectionRun:
		return renderHarnessDraftPreview(m.currentLanguage(), m.t("harness.section.run"), panel.actionInput.Value(), m.t("harness.preview.run_help"))
	case harnessSectionRunQueued:
		if panel.lastQueueRun != nil {
			return harness.FormatQueueSummary(panel.lastQueueRun)
		}
		return renderHarnessRunQueuedPreview(m.currentLanguage(), panel.tasks)
	case harnessSectionInbox:
		if entry := m.selectedHarnessInboxEntry(); entry != nil {
			return renderHarnessInboxEntry(m.currentLanguage(), entry)
		}
		return harness.FormatOwnerInbox(panel.inbox)
	case harnessSectionReview:
		if task := m.selectedHarnessReviewTask(); task != nil {
			return harness.FormatReviewList([]*harness.Task{task})
		}
		return harness.FormatReviewList(panel.review)
	case harnessSectionPromote:
		if task := m.selectedHarnessPromoteTask(); task != nil {
			return harness.FormatPromotionList([]*harness.Task{task})
		}
		return harness.FormatPromotionList(panel.promote)
	case harnessSectionRelease:
		return harness.FormatReleasePlan(panel.release)
	case harnessSectionRollouts:
		if rollout := m.selectedHarnessRollout(); rollout != nil {
			return harness.FormatReleaseWavePlan(rollout)
		}
		return harness.FormatReleaseWaveRollouts(panel.rollouts)
	default:
		return ""
	}
}

func (m *Model) harnessPanelHints() string {
	panel := m.harnessPanel
	if panel == nil {
		return ""
	}
	if panel.loadErr != "" {
		return m.t("harness.hints.unavailable")
	}
	hints := []string{m.t("harness.hints.move"), m.t("harness.hints.tab"), m.t("harness.hints.refresh"), m.t("harness.hints.close")}
	switch panel.selectedSection {
	case harnessSectionCheck:
		hints = append(hints, m.t("harness.hints.check"))
	case harnessSectionMonitor:
		hints = append(hints, m.t("harness.hints.monitor"))
	case harnessSectionGC:
		hints = append(hints, m.t("harness.hints.gc"))
	case harnessSectionQueue:
		hints = append(hints, m.t("harness.hints.type_goal"), m.t("harness.hints.queue"), m.t("harness.hints.focus_input"))
	case harnessSectionRun:
		hints = append(hints, m.t("harness.hints.type_goal"), m.t("harness.hints.run"), m.t("harness.hints.focus_input"))
	case harnessSectionTasks:
		if task := m.selectedHarnessTask(); task != nil && task.Status == harness.TaskFailed {
			hints = append(hints, m.t("harness.hints.rerun"))
		}
	case harnessSectionRunQueued:
		hints = append(hints, m.t("harness.hints.next"), m.t("harness.hints.all"), m.t("harness.hints.retry_failed"), m.t("harness.hints.resume"))
	case harnessSectionInbox:
		hints = append(hints, m.t("harness.hints.promote_owner"), m.t("harness.hints.retry_owner"))
	case harnessSectionReview:
		hints = append(hints, m.t("harness.hints.approve"), m.t("harness.hints.reject"))
	case harnessSectionPromote:
		hints = append(hints, m.t("harness.hints.promote"))
	case harnessSectionRelease:
		hints = append(hints, m.t("harness.hints.apply_batch"))
	case harnessSectionRollouts:
		hints = append(hints, m.t("harness.hints.advance"), m.t("harness.hints.approve_gate"), m.t("harness.hints.pause_resume"), m.t("harness.hints.abort"))
	}
	return strings.Join(hints, " • ")
}

func (m *Model) selectedHarnessContextSummary() *harness.ContextSummary {
	panel := m.harnessPanel
	if panel == nil || panel.contexts == nil || panel.selectedItem >= len(panel.contexts.Summaries) {
		return nil
	}
	return &panel.contexts.Summaries[panel.selectedItem]
}

func (m *Model) selectedHarnessTask() *harness.Task {
	panel := m.harnessPanel
	if panel == nil || panel.selectedItem >= len(panel.tasks) {
		return nil
	}
	return panel.tasks[panel.selectedItem]
}

func (m *Model) selectedHarnessInboxEntry() *harness.OwnerInboxEntry {
	panel := m.harnessPanel
	if panel == nil || panel.inbox == nil || panel.selectedItem >= len(panel.inbox.Entries) {
		return nil
	}
	return &panel.inbox.Entries[panel.selectedItem]
}

func (m *Model) selectedHarnessReviewTask() *harness.Task {
	panel := m.harnessPanel
	if panel == nil || panel.selectedItem >= len(panel.review) {
		return nil
	}
	return panel.review[panel.selectedItem]
}

func (m *Model) selectedHarnessPromoteTask() *harness.Task {
	panel := m.harnessPanel
	if panel == nil || panel.selectedItem >= len(panel.promote) {
		return nil
	}
	return panel.promote[panel.selectedItem]
}

func (m *Model) selectedHarnessRollout() *harness.ReleaseWavePlan {
	panel := m.harnessPanel
	if panel == nil || panel.selectedItem >= len(panel.rollouts) {
		return nil
	}
	return panel.rollouts[panel.selectedItem]
}

func renderHarnessContextSummary(lang Language, summary *harness.ContextSummary) string {
	if summary == nil {
		return tr(lang, "harness.preview.no_context")
	}
	label := firstNonEmptyHarness(summary.Path, summary.Name, tr(lang, "harness.unscoped"))
	var b strings.Builder
	fmt.Fprintf(&b, "%s: %s\n", tr(lang, "harness.label.context_title"), label)
	if summary.Name != "" && summary.Name != label {
		fmt.Fprintf(&b, "%s: %s\n", tr(lang, "harness.label.name"), summary.Name)
	}
	if summary.Description != "" {
		fmt.Fprintf(&b, "%s: %s\n", tr(lang, "harness.label.description"), summary.Description)
	}
	if summary.Owner != "" {
		fmt.Fprintf(&b, "%s: %s\n", tr(lang, "harness.label.owner"), summary.Owner)
	}
	fmt.Fprintf(&b, "%s: %d\n", tr(lang, "harness.label.commands"), summary.CommandCount)
	fmt.Fprintf(&b, "%s: total=%d queued=%d running=%d blocked=%d failed=%d review_ready=%d promotion_ready=%d release_ready=%d\n",
		tr(lang, "harness.label.tasks"),
		summary.TaskCount, summary.QueuedTasks, summary.RunningTasks, summary.BlockedTasks, summary.FailedTasks, summary.ReviewReady, summary.PromotionReady, summary.ReleaseReady)
	fmt.Fprintf(&b, "%s: active=%d planned=%d paused=%d aborted=%d completed=%d\n", tr(lang, "harness.label.rollouts"),
		summary.ActiveRollouts, summary.PlannedRollouts, summary.PausedRollouts, summary.AbortedRollouts, summary.CompletedRollouts)
	fmt.Fprintf(&b, "%s: pending=%d approved=%d rejected=%d\n", tr(lang, "harness.label.gates"), summary.PendingGates, summary.ApprovedGates, summary.RejectedGates)
	if summary.LatestTask != nil {
		fmt.Fprintf(&b, "%s: %s [%s] %s\n", tr(lang, "harness.label.latest"), summary.LatestTask.ID, summary.LatestTask.Status, summary.LatestTask.Goal)
	}
	return strings.TrimRight(b.String(), "\n")
}

func renderHarnessTask(lang Language, task *harness.Task, root string) string {
	if task == nil {
		return tr(lang, "harness.preview.no_task")
	}
	var b strings.Builder
	b.WriteString(tr(lang, "harness.task_title") + "\n")
	fmt.Fprintf(&b, "%s: %s\n", tr(lang, "harness.label.id"), task.ID)
	fmt.Fprintf(&b, "%s: %s\n", tr(lang, "harness.label.status"), task.Status)
	if task.Goal != "" {
		b.WriteString(tr(lang, "harness.label.goal") + ":\n")
		fmt.Fprintf(&b, "  %s\n", strings.TrimSpace(task.Goal))
	}
	if task.Attempt > 0 {
		fmt.Fprintf(&b, "%s: %d\n", tr(lang, "harness.label.attempts"), task.Attempt)
	}
	if len(task.DependsOn) > 0 {
		fmt.Fprintf(&b, "%s: %s\n", tr(lang, "harness.label.depends_on"), strings.Join(task.DependsOn, ", "))
	}
	if task.ContextName != "" || task.ContextPath != "" {
		label := firstNonEmptyHarness(task.ContextName, harnessPanelPathLabel(root, task.ContextPath))
		fmt.Fprintf(&b, "%s: %s\n", tr(lang, "harness.label.context"), label)
	}
	if task.WorkspacePath != "" {
		fmt.Fprintf(&b, "%s: %s\n", tr(lang, "harness.label.workspace"), harnessPanelPathLabel(root, task.WorkspacePath))
	}
	if task.BranchName != "" {
		fmt.Fprintf(&b, "%s: %s\n", tr(lang, "harness.label.branch"), task.BranchName)
	}
	if task.WorkerID != "" {
		status := firstNonEmptyHarness(task.WorkerStatus, tr(lang, "harness.unknown"))
		if strings.TrimSpace(task.WorkerPhase) != "" && task.WorkerPhase != status {
			fmt.Fprintf(&b, "%s: %s [%s, %s]\n", tr(lang, "harness.label.worker"), task.WorkerID, status, task.WorkerPhase)
		} else {
			fmt.Fprintf(&b, "%s: %s [%s]\n", tr(lang, "harness.label.worker"), task.WorkerID, status)
		}
	}
	if task.WorkerProgress != "" {
		fmt.Fprintf(&b, "%s: %s\n", tr(lang, "harness.label.progress"), compactSingleLine(task.WorkerProgress))
	}
	if task.VerificationStatus != "" {
		fmt.Fprintf(&b, "%s: %s\n", tr(lang, "harness.label.verification"), task.VerificationStatus)
	}
	if len(task.ChangedFiles) > 0 {
		fmt.Fprintf(&b, "%s: %d\n", tr(lang, "harness.label.changed_files"), len(task.ChangedFiles))
	}
	if task.VerificationReportPath != "" {
		fmt.Fprintf(&b, "%s: %s\n", tr(lang, "harness.label.delivery_report"), harnessPanelPathLabel(root, task.VerificationReportPath))
	}
	if task.LogPath != "" {
		fmt.Fprintf(&b, "%s: %s\n", tr(lang, "harness.label.log"), harnessPanelPathLabel(root, task.LogPath))
	}
	if task.ReviewStatus != "" {
		fmt.Fprintf(&b, "%s: %s\n", tr(lang, "harness.label.review"), task.ReviewStatus)
	}
	if task.ReviewNotes != "" {
		fmt.Fprintf(&b, "%s: %s\n", tr(lang, "harness.label.review_notes"), compactSingleLine(task.ReviewNotes))
	}
	if task.PromotionStatus != "" {
		fmt.Fprintf(&b, "%s: %s\n", tr(lang, "harness.label.promotion"), task.PromotionStatus)
	}
	if task.PromotionNotes != "" {
		fmt.Fprintf(&b, "%s: %s\n", tr(lang, "harness.label.promotion_notes"), compactSingleLine(task.PromotionNotes))
	}
	if task.ReleaseBatchID != "" {
		fmt.Fprintf(&b, "%s: %s\n", tr(lang, "harness.label.release_batch"), task.ReleaseBatchID)
	}
	if task.ReleaseNotes != "" {
		fmt.Fprintf(&b, "%s: %s\n", tr(lang, "harness.label.release_notes"), compactSingleLine(task.ReleaseNotes))
	}
	if task.Error != "" {
		fmt.Fprintf(&b, "%s: %s\n", tr(lang, "harness.label.error"), compactSingleLine(task.Error))
	}
	return strings.TrimRight(b.String(), "\n")
}

func renderHarnessProjectSummary(lang Language, project *harness.Project, cfg *harness.Config) string {
	if project == nil {
		return tr(lang, "harness.preview.project_not_initialized")
	}
	var b strings.Builder
	b.WriteString(tr(lang, "harness.preview.project_initialized") + "\n")
	fmt.Fprintf(&b, "\n%s: %s\n", tr(lang, "harness.label.repo"), harnessPanelPathLabel(project.RootDir, project.RootDir))
	fmt.Fprintf(&b, "%s: %s\n", tr(lang, "harness.label.config"), harnessPanelPathLabel(project.RootDir, project.ConfigPath))
	if cfg != nil && strings.TrimSpace(cfg.Project.Name) != "" {
		fmt.Fprintf(&b, "%s: %s\n", tr(lang, "harness.label.project"), cfg.Project.Name)
	}
	b.WriteString("\n" + tr(lang, "harness.preview.project_help"))
	return strings.TrimRight(b.String(), "\n")
}

func renderHarnessDoctorPreview(lang Language, report *harness.DoctorReport) string {
	if report == nil {
		return tr(lang, "harness.preview.no_doctor")
	}
	var b strings.Builder
	b.WriteString(tr(lang, "harness.doctor_title") + "\n")
	fmt.Fprintf(&b, "- %s: %s\n", tr(lang, "harness.label.repo"), harnessPanelPathLabel(report.Project.RootDir, report.Project.RootDir))
	fmt.Fprintf(&b, "- %s: %s\n", tr(lang, "harness.label.config"), harnessPanelPathLabel(report.Project.RootDir, report.Project.ConfigPath))
	if report.Config != nil && strings.TrimSpace(report.Config.Project.Name) != "" {
		fmt.Fprintf(&b, "- %s: %s\n", tr(lang, "harness.label.project"), report.Config.Project.Name)
	}
	if report.Structural != nil {
		status := tr(lang, "harness.status.ok")
		if !report.Structural.Passed {
			status = tr(lang, "harness.status.needs_attention")
		}
		fmt.Fprintf(&b, "- %s: %s\n", tr(lang, "harness.label.structure"), status)
	}
	if report.Contexts > 0 {
		fmt.Fprintf(&b, "- %s: %d\n", tr(lang, "harness.label.contexts"), report.Contexts)
	}
	fmt.Fprintf(&b, "- %s: total=%d running=%d blocked=%d failed=%d\n", tr(lang, "harness.label.tasks"), report.TotalTasks, report.RunningTasks, report.BlockedTasks, report.FailedTasks)
	fmt.Fprintf(&b, "- %s: backed=%d drift=%d\n", tr(lang, "harness.label.workers"), report.WorkerTasks, report.WorkerDrift)
	fmt.Fprintf(&b, "- %s: review_ready=%d promotion_ready=%d release_ready=%d retryable=%d\n", tr(lang, "harness.label.workflow"), report.ReviewReady, report.PromotionReady, report.ReleaseReady, report.Retryable)
	fmt.Fprintf(&b, "- %s: verification_failed=%d stale_blocked=%d\n", tr(lang, "harness.label.quality"), report.VerificationFailed, report.StaleBlocked)
	fmt.Fprintf(&b, "- %s: orphaned=%d\n", tr(lang, "harness.label.worktrees"), report.OrphanedWorktrees)
	if report.Rollouts > 0 {
		fmt.Fprintf(&b, "- %s: total=%d active=%d planned=%d paused=%d aborted=%d completed=%d\n", tr(lang, "harness.label.rollouts"), report.Rollouts, report.ActiveRollouts, report.PlannedRollouts, report.PausedRollouts, report.AbortedRollouts, report.CompletedRollouts)
		fmt.Fprintf(&b, "- %s: pending=%d approved=%d rejected=%d\n", tr(lang, "harness.label.gates"), report.PendingGates, report.ApprovedGates, report.RejectedGates)
	}
	if report.LastTask != nil {
		fmt.Fprintf(&b, "\n%s\n- %s [%s]\n", tr(lang, "harness.latest_task"), report.LastTask.ID, report.LastTask.Status)
		if label := firstNonEmptyHarness(report.LastTask.ContextName, report.LastTask.ContextPath); label != "" {
			fmt.Fprintf(&b, "- %s: %s\n", tr(lang, "harness.label.context"), label)
		}
		if report.LastTask.WorkerID != "" {
			fmt.Fprintf(&b, "- %s: %s [%s]\n", tr(lang, "harness.label.worker"), report.LastTask.WorkerID, report.LastTask.WorkerStatus)
		}
		if report.LastTask.WorkerProgress != "" {
			fmt.Fprintf(&b, "- %s: %s\n", tr(lang, "harness.label.progress"), compactSingleLine(report.LastTask.WorkerProgress))
		}
		if report.LastTask.VerificationStatus != "" {
			fmt.Fprintf(&b, "- %s: %s\n", tr(lang, "harness.label.verification"), report.LastTask.VerificationStatus)
		}
		if report.LastTask.ReviewStatus != "" {
			fmt.Fprintf(&b, "- %s: %s\n", tr(lang, "harness.label.review"), report.LastTask.ReviewStatus)
		}
		if report.LastTask.PromotionStatus != "" {
			fmt.Fprintf(&b, "- %s: %s\n", tr(lang, "harness.label.promotion"), report.LastTask.PromotionStatus)
		}
		if report.LastTask.ReleaseBatchID != "" {
			fmt.Fprintf(&b, "- %s: %s\n", tr(lang, "harness.label.release_batch_human"), report.LastTask.ReleaseBatchID)
		}
		if report.LastTask.LogPath != "" {
			fmt.Fprintf(&b, "- %s: %s\n", tr(lang, "harness.label.log"), harnessPanelPathLabel(report.Project.RootDir, report.LastTask.LogPath))
		}
		if report.LastTask.VerificationReportPath != "" {
			fmt.Fprintf(&b, "- %s: %s\n", tr(lang, "harness.label.delivery_report_human"), harnessPanelPathLabel(report.Project.RootDir, report.LastTask.VerificationReportPath))
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func renderHarnessMonitorPreview(lang Language, project *harness.Project, report *harness.MonitorReport) string {
	if report == nil {
		return tr(lang, "harness.preview.monitor_unavailable")
	}
	root := ""
	if project != nil {
		root = project.RootDir
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n- %s: %s\n- %s: %s\n", tr(lang, "harness.monitor_title"), tr(lang, "harness.label.snapshot"), harnessPanelPathLabel(root, report.SnapshotPath), tr(lang, "harness.label.events"), harnessPanelPathLabel(root, report.EventLogPath))
	fmt.Fprintf(&b, "- %s: total=%d queued=%d running=%d blocked=%d failed=%d\n", tr(lang, "harness.label.tasks"),
		report.TaskTotals.Total, report.TaskTotals.Queued, report.TaskTotals.Running, report.TaskTotals.Blocked, report.TaskTotals.Failed)
	fmt.Fprintf(&b, "- %s: review_pending=%d promotion_ready=%d released=%d active_workers=%d\n", tr(lang, "harness.label.workflow"),
		report.TaskTotals.ReviewPending, report.TaskTotals.PromotionReady, report.TaskTotals.Released, report.TaskTotals.ActiveWorkers)
	fmt.Fprintf(&b, "- %s: batches=%d active=%d planned=%d paused=%d aborted=%d completed=%d\n", tr(lang, "harness.label.rollouts"),
		report.RolloutTotals.Batches, report.RolloutTotals.Active, report.RolloutTotals.Planned, report.RolloutTotals.Paused, report.RolloutTotals.Aborted, report.RolloutTotals.Completed)
	fmt.Fprintf(&b, "- %s: pending=%d approved=%d rejected=%d\n", tr(lang, "harness.label.gates"),
		report.RolloutTotals.GatesPending, report.RolloutTotals.GatesApproved, report.RolloutTotals.GatesRejected)
	if len(report.FocusTasks) > 0 {
		task := report.FocusTasks[0]
		fmt.Fprintf(&b, "\n%s\n- %s [%s]\n", tr(lang, "harness.focus"), task.ID, firstNonEmptyHarness(task.Status, tr(lang, "harness.unknown")))
		if task.Goal != "" {
			fmt.Fprintf(&b, "- %s: %s\n", tr(lang, "harness.label.goal"), compactSingleLine(task.Goal))
		}
		if context := firstNonEmptyHarness(task.ContextPath, task.ContextName); context != "" {
			fmt.Fprintf(&b, "- %s: %s\n", tr(lang, "harness.label.context"), context)
		}
	}
	if len(report.RecentEvents) > 0 {
		event := report.RecentEvents[0]
		fmt.Fprintf(&b, "\n%s\n- %s %s\n", tr(lang, "harness.latest_event"), event.RecordedAt.Format("15:04:05"), event.Kind)
		if summary := strings.TrimSpace(event.Summary); summary != "" {
			fmt.Fprintf(&b, "- %s\n", compactSingleLine(summary))
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func (m Model) renderHarnessPanelNavLines(width, height int) []string {
	panel := m.harnessPanel
	lines := []string{m.t("harness.views"), ""}
	if panel == nil {
		return normalizeHarnessPanelLines(lines, height)
	}
	lines = append(lines, renderHarnessPlainList(harnessSectionTitles(m.currentLanguage()), panel.selectedSection, panel.focus == harnessPanelFocusSection, width, max(1, height-len(lines)), m.currentLanguage())...)
	return normalizeHarnessPanelLines(lines, height)
}

func (m Model) renderHarnessPanelMainLines(width, height int) []string {
	panel := m.harnessPanel
	if panel == nil {
		return normalizeHarnessPanelLines(nil, height)
	}
	lines := []string{harnessSectionTitles(m.currentLanguage())[panel.selectedSection], ""}
	items := m.harnessPanelItems()
	if len(items) > 0 {
		lines = append(lines, m.t("harness.items"))
		lines = append(lines, renderHarnessPlainList(items, panel.selectedItem, panel.focus == harnessPanelFocusItem, width, 6, m.currentLanguage())...)
		lines = append(lines, "")
	}
	lines = append(lines, m.t("harness.action"))
	lines = append(lines, m.renderHarnessActionLines(width)...)
	lines = append(lines, "", m.t("harness.details"))

	remaining := height - len(lines)
	if remaining < 4 {
		remaining = 4
	}
	lines = append(lines, wrapHarnessPanelText(m.harnessPanelPreview(), width, remaining)...)
	return normalizeHarnessPanelLines(lines, height)
}

func (m Model) renderHarnessActionLines(width int) []string {
	panel := m.harnessPanel
	if panel == nil {
		return nil
	}
	if harnessPanelNeedsInput(panel.selectedSection) {
		return []string{
			renderHarnessPanelInput(panel.actionInput, panel.focus == harnessPanelFocusInput, width),
			lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(truncateString(harnessPanelPrimaryHint(panel.selectedSection, m.currentLanguage()), width)),
		}
	}
	return []string{
		lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(truncateString(harnessPanelPrimaryHint(panel.selectedSection, m.currentLanguage()), width)),
	}
}

func (m Model) renderHarnessPanelFooterLines(width int) []string {
	panel := m.harnessPanel
	if panel == nil {
		return nil
	}
	lines := make([]string, 0, 2)
	if msg := renderHarnessPanelMessage(panel.message); msg != "" {
		lines = append(lines, msg)
	}
	if hints := strings.TrimSpace(m.harnessPanelHints()); hints != "" {
		lines = append(lines, lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(truncateString(hints, width)))
	}
	return lines
}

func renderHarnessPlainList(items []string, selected int, focused bool, width, maxLines int, lang Language) []string {
	if len(items) == 0 || maxLines <= 0 {
		return []string{"  " + tr(lang, "harness.none")}
	}
	start, end := harnessPanelListWindow(len(items), selected, maxLines)
	lines := make([]string, 0, end-start)
	for i := start; i < end; i++ {
		prefix := "  "
		if i == selected {
			if focused {
				prefix = "> "
			} else {
				prefix = "* "
			}
		}
		lines = append(lines, prefix+truncateString(items[i], max(1, width-len(prefix))))
	}
	return lines
}

func harnessPanelListWindow(size, selected, maxLines int) (int, int) {
	if size <= maxLines {
		return 0, size
	}
	start := max(0, selected-maxLines/2)
	end := start + maxLines
	if end > size {
		end = size
		start = end - maxLines
	}
	return start, end
}

func joinHarnessPanelColumns(leftLines, rightLines []string, leftWidth, rightWidth, height int) string {
	leftLines = normalizeHarnessPanelLines(leftLines, height)
	rightLines = normalizeHarnessPanelLines(rightLines, height)
	rows := make([]string, 0, height)
	for i := 0; i < height; i++ {
		rows = append(rows, padHarnessPanelLine(leftLines[i], leftWidth)+"  "+padHarnessPanelLine(rightLines[i], rightWidth))
	}
	return strings.Join(rows, "\n")
}

func padHarnessPanelLine(line string, width int) string {
	visible := lipgloss.Width(line)
	if visible >= width {
		return line
	}
	return line + strings.Repeat(" ", width-visible)
}

func normalizeHarnessPanelLines(lines []string, height int) []string {
	if len(lines) > height {
		return lines[:height]
	}
	for len(lines) < height {
		lines = append(lines, "")
	}
	return lines
}

func wrapHarnessPanelText(content string, width, maxLines int) []string {
	if maxLines <= 0 {
		return nil
	}
	if width <= 0 {
		width = 1
	}
	content = strings.TrimSpace(content)
	if content == "" {
		return []string{""}
	}
	var lines []string
	for _, raw := range strings.Split(content, "\n") {
		line := strings.TrimRight(raw, " ")
		if strings.TrimSpace(line) == "" {
			lines = append(lines, "")
			continue
		}
		wrapped := wordwrap.String(line, width)
		for _, candidate := range strings.Split(wrapped, "\n") {
			lines = append(lines, hardWrapHarnessPanelLine(candidate, width)...)
		}
		if len(lines) >= maxLines {
			return lines[:maxLines]
		}
	}
	if len(lines) > maxLines {
		lines = lines[:maxLines]
	}
	return lines
}

func hardWrapHarnessPanelLine(line string, width int) []string {
	line = strings.TrimRight(line, " ")
	if line == "" {
		return []string{""}
	}
	if width <= 0 {
		return []string{line}
	}
	var out []string
	remaining := line
	for remaining != "" {
		if lipgloss.Width(remaining) <= width {
			out = append(out, remaining)
			break
		}
		cut := 0
		currentWidth := 0
		for i, r := range remaining {
			rw := lipgloss.Width(string(r))
			if currentWidth+rw > width {
				break
			}
			currentWidth += rw
			cut = i + len(string(r))
		}
		if cut <= 0 {
			break
		}
		out = append(out, strings.TrimRight(remaining[:cut], " "))
		remaining = strings.TrimLeft(remaining[cut:], " ")
	}
	if len(out) == 0 {
		return []string{line}
	}
	return out
}

func harnessPanelPrimaryHint(section int, lang Language) string {
	switch section {
	case harnessSectionCheck:
		return tr(lang, "harness.hint.primary.check")
	case harnessSectionMonitor:
		return tr(lang, "harness.hint.primary.monitor")
	case harnessSectionGC:
		return tr(lang, "harness.hint.primary.gc")
	case harnessSectionQueue:
		return tr(lang, "harness.hint.primary.queue")
	case harnessSectionRun:
		return tr(lang, "harness.hint.primary.run")
	case harnessSectionTasks:
		return tr(lang, "harness.hint.primary.tasks")
	case harnessSectionRunQueued:
		return tr(lang, "harness.hint.primary.run_queued")
	case harnessSectionInbox:
		return tr(lang, "harness.hint.primary.inbox")
	case harnessSectionReview:
		return tr(lang, "harness.hint.primary.review")
	case harnessSectionPromote:
		return tr(lang, "harness.hint.primary.promote")
	case harnessSectionRelease:
		return tr(lang, "harness.hint.primary.release")
	case harnessSectionRollouts:
		return tr(lang, "harness.hint.primary.rollouts")
	default:
		return tr(lang, "harness.hint.primary.none")
	}
}

func renderHarnessDraftPreview(lang Language, label, draft, help string) string {
	draft = strings.TrimSpace(draft)
	if draft == "" {
		draft = tr(lang, "harness.input_empty")
	}
	return fmt.Sprintf("%s %s:\n%s\n\n%s", label, tr(lang, "harness.label.target"), draft, help)
}

func renderHarnessRunQueuedPreview(lang Language, tasks []*harness.Task) string {
	var queued, running, blocked, failed int
	for _, task := range tasks {
		if task == nil {
			continue
		}
		switch task.Status {
		case harness.TaskQueued:
			queued++
		case harness.TaskRunning:
			running++
		case harness.TaskBlocked:
			blocked++
		case harness.TaskFailed:
			failed++
		}
	}
	return tr(lang, "harness.preview.run_queued", queued, running, blocked, failed)
}

func renderHarnessInboxEntry(lang Language, entry *harness.OwnerInboxEntry) string {
	if entry == nil {
		return tr(lang, "harness.preview.no_owner")
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s: %s\n", tr(lang, "harness.label.owner_title"), entry.Owner)
	fmt.Fprintf(&b, "%s: %d\n", tr(lang, "harness.label.review_ready"), len(entry.ReviewReady))
	fmt.Fprintf(&b, "%s: %d\n", tr(lang, "harness.label.promotion_ready"), len(entry.PromotionReady))
	fmt.Fprintf(&b, "%s: %d\n", tr(lang, "harness.label.retryable"), len(entry.Retryable))
	fmt.Fprintf(&b, "%s: active=%d planned=%d paused=%d aborted=%d completed=%d\n", tr(lang, "harness.label.rollouts"),
		entry.ActiveRollouts, entry.PlannedRollouts, entry.PausedRollouts, entry.AbortedRollouts, entry.CompletedRollouts)
	fmt.Fprintf(&b, "%s: pending=%d approved=%d rejected=%d\n", tr(lang, "harness.label.gates"), entry.PendingGates, entry.ApprovedGates, entry.RejectedGates)
	appendHarnessTaskGroup(&b, tr(lang, "harness.group.review"), entry.ReviewReady)
	appendHarnessTaskGroup(&b, tr(lang, "harness.group.promotion"), entry.PromotionReady)
	appendHarnessTaskGroup(&b, tr(lang, "harness.group.retry"), entry.Retryable)
	return strings.TrimRight(b.String(), "\n")
}

func appendHarnessTaskGroup(b *strings.Builder, label string, tasks []*harness.Task) {
	if b == nil || len(tasks) == 0 {
		return
	}
	for _, task := range tasks {
		if task == nil {
			continue
		}
		fmt.Fprintf(b, "%s: %s %s\n", label, task.ID, task.Goal)
	}
}

func harnessRolloutLabel(lang Language, rollout *harness.ReleaseWavePlan) string {
	if rollout == nil || len(rollout.Groups) == 0 {
		return tr(lang, "harness.no_waves")
	}
	counts := map[string]int{}
	for _, group := range rollout.Groups {
		if group == nil {
			continue
		}
		counts[group.WaveStatus]++
	}
	for _, status := range []string{harness.ReleaseWaveActive, harness.ReleaseWavePaused, harness.ReleaseWavePlanned, harness.ReleaseWaveCompleted, harness.ReleaseWaveAborted} {
		if counts[status] > 0 {
			return fmt.Sprintf("%s x%d", status, counts[status])
		}
	}
	return tr(lang, "harness.mixed")
}

func queueRunMessage(lang Language, summary *harness.RunQueueSummary, opts harness.QueueRunOptions) string {
	if summary == nil {
		return tr(lang, "harness.message.no_queued_executed")
	}
	switch {
	case opts.RetryFailed:
		return tr(lang, "harness.message.queue_retried", len(summary.Executed))
	case opts.ResumeInterrupted:
		return tr(lang, "harness.message.queue_resumed", len(summary.Executed))
	case opts.All:
		return tr(lang, "harness.message.queue_ran", len(summary.Executed))
	default:
		return tr(lang, "harness.message.queue_ran", len(summary.Executed))
	}
}

func newHarnessPanelInput(lang Language) textinput.Model {
	ti := textinput.New()
	ti.Prompt = "  "
	ti.Placeholder = harnessPanelInputPlaceholder(harnessSectionQueue, lang)
	ti.CharLimit = 240
	ti.SetWidth(52)
	ti.Blur()
	return ti
}

func (m *Model) updateHarnessPanelInputState() {
	panel := m.harnessPanel
	if panel == nil {
		return
	}
	panel.actionInput.Placeholder = harnessPanelInputPlaceholder(panel.selectedSection, m.currentLanguage())
	if harnessPanelNeedsInput(panel.selectedSection) {
		return
	}
	panel.actionInput.Blur()
	if panel.focus == harnessPanelFocusInput {
		if len(m.harnessPanelItems()) > 0 {
			panel.focus = harnessPanelFocusItem
		} else {
			panel.focus = harnessPanelFocusSection
		}
	}
}

func harnessPanelNeedsInput(section int) bool {
	switch section {
	case harnessSectionQueue, harnessSectionRun:
		return true
	default:
		return false
	}
}

func harnessPanelInputPlaceholder(section int, lang Language) string {
	switch lang {
	case LangZhCN:
		switch section {
		case harnessSectionRun:
			return "输入要运行的 harness 目标，例如：修复 inventory 同步失败"
		default:
			return "输入要排队的 harness 目标，例如：给 billing 模块补回归测试"
		}
	default:
		switch section {
		case harnessSectionRun:
			return "Describe the harness run goal, e.g. fix inventory sync failures"
		default:
			return "Describe the queued harness goal, e.g. add billing regression tests"
		}
	}
}

func renderHarnessPanelInput(input textinput.Model, focused bool, width int) string {
	control := input
	control.SetWidth(max(20, width-2))
	if focused {
		control.Focus()
	} else {
		control.Blur()
	}
	return control.View()
}

func renderHarnessActionInputBox(section int, input textinput.Model, focused bool, width int, lang Language) string {
	if harnessPanelNeedsInput(section) {
		return lipgloss.NewStyle().
			Width(width).
			Height(3).
			Render(renderHarnessPanelInput(input, focused, width))
	}
	return lipgloss.NewStyle().
		Width(width).
		Height(3).
		Foreground(lipgloss.Color("8")).
		Render(clipHarnessPanelText(tr(lang, "harness.hint.primary.none"), width, 3))
}

func renderHarnessPreviewBox(content string, width, height int) string {
	content = clipHarnessPanelText(content, width, height)
	return lipgloss.NewStyle().
		Width(width).
		Height(height).
		Render(content)
}

func renderHarnessHintsBox(content string, width, height int) string {
	content = clipHarnessPanelText(content, width, height)
	return lipgloss.NewStyle().
		Width(width).
		Height(height).
		Foreground(lipgloss.Color("8")).
		Render(content)
}

func clipHarnessPanelText(content string, width, height int) string {
	if width <= 0 {
		width = 1
	}
	content = truncateLines(content, height)
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		lines[i] = truncateString(line, width)
	}
	return strings.Join(lines, "\n")
}

func harnessPanelPathLabel(root, path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if root = strings.TrimSpace(root); root != "" {
		if filepath.Clean(path) == filepath.Clean(root) {
			return filepath.Base(root)
		}
		if rel, err := filepath.Rel(root, path); err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return filepath.ToSlash(rel)
		}
	}
	return filepath.Base(path)
}

func normalizeHarnessPanelBody(body string, height int) string {
	lines := strings.Split(body, "\n")
	if len(lines) > height {
		lines = lines[:height]
	}
	for len(lines) < height {
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n")
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

func firstNonEmptyHarness(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func clampHarnessIndex(value, size int) int {
	if size <= 0 {
		return 0
	}
	if value < 0 {
		return 0
	}
	if value >= size {
		return size - 1
	}
	return value
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
