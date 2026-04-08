package harness

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

type MonitorOptions struct {
	RecentEvents int
	FocusTasks   int
}

type MonitorReport struct {
	GeneratedAt   time.Time
	SnapshotPath  string
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

	db, err := openHarnessSnapshot(project)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	report := &MonitorReport{
		GeneratedAt:  time.Now().UTC(),
		SnapshotPath: project.SnapshotPath,
		EventLogPath: project.EventLogPath,
	}
	if err := loadMonitorTaskTotals(db, &report.TaskTotals); err != nil {
		return nil, err
	}
	if err := loadMonitorRolloutTotals(db, &report.RolloutTotals); err != nil {
		return nil, err
	}
	focusTasks, err := loadMonitorFocusTasks(db, opts.FocusTasks)
	if err != nil {
		return nil, err
	}
	report.FocusTasks = focusTasks
	recentEvents, err := loadMonitorRecentEvents(db, opts.RecentEvents)
	if err != nil {
		return nil, err
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
	fmt.Fprintf(&b, "- snapshot: %s\n", monitorDisplayPath(report.SnapshotPath))
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

func loadMonitorTaskTotals(db *sql.DB, totals *MonitorTaskTotals) error {
	if totals == nil {
		return fmt.Errorf("missing monitor task totals")
	}
	return db.QueryRow(`
		SELECT
			COUNT(*),
			COALESCE(SUM(CASE WHEN status = ? THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN status = ? THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN status = ? THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN status = ? THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN status = ? THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN status = ? THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN review_status = ? THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN status = ? AND review_status = ? AND COALESCE(promotion_status, '') != ? THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN released_at IS NOT NULL AND released_at != '' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN worker_id IS NOT NULL AND worker_id != '' AND status = ? THEN 1 ELSE 0 END), 0)
		FROM tasks`,
		string(TaskQueued),
		string(TaskBlocked),
		string(TaskRunning),
		string(TaskCompleted),
		string(TaskFailed),
		string(TaskAbandoned),
		ReviewPending,
		string(TaskCompleted),
		ReviewApproved,
		PromotionApplied,
		string(TaskRunning),
	).Scan(
		&totals.Total,
		&totals.Queued,
		&totals.Blocked,
		&totals.Running,
		&totals.Completed,
		&totals.Failed,
		&totals.Abandoned,
		&totals.ReviewPending,
		&totals.PromotionReady,
		&totals.Released,
		&totals.ActiveWorkers,
	)
}

func loadMonitorRolloutTotals(db *sql.DB, totals *MonitorRolloutTotals) error {
	if totals == nil {
		return fmt.Errorf("missing monitor rollout totals")
	}
	return db.QueryRow(`
		SELECT
			COUNT(*),
			COUNT(DISTINCT CASE WHEN rollout_id IS NOT NULL AND rollout_id != '' THEN rollout_id END),
			COALESCE(SUM(CASE WHEN wave_status = ? THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN wave_status = ? THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN wave_status = ? THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN wave_status = ? THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN wave_status = ? THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN gate_status = ? THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN gate_status = ? THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN gate_status = ? THEN 1 ELSE 0 END), 0)
		FROM release_plans`,
		ReleaseWavePlanned,
		ReleaseWaveActive,
		ReleaseWavePaused,
		ReleaseWaveAborted,
		ReleaseWaveCompleted,
		ReleaseGatePending,
		ReleaseGateApproved,
		ReleaseGateRejected,
	).Scan(
		&totals.Batches,
		&totals.Rollouts,
		&totals.Planned,
		&totals.Active,
		&totals.Paused,
		&totals.Aborted,
		&totals.Completed,
		&totals.GatesPending,
		&totals.GatesApproved,
		&totals.GatesRejected,
	)
}

func loadMonitorFocusTasks(db *sql.DB, limit int) ([]*MonitorTask, error) {
	rows, err := db.Query(`
		SELECT task_id, goal, status, context_name, context_path, worker_id, worker_status, worker_phase,
		       worker_progress, review_status, promotion_status, release_batch_id, updated_at
		FROM tasks
		ORDER BY
			CASE status
				WHEN 'running' THEN 0
				WHEN 'blocked' THEN 1
				WHEN 'failed' THEN 2
				WHEN 'queued' THEN 3
				ELSE 4
			END,
			updated_at DESC
		LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("query monitor focus tasks: %w", err)
	}
	defer rows.Close()
	var tasks []*MonitorTask
	for rows.Next() {
		var task MonitorTask
		var contextName, contextPath, workerID, workerStatus, workerPhase, workerProgress sql.NullString
		var reviewStatus, promotionStatus, releaseBatchID sql.NullString
		var updatedAt string
		if err := rows.Scan(
			&task.ID,
			&task.Goal,
			&task.Status,
			&contextName,
			&contextPath,
			&workerID,
			&workerStatus,
			&workerPhase,
			&workerProgress,
			&reviewStatus,
			&promotionStatus,
			&releaseBatchID,
			&updatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan monitor focus task: %w", err)
		}
		task.ContextName = nullableStringValue(contextName)
		task.ContextPath = nullableStringValue(contextPath)
		task.WorkerID = nullableStringValue(workerID)
		task.WorkerStatus = nullableStringValue(workerStatus)
		task.WorkerPhase = nullableStringValue(workerPhase)
		task.WorkerProgress = nullableStringValue(workerProgress)
		task.ReviewStatus = nullableStringValue(reviewStatus)
		task.PromotionStatus = nullableStringValue(promotionStatus)
		task.ReleaseBatchID = nullableStringValue(releaseBatchID)
		task.UpdatedAt = parseMonitorTime(updatedAt)
		tasks = append(tasks, &task)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate monitor focus tasks: %w", err)
	}
	return tasks, nil
}

func loadMonitorRecentEvents(db *sql.DB, limit int) ([]*MonitorEvent, error) {
	rows, err := db.Query(`
		SELECT kind, entity_type, entity_id, task_id, batch_id, rollout_id, wave_order, status, gate_status, summary, recorded_at
		FROM events
		ORDER BY recorded_at DESC
		LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("query monitor recent events: %w", err)
	}
	defer rows.Close()
	var events []*MonitorEvent
	for rows.Next() {
		var event MonitorEvent
		var taskID, batchID, rolloutID, status, gateStatus, summary sql.NullString
		var recordedAt string
		if err := rows.Scan(
			&event.Kind,
			&event.EntityType,
			&event.EntityID,
			&taskID,
			&batchID,
			&rolloutID,
			&event.WaveOrder,
			&status,
			&gateStatus,
			&summary,
			&recordedAt,
		); err != nil {
			return nil, fmt.Errorf("scan monitor event: %w", err)
		}
		event.TaskID = nullableStringValue(taskID)
		event.BatchID = nullableStringValue(batchID)
		event.RolloutID = nullableStringValue(rolloutID)
		event.Status = nullableStringValue(status)
		event.GateStatus = nullableStringValue(gateStatus)
		event.Summary = nullableStringValue(summary)
		event.RecordedAt = parseMonitorTime(recordedAt)
		events = append(events, &event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate monitor recent events: %w", err)
	}
	return events, nil
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

func nullableStringValue(value sql.NullString) string {
	if !value.Valid {
		return ""
	}
	return strings.TrimSpace(value.String)
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
