package harness

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type MonitorOptions struct {
	RecentEvents int
	FocusTasks   int
}

type MonitorReport struct {
	GeneratedAt   time.Time
	SnapshotDir   string
	EventLogPath  string
	TaskTotals    MonitorTaskTotals
	RolloutTotals MonitorRolloutTotals
	FocusTasks    []*MonitorTask
	RecentEvents  []*MonitorEvent
}

type MonitorTaskTotals struct {
	Total          int
	Queued         int
	Blocked        int
	Running        int
	Completed      int
	Failed         int
	Abandoned      int
	ReviewPending  int
	PromotionReady int
	Released       int
	ActiveWorkers  int
}

type MonitorRolloutTotals struct {
	Batches       int
	Rollouts      int
	Planned       int
	Active        int
	Paused        int
	Aborted       int
	Completed     int
	GatesPending  int
	GatesApproved int
	GatesRejected int
}

type MonitorTask struct {
	ID              string
	Goal            string
	Status          string
	ContextName     string
	ContextPath     string
	WorkerID        string
	WorkerStatus    string
	WorkerPhase     string
	WorkerProgress  string
	ReviewStatus    string
	PromotionStatus string
	ReleaseBatchID  string
	UpdatedAt       time.Time
}

type MonitorEvent struct {
	Kind       string
	EntityType string
	EntityID   string
	TaskID     string
	BatchID    string
	RolloutID  string
	WaveOrder  int
	Status     string
	GateStatus string
	Summary    string
	RecordedAt time.Time
}

func BuildMonitorReport(project Project, opts MonitorOptions) (*MonitorReport, error) {
	if opts.RecentEvents <= 0 {
		opts.RecentEvents = 8
	}
	if opts.FocusTasks <= 0 {
		opts.FocusTasks = 6
	}

	report := &MonitorReport{
		GeneratedAt:  time.Now().UTC(),
		SnapshotDir:  filepath.Join(project.StateDir, "snapshots"),
		EventLogPath: project.EventLogPath,
	}

	tasks, err := loadAllTaskSnapshots(project)
	if err != nil {
		return nil, fmt.Errorf("load task snapshots: %w", err)
	}
	aggregateTaskTotals(tasks, &report.TaskTotals)

	focusTasks := selectFocusTasks(tasks, opts.FocusTasks)
	report.FocusTasks = focusTasks

	plans, err := loadAllReleasePlanSnapshots(project)
	if err != nil {
		return nil, fmt.Errorf("load release snapshots: %w", err)
	}
	aggregateRolloutTotals(plans, &report.RolloutTotals)

	recentEvents, err := loadRecentEventsFromJSONL(project.EventLogPath, opts.RecentEvents)
	if err != nil {
		return nil, fmt.Errorf("load recent events: %w", err)
	}
	report.RecentEvents = recentEvents

	return report, nil
}

func FormatMonitorReport(report *MonitorReport) string {
	if report == nil {
		return "Harness monitor unavailable."
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("Harness monitor @ %s\n", report.GeneratedAt.Format(time.RFC3339)))
	fmt.Fprintf(&b, "- snapshots: %s\n", monitorDisplayPath(report.SnapshotDir))
	fmt.Fprintf(&b, "- events: %s\n", monitorDisplayPath(report.EventLogPath))
	fmt.Fprintf(&b, "Tasks: total=%d queued=%d blocked=%d running=%d completed=%d failed=%d abandoned=%d\n",
		report.TaskTotals.Total, report.TaskTotals.Queued, report.TaskTotals.Blocked, report.TaskTotals.Running,
		report.TaskTotals.Completed, report.TaskTotals.Failed, report.TaskTotals.Abandoned)
	fmt.Fprintf(&b, "Workflow: review_pending=%d promotion_ready=%d released=%d active_workers=%d\n",
		report.TaskTotals.ReviewPending, report.TaskTotals.PromotionReady, report.TaskTotals.Released, report.TaskTotals.ActiveWorkers)
	fmt.Fprintf(&b, "Rollouts: batches=%d rollouts=%d active=%d planned=%d paused=%d aborted=%d completed=%d\n",
		report.RolloutTotals.Batches, report.RolloutTotals.Rollouts, report.RolloutTotals.Active,
		report.RolloutTotals.Planned, report.RolloutTotals.Paused, report.RolloutTotals.Aborted, report.RolloutTotals.Completed)
	fmt.Fprintf(&b, "Gates: pending=%d approved=%d rejected=%d\n",
		report.RolloutTotals.GatesPending, report.RolloutTotals.GatesApproved, report.RolloutTotals.GatesRejected)

	b.WriteString("\nFocus tasks:\n")
	if len(report.FocusTasks) == 0 {
		b.WriteString("- none\n")
	} else {
		for _, task := range report.FocusTasks {
			if task == nil {
				continue
			}
			fmt.Fprintf(&b, "- %s [%s] %s\n", task.ID, firstNonEmptyText(task.Status, "unknown"), strings.TrimSpace(task.Goal))
			if context := firstNonEmptyText(task.ContextPath, task.ContextName); context != "" {
				fmt.Fprintf(&b, "  context: %s\n", context)
			}
			if task.WorkerID != "" || task.WorkerStatus != "" || task.WorkerPhase != "" {
				fmt.Fprintf(&b, "  worker: %s %s\n",
					strings.TrimSpace(firstNonEmptyText(task.WorkerStatus, "unknown")),
					strings.TrimSpace(firstNonEmptyText(task.WorkerPhase, "")))
				if strings.TrimSpace(task.WorkerProgress) != "" {
					fmt.Fprintf(&b, "  progress: %s\n", strings.TrimSpace(task.WorkerProgress))
				}
			}
			if task.ReviewStatus != "" || task.PromotionStatus != "" || task.ReleaseBatchID != "" {
				fmt.Fprintf(&b, "  review=%s promotion=%s release=%s\n",
					firstNonEmptyText(task.ReviewStatus, "-"),
					firstNonEmptyText(task.PromotionStatus, "-"),
					firstNonEmptyText(task.ReleaseBatchID, "-"))
			}
			fmt.Fprintf(&b, "  updated: %s\n", task.UpdatedAt.Format(time.RFC3339))
		}
	}

	b.WriteString("\nRecent events:\n")
	if len(report.RecentEvents) == 0 {
		b.WriteString("- none\n")
	} else {
		for _, event := range report.RecentEvents {
			if event == nil {
				continue
			}
			fmt.Fprintf(&b, "- %s %s %s\n", event.RecordedAt.Format("15:04:05"), event.Kind, firstNonEmptyText(event.Summary, event.EntityID))
			switch {
			case event.RolloutID != "":
				fmt.Fprintf(&b, "  rollout=%s wave=%d status=%s gate=%s\n", event.RolloutID, event.WaveOrder, firstNonEmptyText(event.Status, "-"), firstNonEmptyText(event.GateStatus, "-"))
			case event.TaskID != "":
				fmt.Fprintf(&b, "  task=%s status=%s\n", event.TaskID, firstNonEmptyText(event.Status, "-"))
			case event.BatchID != "":
				fmt.Fprintf(&b, "  batch=%s status=%s\n", event.BatchID, firstNonEmptyText(event.Status, "-"))
			}
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// --- In-memory aggregation ---

func aggregateTaskTotals(tasks []*Task, totals *MonitorTaskTotals) {
	totals.Total = len(tasks)
	for _, t := range tasks {
		switch t.Status {
		case TaskQueued:
			totals.Queued++
		case TaskBlocked:
			totals.Blocked++
		case TaskRunning:
			totals.Running++
		case TaskCompleted:
			totals.Completed++
		case TaskFailed:
			totals.Failed++
		case TaskAbandoned:
			totals.Abandoned++
		}
		if t.ReviewStatus == ReviewPending {
			totals.ReviewPending++
		}
		if t.Status == TaskCompleted && t.ReviewStatus == ReviewApproved && t.PromotionStatus != PromotionApplied {
			totals.PromotionReady++
		}
		if t.ReleasedAt != nil {
			totals.Released++
		}
		if t.WorkerID != "" && t.Status == TaskRunning {
			totals.ActiveWorkers++
		}
	}
}

func aggregateRolloutTotals(plans []*ReleasePlan, totals *MonitorRolloutTotals) {
	seen := make(map[string]bool)
	totals.Batches = len(plans)
	for _, p := range plans {
		if rid := strings.TrimSpace(p.RolloutID); rid != "" {
			seen[rid] = true
		}
		switch strings.TrimSpace(p.WaveStatus) {
		case ReleaseWavePlanned:
			totals.Planned++
		case ReleaseWaveActive:
			totals.Active++
		case ReleaseWavePaused:
			totals.Paused++
		case ReleaseWaveAborted:
			totals.Aborted++
		case ReleaseWaveCompleted:
			totals.Completed++
		}
		switch strings.TrimSpace(p.GateStatus) {
		case ReleaseGatePending:
			totals.GatesPending++
		case ReleaseGateApproved:
			totals.GatesApproved++
		case ReleaseGateRejected:
			totals.GatesRejected++
		}
	}
	totals.Rollouts = len(seen)
}

// selectFocusTasks returns the top N most relevant tasks sorted by status
// priority (running > blocked > failed > queued > other) then by updated_at desc.
func selectFocusTasks(tasks []*Task, limit int) []*MonitorTask {
	statusPriority := map[TaskStatus]int{
		TaskRunning:   0,
		TaskBlocked:   1,
		TaskFailed:    2,
		TaskQueued:    3,
		TaskCompleted: 4,
		TaskAbandoned: 5,
	}

	sorted := make([]*Task, len(tasks))
	copy(sorted, tasks)
	sort.SliceStable(sorted, func(i, j int) bool {
		pi, oki := statusPriority[sorted[i].Status]
		pj, okj := statusPriority[sorted[j].Status]
		if !oki {
			pi = 6
		}
		if !okj {
			pj = 6
		}
		if pi != pj {
			return pi < pj
		}
		return sorted[i].UpdatedAt.After(sorted[j].UpdatedAt)
	})

	if len(sorted) > limit {
		sorted = sorted[:limit]
	}

	result := make([]*MonitorTask, len(sorted))
	for i, t := range sorted {
		result[i] = &MonitorTask{
			ID:              t.ID,
			Goal:            t.Goal,
			Status:          string(t.Status),
			ContextName:     t.ContextName,
			ContextPath:     t.ContextPath,
			WorkerID:        t.WorkerID,
			WorkerStatus:    t.WorkerStatus,
			WorkerPhase:     t.WorkerPhase,
			WorkerProgress:  t.WorkerProgress,
			ReviewStatus:    t.ReviewStatus,
			PromotionStatus: t.PromotionStatus,
			ReleaseBatchID:  t.ReleaseBatchID,
			UpdatedAt:       t.UpdatedAt,
		}
	}
	return result
}

func monitorDisplayPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return "(unset)"
	}
	if rel, err := filepath.Rel(".", path); err == nil && !strings.HasPrefix(rel, "..") {
		return rel
	}
	return path
}

// firstNonEmptyText is already defined elsewhere in the package; avoid redeclaration.
// The harness package already has this helper.

// marshalSnapshotJSON is kept for backward compat — it now just returns the
// JSON bytes directly since we no longer need SQL-compatible any values.
// Deprecated: not needed anymore, kept only if other files reference it.
func marshalSnapshotJSON(v any) any {
	data, err := json.Marshal(v)
	if err != nil || string(data) == "null" {
		return nil
	}
	return string(data)
}

func nullableText(text string) any {
	if strings.TrimSpace(text) == "" {
		return nil
	}
	return text
}

func nullableTime(ts *time.Time) any {
	if ts == nil {
		return nil
	}
	return ts.UTC().Format(time.RFC3339Nano)
}

func parseMonitorTime(raw string) time.Time {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}
	}
	parsed, err := time.Parse(time.RFC3339Nano, raw)
	if err == nil {
		return parsed.UTC()
	}
	return time.Time{}
}
