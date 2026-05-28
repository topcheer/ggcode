package tui

import (
	"fmt"
	"os"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/topcheer/ggcode/internal/harness"
	"github.com/topcheer/ggcode/internal/util"
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
	lastRefreshAt   time.Time
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
	_ = m.refreshHarnessPanel()
}

// refreshHarnessPanelSync loads harness panel data synchronously.
// Used by tests that need immediate data without async cmd dispatch.
func (m *Model) refreshHarnessPanelSync() {
	panel := m.harnessPanel
	if panel == nil {
		return
	}
	panel.lastRefreshAt = time.Now()

	workDir, _ := os.Getwd()
	project, cfg, err := loadHarnessForTUI(workDir)
	if err != nil {
		panel.loadErr = err.Error()
		panel.project = nil
		panel.cfg = nil
		return
	}
	panel.loadErr = ""
	panel.project = &project
	panel.cfg = cfg

	panel.doctor, _ = harness.Doctor(project, cfg)
	panel.monitor, _ = harness.BuildMonitorReport(project, harness.MonitorOptions{})
	panel.contexts, _ = harness.BuildContextReport(project, cfg)
	panel.tasks, _ = harness.ListTasks(project)
	panel.inbox, _ = harness.BuildOwnerInbox(project, cfg)
	panel.review, _ = harness.ListReviewableTasks(project)
	panel.promote, _ = harness.ListPromotableTasks(project)
	panel.release, _ = harness.BuildReleasePlan(project, cfg)
	panel.rollouts, _ = harness.ListReleaseWaveRollouts(project)

	panel.selectedSection = clampHarnessIndex(panel.selectedSection, len(harnessSectionTitles(m.currentLanguage())))
	m.updateHarnessPanelInputState()
	m.syncHarnessPanelSelection()
}

func (m *Model) closeHarnessPanel() {
	m.harnessPanel = nil
}

// refreshHarnessPanelCmd returns a tea.Cmd that loads harness data asynchronously.
func (m *Model) refreshHarnessPanelCmd() tea.Cmd {
	return func() tea.Msg {
		workDir, _ := os.Getwd()
		project, cfg, err := loadHarnessForTUI(workDir)
		if err != nil {
			return harnessPanelRefreshResultMsg{Err: err.Error()}
		}
		result := harnessPanelRefreshResultMsg{}
		result.Project = &project
		result.Cfg = cfg

		if r, e := harness.Doctor(project, cfg); e != nil {
			return harnessPanelRefreshResultMsg{Err: e.Error()}
		} else {
			result.Doctor = r
		}
		if r, e := harness.BuildMonitorReport(project, harness.MonitorOptions{}); e != nil {
			return harnessPanelRefreshResultMsg{Err: e.Error()}
		} else {
			result.Monitor = r
		}
		if r, e := harness.BuildContextReport(project, cfg); e != nil {
			return harnessPanelRefreshResultMsg{Err: e.Error()}
		} else {
			result.Contexts = r
		}
		if r, e := harness.ListTasks(project); e != nil {
			return harnessPanelRefreshResultMsg{Err: e.Error()}
		} else {
			result.Tasks = r
		}
		if r, e := harness.BuildOwnerInbox(project, cfg); e != nil {
			return harnessPanelRefreshResultMsg{Err: e.Error()}
		} else {
			result.Inbox = r
		}
		if r, e := harness.ListReviewableTasks(project); e != nil {
			return harnessPanelRefreshResultMsg{Err: e.Error()}
		} else {
			result.Review = r
		}
		if r, e := harness.ListPromotableTasks(project); e != nil {
			return harnessPanelRefreshResultMsg{Err: e.Error()}
		} else {
			result.Promote = r
		}
		if r, e := harness.BuildReleasePlan(project, cfg); e != nil {
			return harnessPanelRefreshResultMsg{Err: e.Error()}
		} else {
			result.Release = r
		}
		if r, e := harness.ListReleaseWaveRollouts(project); e != nil {
			return harnessPanelRefreshResultMsg{Err: e.Error()}
		} else {
			result.Rollouts = r
		}
		return result
	}
}

// applyHarnessPanelResult applies an async load result to the panel state.
func (m *Model) applyHarnessPanelResult(msg harnessPanelRefreshResultMsg) {
	panel := m.harnessPanel
	if panel == nil {
		return
	}
	if msg.Err != "" {
		panel.loadErr = msg.Err
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
	panel.project = msg.Project
	panel.cfg = msg.Cfg
	panel.doctor = msg.Doctor
	panel.monitor = msg.Monitor
	panel.contexts = msg.Contexts
	panel.tasks = msg.Tasks
	panel.inbox = msg.Inbox
	panel.review = msg.Review
	panel.promote = msg.Promote
	panel.release = msg.Release
	panel.rollouts = msg.Rollouts
	panel.selectedSection = clampHarnessIndex(panel.selectedSection, len(harnessSectionTitles(m.currentLanguage())))
	m.updateHarnessPanelInputState()
	m.syncHarnessPanelSelection()
}

func (m *Model) refreshHarnessPanel() tea.Cmd {
	panel := m.harnessPanel
	if panel == nil {
		return nil
	}
	// Debounce: skip refresh if last one was less than 500ms ago,
	// but always allow initial loads (no data yet).
	if panel.project != nil && panel.lastRefreshAt.After(time.Now().Add(-500*time.Millisecond)) {
		return nil
	}
	return m.refreshHarnessPanelForced()
}

func (m *Model) refreshHarnessPanelForced() tea.Cmd {
	panel := m.harnessPanel
	if panel == nil {
		return nil
	}
	panel.lastRefreshAt = time.Now()

	workDir, _ := os.Getwd()
	project, cfg, err := loadHarnessForTUI(workDir)
	if err != nil {
		panel.loadErr = err.Error()
		panel.project = nil
		panel.cfg = nil
		return nil
	}
	panel.loadErr = ""
	panel.project = &project
	panel.cfg = cfg

	panel.selectedSection = clampHarnessIndex(panel.selectedSection, len(harnessSectionTitles(m.currentLanguage())))
	m.updateHarnessPanelInputState()
	m.syncHarnessPanelSelection()

	// Return async cmd — data loads in a background goroutine,
	// result arrives via harnessPanelRefreshResultMsg.
	// This avoids blocking the UI main thread with 9 synchronous
	// harness API calls.
	return m.refreshHarnessPanelCmd()
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
			label := util.FirstNonEmpty(summary.Path, summary.Name, m.t("harness.unscoped"))
			items = append(items, util.Truncate(fmt.Sprintf("%s • %d %s", label, summary.TaskCount, m.t("harness.tasks_count")), 52))
		}
		return items
	case harnessSectionTasks:
		items := make([]string, 0, len(panel.tasks))
		for _, task := range panel.tasks {
			if task == nil {
				continue
			}
			items = append(items, util.Truncate(fmt.Sprintf("%s • %s • %s", task.ID, task.Status, compactSingleLine(task.Goal)), 52))
		}
		return items
	case harnessSectionInbox:
		if panel.inbox == nil {
			return nil
		}
		items := make([]string, 0, len(panel.inbox.Entries))
		for _, entry := range panel.inbox.Entries {
			items = append(items, util.Truncate(fmt.Sprintf("%s • %s %d • %s %d", entry.Owner, m.t("harness.review_ready_short"), len(entry.ReviewReady), m.t("harness.promote_ready_short"), len(entry.PromotionReady)), 52))
		}
		return items
	case harnessSectionReview:
		items := make([]string, 0, len(panel.review))
		for _, task := range panel.review {
			items = append(items, util.Truncate(fmt.Sprintf("%s • %s", task.ID, compactSingleLine(task.Goal)), 52))
		}
		return items
	case harnessSectionPromote:
		items := make([]string, 0, len(panel.promote))
		for _, task := range panel.promote {
			items = append(items, util.Truncate(fmt.Sprintf("%s • %s", task.ID, compactSingleLine(task.Goal)), 52))
		}
		return items
	case harnessSectionRollouts:
		items := make([]string, 0, len(panel.rollouts))
		for _, rollout := range panel.rollouts {
			items = append(items, util.Truncate(fmt.Sprintf("%s • %s", rollout.RolloutID, harnessRolloutLabel(m.currentLanguage(), rollout)), 52))
		}
		return items
	default:
		return nil
	}
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
func newHarnessPanelInput(lang Language) textinput.Model {
	ti := textinput.New()
	ti.Prompt = "  "
	ti.Placeholder = placeholderWithPasteShortcutHint(harnessPanelInputPlaceholder(harnessSectionQueue, lang), lang)
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
	panel.actionInput.Placeholder = placeholderWithPasteShortcutHint(harnessPanelInputPlaceholder(panel.selectedSection, m.currentLanguage()), m.currentLanguage())
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
