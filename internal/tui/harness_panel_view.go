package tui

import (
	"fmt"
	"path/filepath"
	"strings"

	"charm.land/bubbles/v2/textinput"
	"charm.land/lipgloss/v2"
	"github.com/muesli/reflow/wordwrap"

	"github.com/topcheer/ggcode/internal/harness"
	"github.com/topcheer/ggcode/internal/util"
)

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
func renderHarnessContextSummary(lang Language, summary *harness.ContextSummary) string {
	if summary == nil {
		return tr(lang, "harness.preview.no_context")
	}
	label := util.FirstNonEmpty(summary.Path, summary.Name, tr(lang, "harness.unscoped"))
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
		label := util.FirstNonEmpty(task.ContextName, harnessPanelPathLabel(root, task.ContextPath))
		fmt.Fprintf(&b, "%s: %s\n", tr(lang, "harness.label.context"), label)
	}
	if task.WorkspacePath != "" {
		fmt.Fprintf(&b, "%s: %s\n", tr(lang, "harness.label.workspace"), harnessPanelPathLabel(root, task.WorkspacePath))
	}
	if task.BranchName != "" {
		fmt.Fprintf(&b, "%s: %s\n", tr(lang, "harness.label.branch"), task.BranchName)
	}
	if task.WorkerID != "" {
		status := util.FirstNonEmpty(task.WorkerStatus, tr(lang, "harness.unknown"))
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
		if label := util.FirstNonEmpty(report.LastTask.ContextName, report.LastTask.ContextPath); label != "" {
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
	fmt.Fprintf(&b, "%s\n- %s: %s\n- %s: %s\n", tr(lang, "harness.monitor_title"), tr(lang, "harness.label.snapshot"), harnessPanelPathLabel(root, report.SnapshotDir), tr(lang, "harness.label.events"), harnessPanelPathLabel(root, report.EventLogPath))
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
		fmt.Fprintf(&b, "\n%s\n- %s [%s]\n", tr(lang, "harness.focus"), task.ID, util.FirstNonEmpty(task.Status, tr(lang, "harness.unknown")))
		if task.Goal != "" {
			fmt.Fprintf(&b, "- %s: %s\n", tr(lang, "harness.label.goal"), compactSingleLine(task.Goal))
		}
		if context := util.FirstNonEmpty(task.ContextPath, task.ContextName); context != "" {
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
			lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(util.Truncate(harnessPanelPrimaryHint(panel.selectedSection, m.currentLanguage()), width)),
		}
	}
	return []string{
		lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(util.Truncate(harnessPanelPrimaryHint(panel.selectedSection, m.currentLanguage()), width)),
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
		lines = append(lines, lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(util.Truncate(hints, width)))
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
		lines = append(lines, prefix+util.Truncate(items[i], max(1, width-len(prefix))))
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
		lines[i] = util.Truncate(line, width)
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
