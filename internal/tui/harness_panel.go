package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
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

var harnessSectionTitles = []string{
	"Init",
	"Check",
	"Doctor",
	"Monitor",
	"GC",
	"Contexts",
	"Tasks",
	"Queue",
	"Run",
	"Run queued",
	"Inbox",
	"Review",
	"Promote",
	"Release",
	"Rollouts",
}

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
	panel.selectedSection = clampHarnessIndex(panel.selectedSection, len(harnessSectionTitles))
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
			lipgloss.NewStyle().Bold(true).Render("Harness unavailable"),
			"",
			panel.loadErr,
			"",
			"Start here in an existing project:",
			"  1. Press Enter or i to initialize harness",
			"  2. Press r to refresh once init finishes",
			"",
			lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render("Enter/i init harness • r refresh • Esc close"),
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

func (m *Model) handleHarnessPanelKey(msg tea.KeyMsg) (Model, tea.Cmd) {
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
	if panel == nil || len(harnessSectionTitles) == 0 {
		return
	}
	panel.selectedSection = (panel.selectedSection + delta + len(harnessSectionTitles)) % len(harnessSectionTitles)
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
	if panel.loadErr != "" {
		return m.initHarnessFromPanel()
	}
	switch panel.selectedSection {
	case harnessSectionInit:
		panel.message = "Harness is already initialized."
		return nil
	case harnessSectionCheck:
		return m.runHarnessCheck()
	case harnessSectionMonitor:
		m.refreshHarnessPanel()
		if panel := m.harnessPanel; panel != nil {
			panel.message = "Harness monitor refreshed."
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
			panel.message = fmt.Sprintf("Harness task %s is %s; only failed tasks can be rerun.", task.ID, task.Status)
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
		panel.message = fmt.Sprintf("Approved review for %s", updated.ID)
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
		panel.message = fmt.Sprintf("Promoted %s", updated.ID)
	case harnessSectionRelease:
		if panel.release == nil || len(panel.release.Tasks) == 0 {
			panel.message = "No harness tasks are ready for release."
			return nil
		}
		applied, err := harness.ApplyReleasePlan(*panel.project, panel.release, "")
		if err != nil {
			panel.message = err.Error()
			return nil
		}
		m.refreshHarnessPanel()
		panel.message = fmt.Sprintf("Applied release batch %s", applied.BatchID)
	case harnessSectionRollouts:
		rollout := m.selectedHarnessRollout()
		if rollout == nil {
			panel.message = "No persisted rollouts found."
			return nil
		}
		updated, err := harness.AdvanceReleaseWaveRollout(*panel.project, rollout.RolloutID)
		if err != nil {
			panel.message = err.Error()
			return nil
		}
		m.refreshHarnessPanel()
		panel.message = fmt.Sprintf("Advanced rollout %s", updated.RolloutID)
	}
	return m.pollHarnessPanelAutoRefresh()
}

func (m *Model) runHarnessPanelSecondaryAction(action string) tea.Cmd {
	panel := m.harnessPanel
	if panel == nil || panel.project == nil {
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
			panel.message = fmt.Sprintf("Promoted %d task(s) for %s", len(promoted), entry.Owner)
		case "f":
			summary, err := harness.RetryFailedTasksForOwner(context.Background(), *panel.project, panel.cfg, entry.Owner, harness.BinaryRunner{})
			if err != nil {
				panel.message = err.Error()
				return nil
			}
			panel.lastQueueRun = summary
			m.refreshHarnessPanel()
			panel.message = fmt.Sprintf("Retried failed tasks for %s", entry.Owner)
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
		panel.message = fmt.Sprintf("Rejected review for %s", updated.ID)
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
			panel.message = fmt.Sprintf("Approved next gate for %s", updated.RolloutID)
		case "p":
			for _, group := range rollout.Groups {
				if group != nil && group.WaveStatus == harness.ReleaseWavePaused {
					updated, err := harness.ResumeReleaseWaveRollout(*panel.project, rollout.RolloutID, "")
					if err != nil {
						panel.message = err.Error()
						return nil
					}
					m.refreshHarnessPanel()
					panel.message = fmt.Sprintf("Resumed rollout %s", updated.RolloutID)
					return nil
				}
			}
			updated, err := harness.PauseReleaseWaveRollout(*panel.project, rollout.RolloutID, "")
			if err != nil {
				panel.message = err.Error()
				return nil
			}
			m.refreshHarnessPanel()
			panel.message = fmt.Sprintf("Paused rollout %s", updated.RolloutID)
		case "x":
			updated, err := harness.AbortReleaseWaveRollout(*panel.project, rollout.RolloutID, "")
			if err != nil {
				panel.message = err.Error()
				return nil
			}
			m.refreshHarnessPanel()
			panel.message = fmt.Sprintf("Aborted rollout %s", updated.RolloutID)
		}
	}
	return nil
}

func (m *Model) initHarnessFromPanel() tea.Cmd {
	panel := m.harnessPanel
	if panel == nil {
		return nil
	}
	workDir, _ := os.Getwd()
	result, err := harness.Init(workDir, harness.InitOptions{})
	if err != nil {
		panel.message = err.Error()
		return nil
	}
	m.refreshHarnessPanel()
	if panel := m.harnessPanel; panel != nil {
		panel.message = fmt.Sprintf("Initialized harness in %s", result.Project.RootDir)
	}
	return nil
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
		panel.message = "Harness check passed."
	} else {
		panel.message = "Harness check found issues."
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
		panel.message = "Harness gc complete."
	}
	return nil
}

func (m *Model) queueHarnessDraft() tea.Cmd {
	panel := m.harnessPanel
	if panel == nil || panel.project == nil {
		return nil
	}
	goal := strings.TrimSpace(panel.actionInput.Value())
	if goal == "" {
		panel.message = "Type a queue goal in the panel input first."
		return nil
	}
	task, err := harness.EnqueueTask(*panel.project, goal, "tui")
	if err != nil {
		panel.message = err.Error()
		return nil
	}
	m.refreshHarnessPanel()
	if panel := m.harnessPanel; panel != nil {
		panel.message = fmt.Sprintf("Queued harness task %s", task.ID)
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
		panel.message = "Type a run goal in the panel input first."
		return nil
	}
	command := "/harness run " + goal
	project := *panel.project
	cfg := panel.cfg
	m.closeHarnessPanel()
	return m.runTrackedHarnessGoal(command, goal, project, cfg)
}

func (m *Model) runHarnessQueued(opts harness.QueueRunOptions) tea.Cmd {
	panel := m.harnessPanel
	if panel == nil || panel.project == nil || panel.cfg == nil {
		return nil
	}
	summary, err := harness.RunQueuedTasks(context.Background(), *panel.project, panel.cfg, nil, opts)
	if err != nil {
		panel.message = err.Error()
		return nil
	}
	panel.lastQueueRun = summary
	m.refreshHarnessPanel()
	if panel := m.harnessPanel; panel != nil {
		panel.lastQueueRun = summary
		panel.message = queueRunMessage(summary, opts)
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
			label := firstNonEmptyHarness(summary.Path, summary.Name, "unscoped")
			items = append(items, truncateString(fmt.Sprintf("%s • %d tasks", label, summary.TaskCount), 52))
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
			items = append(items, truncateString(fmt.Sprintf("%s • review %d • promote %d", entry.Owner, len(entry.ReviewReady), len(entry.PromotionReady)), 52))
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
			items = append(items, truncateString(fmt.Sprintf("%s • %s", rollout.RolloutID, harnessRolloutLabel(rollout)), 52))
		}
		return items
	default:
		return nil
	}
}

func (m Model) harnessPanelLeftWidth(totalWidth int) int {
	longest := len("Views")
	for _, title := range harnessSectionTitles {
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
		return "Harness is not initialized in this project yet.\n\nPress Enter or i to run harness init in the current repository."
	}
	switch panel.selectedSection {
	case harnessSectionInit:
		return renderHarnessProjectSummary(panel.project, panel.cfg)
	case harnessSectionCheck:
		if panel.lastCheck != nil {
			return harness.FormatCheckReport(panel.lastCheck)
		}
		return "Run harness checks against the current project.\n\nEnter: run required file/content/context checks plus configured validation commands."
	case harnessSectionDoctor:
		return renderHarnessDoctorPreview(panel.doctor)
	case harnessSectionMonitor:
		return renderHarnessMonitorPreview(panel.project, panel.monitor)
	case harnessSectionGC:
		if panel.lastGC != nil {
			return harness.FormatGCReport(panel.lastGC)
		}
		return "Run harness garbage collection.\n\nEnter: archive stale tasks, abandon stale blocked/running work, prune old logs, and remove orphaned worktrees."
	case harnessSectionContexts:
		if summary := m.selectedHarnessContextSummary(); summary != nil {
			return renderHarnessContextSummary(summary)
		}
		return harness.FormatContextReport(panel.contexts)
	case harnessSectionTasks:
		if task := m.selectedHarnessTask(); task != nil {
			root := ""
			if panel.project != nil {
				root = panel.project.RootDir
			}
			return renderHarnessTask(task, root)
		}
		return harness.FormatTaskList(panel.tasks)
	case harnessSectionQueue:
		return renderHarnessDraftPreview("Queue", panel.actionInput.Value(), "Type the harness goal here, then press Enter to queue it.")
	case harnessSectionRun:
		return renderHarnessDraftPreview("Run", panel.actionInput.Value(), "Type the harness goal here, then press Enter to start the run.")
	case harnessSectionRunQueued:
		if panel.lastQueueRun != nil {
			return harness.FormatQueueSummary(panel.lastQueueRun)
		}
		return renderHarnessRunQueuedPreview(panel.tasks)
	case harnessSectionInbox:
		if entry := m.selectedHarnessInboxEntry(); entry != nil {
			return renderHarnessInboxEntry(entry)
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
		return "Enter/i init harness • r refresh • Esc close"
	}
	hints := []string{"j/k move", "Tab switch", "r refresh", "Esc close"}
	switch panel.selectedSection {
	case harnessSectionCheck:
		hints = append(hints, "Enter run checks")
	case harnessSectionMonitor:
		hints = append(hints, "Enter refresh snapshot")
	case harnessSectionGC:
		hints = append(hints, "Enter run gc")
	case harnessSectionQueue:
		hints = append(hints, "type goal", "Enter queue", "Tab focus input")
	case harnessSectionRun:
		hints = append(hints, "type goal", "Enter run", "Tab focus input")
	case harnessSectionTasks:
		if task := m.selectedHarnessTask(); task != nil && task.Status == harness.TaskFailed {
			hints = append(hints, "Enter rerun failed")
		}
	case harnessSectionRunQueued:
		hints = append(hints, "Enter next", "a all", "f retry-failed", "s resume")
	case harnessSectionInbox:
		hints = append(hints, "p promote owner", "f retry owner")
	case harnessSectionReview:
		hints = append(hints, "Enter approve", "x reject")
	case harnessSectionPromote:
		hints = append(hints, "Enter promote")
	case harnessSectionRelease:
		hints = append(hints, "Enter apply batch")
	case harnessSectionRollouts:
		hints = append(hints, "Enter advance", "g approve gate", "p pause/resume", "x abort")
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

func renderHarnessContextSummary(summary *harness.ContextSummary) string {
	if summary == nil {
		return "No harness context selected."
	}
	label := firstNonEmptyHarness(summary.Path, summary.Name, "unscoped")
	var b strings.Builder
	fmt.Fprintf(&b, "Context: %s\n", label)
	if summary.Name != "" && summary.Name != label {
		fmt.Fprintf(&b, "name: %s\n", summary.Name)
	}
	if summary.Description != "" {
		fmt.Fprintf(&b, "description: %s\n", summary.Description)
	}
	if summary.Owner != "" {
		fmt.Fprintf(&b, "owner: %s\n", summary.Owner)
	}
	fmt.Fprintf(&b, "commands: %d\n", summary.CommandCount)
	fmt.Fprintf(&b, "tasks: total=%d queued=%d running=%d blocked=%d failed=%d review_ready=%d promotion_ready=%d release_ready=%d\n",
		summary.TaskCount, summary.QueuedTasks, summary.RunningTasks, summary.BlockedTasks, summary.FailedTasks, summary.ReviewReady, summary.PromotionReady, summary.ReleaseReady)
	fmt.Fprintf(&b, "rollouts: active=%d planned=%d paused=%d aborted=%d completed=%d\n",
		summary.ActiveRollouts, summary.PlannedRollouts, summary.PausedRollouts, summary.AbortedRollouts, summary.CompletedRollouts)
	fmt.Fprintf(&b, "gates: pending=%d approved=%d rejected=%d\n", summary.PendingGates, summary.ApprovedGates, summary.RejectedGates)
	if summary.LatestTask != nil {
		fmt.Fprintf(&b, "latest: %s [%s] %s\n", summary.LatestTask.ID, summary.LatestTask.Status, summary.LatestTask.Goal)
	}
	return strings.TrimRight(b.String(), "\n")
}

func renderHarnessTask(task *harness.Task, root string) string {
	if task == nil {
		return "No harness task selected."
	}
	var b strings.Builder
	b.WriteString("Harness task\n")
	fmt.Fprintf(&b, "id: %s\n", task.ID)
	fmt.Fprintf(&b, "status: %s\n", task.Status)
	if task.Goal != "" {
		b.WriteString("goal:\n")
		fmt.Fprintf(&b, "  %s\n", strings.TrimSpace(task.Goal))
	}
	if task.Attempt > 0 {
		fmt.Fprintf(&b, "attempts: %d\n", task.Attempt)
	}
	if len(task.DependsOn) > 0 {
		fmt.Fprintf(&b, "depends_on: %s\n", strings.Join(task.DependsOn, ", "))
	}
	if task.ContextName != "" || task.ContextPath != "" {
		label := firstNonEmptyHarness(task.ContextName, harnessPanelPathLabel(root, task.ContextPath))
		fmt.Fprintf(&b, "context: %s\n", label)
	}
	if task.WorkspacePath != "" {
		fmt.Fprintf(&b, "workspace: %s\n", harnessPanelPathLabel(root, task.WorkspacePath))
	}
	if task.BranchName != "" {
		fmt.Fprintf(&b, "branch: %s\n", task.BranchName)
	}
	if task.WorkerID != "" {
		status := firstNonEmptyHarness(task.WorkerStatus, "unknown")
		if strings.TrimSpace(task.WorkerPhase) != "" && task.WorkerPhase != status {
			fmt.Fprintf(&b, "worker: %s [%s, %s]\n", task.WorkerID, status, task.WorkerPhase)
		} else {
			fmt.Fprintf(&b, "worker: %s [%s]\n", task.WorkerID, status)
		}
	}
	if task.WorkerProgress != "" {
		fmt.Fprintf(&b, "progress: %s\n", compactSingleLine(task.WorkerProgress))
	}
	if task.VerificationStatus != "" {
		fmt.Fprintf(&b, "verification: %s\n", task.VerificationStatus)
	}
	if len(task.ChangedFiles) > 0 {
		fmt.Fprintf(&b, "changed_files: %d\n", len(task.ChangedFiles))
	}
	if task.VerificationReportPath != "" {
		fmt.Fprintf(&b, "delivery_report: %s\n", harnessPanelPathLabel(root, task.VerificationReportPath))
	}
	if task.LogPath != "" {
		fmt.Fprintf(&b, "log: %s\n", harnessPanelPathLabel(root, task.LogPath))
	}
	if task.ReviewStatus != "" {
		fmt.Fprintf(&b, "review: %s\n", task.ReviewStatus)
	}
	if task.ReviewNotes != "" {
		fmt.Fprintf(&b, "review_notes: %s\n", compactSingleLine(task.ReviewNotes))
	}
	if task.PromotionStatus != "" {
		fmt.Fprintf(&b, "promotion: %s\n", task.PromotionStatus)
	}
	if task.PromotionNotes != "" {
		fmt.Fprintf(&b, "promotion_notes: %s\n", compactSingleLine(task.PromotionNotes))
	}
	if task.ReleaseBatchID != "" {
		fmt.Fprintf(&b, "release_batch: %s\n", task.ReleaseBatchID)
	}
	if task.ReleaseNotes != "" {
		fmt.Fprintf(&b, "release_notes: %s\n", compactSingleLine(task.ReleaseNotes))
	}
	if task.Error != "" {
		fmt.Fprintf(&b, "error: %s\n", compactSingleLine(task.Error))
	}
	return strings.TrimRight(b.String(), "\n")
}

func renderHarnessProjectSummary(project *harness.Project, cfg *harness.Config) string {
	if project == nil {
		return "Harness is not initialized in this project yet."
	}
	var b strings.Builder
	b.WriteString("Harness is initialized.\n")
	fmt.Fprintf(&b, "\nrepo: %s\n", harnessPanelPathLabel(project.RootDir, project.RootDir))
	fmt.Fprintf(&b, "config: %s\n", harnessPanelPathLabel(project.RootDir, project.ConfigPath))
	if cfg != nil && strings.TrimSpace(cfg.Project.Name) != "" {
		fmt.Fprintf(&b, "project: %s\n", cfg.Project.Name)
	}
	b.WriteString("\nUse /harness to browse and operate the control plane.")
	return strings.TrimRight(b.String(), "\n")
}

func renderHarnessDoctorPreview(report *harness.DoctorReport) string {
	if report == nil {
		return "No harness doctor report."
	}
	var b strings.Builder
	b.WriteString("Harness doctor\n")
	fmt.Fprintf(&b, "- repo: %s\n", harnessPanelPathLabel(report.Project.RootDir, report.Project.RootDir))
	fmt.Fprintf(&b, "- config: %s\n", harnessPanelPathLabel(report.Project.RootDir, report.Project.ConfigPath))
	if report.Config != nil && strings.TrimSpace(report.Config.Project.Name) != "" {
		fmt.Fprintf(&b, "- project: %s\n", report.Config.Project.Name)
	}
	if report.Structural != nil {
		status := "ok"
		if !report.Structural.Passed {
			status = "needs attention"
		}
		fmt.Fprintf(&b, "- structure: %s\n", status)
	}
	if report.Contexts > 0 {
		fmt.Fprintf(&b, "- contexts: %d\n", report.Contexts)
	}
	fmt.Fprintf(&b, "- tasks: total=%d running=%d blocked=%d failed=%d\n", report.TotalTasks, report.RunningTasks, report.BlockedTasks, report.FailedTasks)
	fmt.Fprintf(&b, "- workers: backed=%d drift=%d\n", report.WorkerTasks, report.WorkerDrift)
	fmt.Fprintf(&b, "- workflow: review_ready=%d promotion_ready=%d release_ready=%d retryable=%d\n", report.ReviewReady, report.PromotionReady, report.ReleaseReady, report.Retryable)
	fmt.Fprintf(&b, "- quality: verification_failed=%d stale_blocked=%d\n", report.VerificationFailed, report.StaleBlocked)
	fmt.Fprintf(&b, "- worktrees: orphaned=%d\n", report.OrphanedWorktrees)
	if report.Rollouts > 0 {
		fmt.Fprintf(&b, "- rollouts: total=%d active=%d planned=%d paused=%d aborted=%d completed=%d\n", report.Rollouts, report.ActiveRollouts, report.PlannedRollouts, report.PausedRollouts, report.AbortedRollouts, report.CompletedRollouts)
		fmt.Fprintf(&b, "- gates: pending=%d approved=%d rejected=%d\n", report.PendingGates, report.ApprovedGates, report.RejectedGates)
	}
	if report.LastTask != nil {
		fmt.Fprintf(&b, "\nLatest task\n- %s [%s]\n", report.LastTask.ID, report.LastTask.Status)
		if label := firstNonEmptyHarness(report.LastTask.ContextName, report.LastTask.ContextPath); label != "" {
			fmt.Fprintf(&b, "- context: %s\n", label)
		}
		if report.LastTask.WorkerID != "" {
			fmt.Fprintf(&b, "- worker: %s [%s]\n", report.LastTask.WorkerID, report.LastTask.WorkerStatus)
		}
		if report.LastTask.WorkerProgress != "" {
			fmt.Fprintf(&b, "- progress: %s\n", compactSingleLine(report.LastTask.WorkerProgress))
		}
		if report.LastTask.VerificationStatus != "" {
			fmt.Fprintf(&b, "- verification: %s\n", report.LastTask.VerificationStatus)
		}
		if report.LastTask.ReviewStatus != "" {
			fmt.Fprintf(&b, "- review: %s\n", report.LastTask.ReviewStatus)
		}
		if report.LastTask.PromotionStatus != "" {
			fmt.Fprintf(&b, "- promotion: %s\n", report.LastTask.PromotionStatus)
		}
		if report.LastTask.ReleaseBatchID != "" {
			fmt.Fprintf(&b, "- release batch: %s\n", report.LastTask.ReleaseBatchID)
		}
		if report.LastTask.LogPath != "" {
			fmt.Fprintf(&b, "- log: %s\n", harnessPanelPathLabel(report.Project.RootDir, report.LastTask.LogPath))
		}
		if report.LastTask.VerificationReportPath != "" {
			fmt.Fprintf(&b, "- delivery report: %s\n", harnessPanelPathLabel(report.Project.RootDir, report.LastTask.VerificationReportPath))
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func renderHarnessMonitorPreview(project *harness.Project, report *harness.MonitorReport) string {
	if report == nil {
		return "Harness monitor unavailable."
	}
	root := ""
	if project != nil {
		root = project.RootDir
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Harness monitor\n- snapshot: %s\n- events: %s\n",
		harnessPanelPathLabel(root, report.SnapshotPath),
		harnessPanelPathLabel(root, report.EventLogPath),
	)
	fmt.Fprintf(&b, "- tasks: total=%d queued=%d running=%d blocked=%d failed=%d\n",
		report.TaskTotals.Total, report.TaskTotals.Queued, report.TaskTotals.Running, report.TaskTotals.Blocked, report.TaskTotals.Failed)
	fmt.Fprintf(&b, "- workflow: review_pending=%d promotion_ready=%d released=%d active_workers=%d\n",
		report.TaskTotals.ReviewPending, report.TaskTotals.PromotionReady, report.TaskTotals.Released, report.TaskTotals.ActiveWorkers)
	fmt.Fprintf(&b, "- rollouts: batches=%d active=%d planned=%d paused=%d aborted=%d completed=%d\n",
		report.RolloutTotals.Batches, report.RolloutTotals.Active, report.RolloutTotals.Planned, report.RolloutTotals.Paused, report.RolloutTotals.Aborted, report.RolloutTotals.Completed)
	fmt.Fprintf(&b, "- gates: pending=%d approved=%d rejected=%d\n",
		report.RolloutTotals.GatesPending, report.RolloutTotals.GatesApproved, report.RolloutTotals.GatesRejected)
	if len(report.FocusTasks) > 0 {
		task := report.FocusTasks[0]
		fmt.Fprintf(&b, "\nFocus\n- %s [%s]\n", task.ID, firstNonEmptyHarness(task.Status, "unknown"))
		if task.Goal != "" {
			fmt.Fprintf(&b, "- goal: %s\n", compactSingleLine(task.Goal))
		}
		if context := firstNonEmptyHarness(task.ContextPath, task.ContextName); context != "" {
			fmt.Fprintf(&b, "- context: %s\n", context)
		}
	}
	if len(report.RecentEvents) > 0 {
		event := report.RecentEvents[0]
		fmt.Fprintf(&b, "\nLatest event\n- %s %s\n", event.RecordedAt.Format("15:04:05"), event.Kind)
		if summary := strings.TrimSpace(event.Summary); summary != "" {
			fmt.Fprintf(&b, "- %s\n", compactSingleLine(summary))
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func (m Model) renderHarnessPanelNavLines(width, height int) []string {
	panel := m.harnessPanel
	lines := []string{"Views", ""}
	if panel == nil {
		return normalizeHarnessPanelLines(lines, height)
	}
	lines = append(lines, renderHarnessPlainList(harnessSectionTitles, panel.selectedSection, panel.focus == harnessPanelFocusSection, width, max(1, height-len(lines)))...)
	return normalizeHarnessPanelLines(lines, height)
}

func (m Model) renderHarnessPanelMainLines(width, height int) []string {
	panel := m.harnessPanel
	if panel == nil {
		return normalizeHarnessPanelLines(nil, height)
	}
	lines := []string{harnessSectionTitles[panel.selectedSection], ""}
	items := m.harnessPanelItems()
	if len(items) > 0 {
		lines = append(lines, "Items")
		lines = append(lines, renderHarnessPlainList(items, panel.selectedItem, panel.focus == harnessPanelFocusItem, width, 6)...)
		lines = append(lines, "")
	}
	lines = append(lines, "Action")
	lines = append(lines, m.renderHarnessActionLines(width)...)
	lines = append(lines, "", "Details")

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
			lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(truncateString(harnessPanelPrimaryHint(panel.selectedSection), width)),
		}
	}
	return []string{
		lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(truncateString(harnessPanelPrimaryHint(panel.selectedSection), width)),
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

func renderHarnessPlainList(items []string, selected int, focused bool, width, maxLines int) []string {
	if len(items) == 0 || maxLines <= 0 {
		return []string{"  (none)"}
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

func harnessPanelPrimaryHint(section int) string {
	switch section {
	case harnessSectionCheck:
		return "Press Enter to run checks."
	case harnessSectionMonitor:
		return "Press Enter to refresh the monitor snapshot."
	case harnessSectionGC:
		return "Press Enter to run garbage collection."
	case harnessSectionQueue:
		return "Type a goal, then press Enter to queue it."
	case harnessSectionRun:
		return "Type a goal, then press Enter to start the run."
	case harnessSectionTasks:
		return "Press Enter to rerun the selected failed task."
	case harnessSectionRunQueued:
		return "Press Enter for next; a runs all; f retries failed; s resumes interrupted."
	case harnessSectionInbox:
		return "Press p to promote this owner or f to retry this owner."
	case harnessSectionReview:
		return "Press Enter to approve or x to reject."
	case harnessSectionPromote:
		return "Press Enter to promote the selected task."
	case harnessSectionRelease:
		return "Press Enter to apply the current release batch."
	case harnessSectionRollouts:
		return "Press Enter to advance; g approves gate; p pauses/resumes; x aborts."
	default:
		return "No inline input needed for this section."
	}
}

func renderHarnessDraftPreview(label, draft, help string) string {
	draft = strings.TrimSpace(draft)
	if draft == "" {
		draft = "(input box is empty)"
	}
	return fmt.Sprintf("%s target:\n%s\n\n%s", label, draft, help)
}

func renderHarnessRunQueuedPreview(tasks []*harness.Task) string {
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
	return fmt.Sprintf("Queue status:\nqueued=%d running=%d blocked=%d failed=%d\n\nEnter runs the next runnable task.\na runs all runnable tasks.\nf retries failed tasks.\ns resumes interrupted tasks.", queued, running, blocked, failed)
}

func renderHarnessInboxEntry(entry *harness.OwnerInboxEntry) string {
	if entry == nil {
		return "No harness owner selected."
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Owner: %s\n", entry.Owner)
	fmt.Fprintf(&b, "review_ready: %d\n", len(entry.ReviewReady))
	fmt.Fprintf(&b, "promotion_ready: %d\n", len(entry.PromotionReady))
	fmt.Fprintf(&b, "retryable: %d\n", len(entry.Retryable))
	fmt.Fprintf(&b, "rollouts: active=%d planned=%d paused=%d aborted=%d completed=%d\n",
		entry.ActiveRollouts, entry.PlannedRollouts, entry.PausedRollouts, entry.AbortedRollouts, entry.CompletedRollouts)
	fmt.Fprintf(&b, "gates: pending=%d approved=%d rejected=%d\n", entry.PendingGates, entry.ApprovedGates, entry.RejectedGates)
	appendHarnessTaskGroup(&b, "review", entry.ReviewReady)
	appendHarnessTaskGroup(&b, "promotion", entry.PromotionReady)
	appendHarnessTaskGroup(&b, "retry", entry.Retryable)
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

func harnessRolloutLabel(rollout *harness.ReleaseWavePlan) string {
	if rollout == nil || len(rollout.Groups) == 0 {
		return "no waves"
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
	return "mixed"
}

func queueRunMessage(summary *harness.RunQueueSummary, opts harness.QueueRunOptions) string {
	if summary == nil {
		return "No queued harness tasks were executed."
	}
	switch {
	case opts.RetryFailed:
		return fmt.Sprintf("Retried %d failed queued task(s).", len(summary.Executed))
	case opts.ResumeInterrupted:
		return fmt.Sprintf("Resumed %d interrupted queued task(s).", len(summary.Executed))
	case opts.All:
		return fmt.Sprintf("Ran %d queued task(s).", len(summary.Executed))
	default:
		return fmt.Sprintf("Ran %d queued task(s).", len(summary.Executed))
	}
}

func newHarnessPanelInput(lang Language) textinput.Model {
	ti := textinput.New()
	ti.Prompt = "  "
	ti.Placeholder = harnessPanelInputPlaceholder(harnessSectionQueue, lang)
	ti.CharLimit = 240
	ti.Width = 52
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
	control.Width = max(20, width-2)
	if focused {
		control.Focus()
	} else {
		control.Blur()
	}
	return control.View()
}

func renderHarnessActionInputBox(section int, input textinput.Model, focused bool, width int) string {
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
		Render(clipHarnessPanelText("No inline input needed for this command.", width, 3))
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

func isHarnessPanelInputKey(msg tea.KeyMsg) bool {
	switch msg.Type {
	case tea.KeyRunes, tea.KeySpace, tea.KeyBackspace, tea.KeyDelete:
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
