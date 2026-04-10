package harness

import (
	"bufio"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const (
	eventTaskCreated           = "task.created"
	eventTaskUpdated           = "task.updated"
	eventTaskStatusChanged     = "task.status_changed"
	eventTaskWorkerChanged     = "task.worker_changed"
	eventTaskReviewChanged     = "task.review_changed"
	eventTaskPromotionChanged  = "task.promotion_changed"
	eventTaskReleaseChanged    = "task.release_changed"
	eventReleasePersisted      = "release.persisted"
	eventRolloutWavePersisted  = "rollout.wave_persisted"
	eventRolloutWaveStatus     = "rollout.wave_status_changed"
	eventRolloutWaveGateStatus = "rollout.gate_changed"
)

type harnessEvent struct {
	ID             string          `json:"id"`
	Kind           string          `json:"kind"`
	EntityType     string          `json:"entity_type"`
	EntityID       string          `json:"entity_id"`
	TaskID         string          `json:"task_id,omitempty"`
	BatchID        string          `json:"batch_id,omitempty"`
	RolloutID      string          `json:"rollout_id,omitempty"`
	WaveOrder      int             `json:"wave_order,omitempty"`
	Status         string          `json:"status,omitempty"`
	PreviousStatus string          `json:"previous_status,omitempty"`
	GateStatus     string          `json:"gate_status,omitempty"`
	RecordedAt     time.Time       `json:"recorded_at"`
	Summary        string          `json:"summary,omitempty"`
	Payload        json.RawMessage `json:"payload,omitempty"`
}

func recordTaskEvent(project Project, previous, current *Task) error {
	if current == nil {
		return fmt.Errorf("missing task event payload")
	}
	eventID, err := randomID()
	if err != nil {
		return fmt.Errorf("generate task event id: %w", err)
	}
	payload, err := json.Marshal(struct {
		Previous *Task `json:"previous,omitempty"`
		Current  *Task `json:"current"`
	}{
		Previous: previous,
		Current:  current,
	})
	if err != nil {
		return fmt.Errorf("marshal task event payload: %w", err)
	}
	event := harnessEvent{
		ID:             eventID,
		Kind:           classifyTaskEvent(previous, current),
		EntityType:     "task",
		EntityID:       current.ID,
		TaskID:         current.ID,
		Status:         string(current.Status),
		PreviousStatus: taskStatusValue(previous),
		RecordedAt:     current.UpdatedAt,
		Summary:        summarizeTaskEvent(previous, current),
		Payload:        payload,
	}
	return persistHarnessEvent(project, event, func(tx *sql.Tx) error {
		return upsertTaskSnapshot(tx, current)
	})
}

func recordReleasePlanEvent(project Project, previous, current *ReleasePlan) error {
	if current == nil {
		return fmt.Errorf("missing release plan event payload")
	}
	eventID, err := randomID()
	if err != nil {
		return fmt.Errorf("generate release event id: %w", err)
	}
	payload, err := json.Marshal(struct {
		Previous *ReleasePlan `json:"previous,omitempty"`
		Current  *ReleasePlan `json:"current"`
	}{
		Previous: previous,
		Current:  current,
	})
	if err != nil {
		return fmt.Errorf("marshal release event payload: %w", err)
	}
	event := harnessEvent{
		ID:             eventID,
		Kind:           classifyReleaseEvent(previous, current),
		EntityType:     "release_plan",
		EntityID:       current.BatchID,
		BatchID:        current.BatchID,
		RolloutID:      strings.TrimSpace(current.RolloutID),
		WaveOrder:      current.WaveOrder,
		Status:         strings.TrimSpace(current.WaveStatus),
		PreviousStatus: releaseWaveStatusValue(previous),
		GateStatus:     strings.TrimSpace(current.GateStatus),
		RecordedAt:     releaseEventTime(current),
		Summary:        summarizeReleaseEvent(previous, current),
		Payload:        payload,
	}
	return persistHarnessEvent(project, event, func(tx *sql.Tx) error {
		return upsertReleasePlanSnapshot(tx, current)
	})
}

func persistHarnessEvent(project Project, event harnessEvent, apply func(tx *sql.Tx) error) error {
	if err := appendHarnessEvent(project, event); err != nil {
		return err
	}
	db, err := openHarnessSnapshot(project)
	if err != nil {
		return err
	}
	defer db.Close()
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin harness snapshot transaction: %w", err)
	}
	if err := insertHarnessEvent(tx, event); err != nil {
		_ = tx.Rollback()
		return err
	}
	if apply != nil {
		if err := apply(tx); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit harness snapshot transaction: %w", err)
	}
	return nil
}

func appendHarnessEvent(project Project, event harnessEvent) error {
	if err := os.MkdirAll(filepath.Dir(project.EventLogPath), 0o755); err != nil {
		return fmt.Errorf("create harness state dir: %w", err)
	}
	f, err := os.OpenFile(project.EventLogPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("open harness event log: %w", err)
	}
	defer f.Close()
	writer := bufio.NewWriter(f)
	data, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshal harness event: %w", err)
	}
	if _, err := writer.Write(data); err != nil {
		return fmt.Errorf("write harness event: %w", err)
	}
	if err := writer.WriteByte('\n'); err != nil {
		return fmt.Errorf("terminate harness event: %w", err)
	}
	if err := writer.Flush(); err != nil {
		return fmt.Errorf("flush harness event log: %w", err)
	}
	return nil
}

func bootstrapHarnessState(project Project) error {
	if err := os.MkdirAll(filepath.Dir(project.EventLogPath), 0o755); err != nil {
		return fmt.Errorf("create harness state dir: %w", err)
	}
	eventLog, err := os.OpenFile(project.EventLogPath, os.O_CREATE, 0o644)
	if err != nil {
		return fmt.Errorf("create harness event log: %w", err)
	}
	if err := eventLog.Close(); err != nil {
		return fmt.Errorf("close harness event log: %w", err)
	}
	db, err := openHarnessSnapshot(project)
	if err != nil {
		return err
	}
	if err := db.Close(); err != nil {
		return fmt.Errorf("close harness snapshot: %w", err)
	}
	return nil
}

func openHarnessSnapshot(project Project) (*sql.DB, error) {
	if err := os.MkdirAll(filepath.Dir(project.SnapshotPath), 0o755); err != nil {
		return nil, fmt.Errorf("create harness snapshot dir: %w", err)
	}
	db, err := sql.Open("sqlite", project.SnapshotPath)
	if err != nil {
		return nil, fmt.Errorf("open harness snapshot: %w", err)
	}
	stmts := []string{
		`PRAGMA busy_timeout = 5000;`,
		`CREATE TABLE IF NOT EXISTS events (
			id TEXT PRIMARY KEY,
			kind TEXT NOT NULL,
			entity_type TEXT NOT NULL,
			entity_id TEXT NOT NULL,
			task_id TEXT,
			batch_id TEXT,
			rollout_id TEXT,
			wave_order INTEGER,
			status TEXT,
			previous_status TEXT,
			gate_status TEXT,
			recorded_at TEXT NOT NULL,
			summary TEXT,
			payload_json TEXT
		);`,
		`CREATE TABLE IF NOT EXISTS tasks (
			task_id TEXT PRIMARY KEY,
			goal TEXT NOT NULL,
			status TEXT NOT NULL,
			depends_on_json TEXT,
			context_name TEXT,
			context_path TEXT,
			entry_point TEXT,
			attempt INTEGER,
			log_path TEXT,
			workspace_path TEXT,
			workspace_mode TEXT,
			branch_name TEXT,
			worker_id TEXT,
			worker_status TEXT,
			worker_phase TEXT,
			worker_progress TEXT,
			changed_files_json TEXT,
			verification_status TEXT,
			verification_report_path TEXT,
			review_status TEXT,
			review_notes TEXT,
			reviewed_at TEXT,
			promotion_status TEXT,
			promotion_notes TEXT,
			promoted_at TEXT,
			release_batch_id TEXT,
			release_notes TEXT,
			released_at TEXT,
			exit_code INTEGER,
			error_text TEXT,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			started_at TEXT,
			finished_at TEXT
		);`,
		`CREATE TABLE IF NOT EXISTS release_plans (
			batch_id TEXT PRIMARY KEY,
			rollout_id TEXT,
			group_by TEXT,
			group_label TEXT,
			wave_order INTEGER,
			wave_status TEXT,
			gate_status TEXT,
			status_note TEXT,
			gate_note TEXT,
			activated_at TEXT,
			gate_checked_at TEXT,
			paused_at TEXT,
			aborted_at TEXT,
			completed_at TEXT,
			generated_at TEXT NOT NULL,
			environment TEXT,
			owner_filter TEXT,
			context_filter TEXT,
			task_count INTEGER NOT NULL,
			owners_json TEXT,
			contexts_json TEXT,
			report_path TEXT,
			updated_at TEXT NOT NULL
		);`,
	}
	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil {
			db.Close()
			return nil, fmt.Errorf("prepare harness snapshot schema: %w", err)
		}
	}
	return db, nil
}

func insertHarnessEvent(tx *sql.Tx, event harnessEvent) error {
	_, err := tx.Exec(
		`INSERT INTO events (
			id, kind, entity_type, entity_id, task_id, batch_id, rollout_id, wave_order,
			status, previous_status, gate_status, recorded_at, summary, payload_json
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		event.ID,
		event.Kind,
		event.EntityType,
		event.EntityID,
		nullableText(event.TaskID),
		nullableText(event.BatchID),
		nullableText(event.RolloutID),
		event.WaveOrder,
		nullableText(event.Status),
		nullableText(event.PreviousStatus),
		nullableText(event.GateStatus),
		event.RecordedAt.UTC().Format(time.RFC3339Nano),
		nullableText(event.Summary),
		nullableText(string(event.Payload)),
	)
	if err != nil {
		return fmt.Errorf("insert harness event: %w", err)
	}
	return nil
}

func upsertTaskSnapshot(tx *sql.Tx, task *Task) error {
	_, err := tx.Exec(
		`INSERT INTO tasks (
			task_id, goal, status, depends_on_json, context_name, context_path, entry_point, attempt,
			log_path, workspace_path, workspace_mode, branch_name, worker_id, worker_status, worker_phase,
			worker_progress, changed_files_json, verification_status, verification_report_path, review_status,
			review_notes, reviewed_at, promotion_status, promotion_notes, promoted_at, release_batch_id,
			release_notes, released_at, exit_code, error_text, created_at, updated_at, started_at, finished_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(task_id) DO UPDATE SET
			goal = excluded.goal,
			status = excluded.status,
			depends_on_json = excluded.depends_on_json,
			context_name = excluded.context_name,
			context_path = excluded.context_path,
			entry_point = excluded.entry_point,
			attempt = excluded.attempt,
			log_path = excluded.log_path,
			workspace_path = excluded.workspace_path,
			workspace_mode = excluded.workspace_mode,
			branch_name = excluded.branch_name,
			worker_id = excluded.worker_id,
			worker_status = excluded.worker_status,
			worker_phase = excluded.worker_phase,
			worker_progress = excluded.worker_progress,
			changed_files_json = excluded.changed_files_json,
			verification_status = excluded.verification_status,
			verification_report_path = excluded.verification_report_path,
			review_status = excluded.review_status,
			review_notes = excluded.review_notes,
			reviewed_at = excluded.reviewed_at,
			promotion_status = excluded.promotion_status,
			promotion_notes = excluded.promotion_notes,
			promoted_at = excluded.promoted_at,
			release_batch_id = excluded.release_batch_id,
			release_notes = excluded.release_notes,
			released_at = excluded.released_at,
			exit_code = excluded.exit_code,
			error_text = excluded.error_text,
			created_at = excluded.created_at,
			updated_at = excluded.updated_at,
			started_at = excluded.started_at,
			finished_at = excluded.finished_at`,
		task.ID,
		task.Goal,
		string(task.Status),
		marshalSnapshotJSON(task.DependsOn),
		nullableText(task.ContextName),
		nullableText(task.ContextPath),
		nullableText(task.EntryPoint),
		task.Attempt,
		nullableText(task.LogPath),
		nullableText(task.WorkspacePath),
		nullableText(task.WorkspaceMode),
		nullableText(task.BranchName),
		nullableText(task.WorkerID),
		nullableText(task.WorkerStatus),
		nullableText(task.WorkerPhase),
		nullableText(task.WorkerProgress),
		marshalSnapshotJSON(task.ChangedFiles),
		nullableText(task.VerificationStatus),
		nullableText(task.VerificationReportPath),
		nullableText(task.ReviewStatus),
		nullableText(task.ReviewNotes),
		nullableTime(task.ReviewedAt),
		nullableText(task.PromotionStatus),
		nullableText(task.PromotionNotes),
		nullableTime(task.PromotedAt),
		nullableText(task.ReleaseBatchID),
		nullableText(task.ReleaseNotes),
		nullableTime(task.ReleasedAt),
		task.ExitCode,
		nullableText(task.Error),
		task.CreatedAt.UTC().Format(time.RFC3339Nano),
		task.UpdatedAt.UTC().Format(time.RFC3339Nano),
		nullableTime(task.StartedAt),
		nullableTime(task.FinishedAt),
	)
	if err != nil {
		return fmt.Errorf("upsert task snapshot: %w", err)
	}
	return nil
}

func upsertReleasePlanSnapshot(tx *sql.Tx, plan *ReleasePlan) error {
	_, err := tx.Exec(
		`INSERT INTO release_plans (
			batch_id, rollout_id, group_by, group_label, wave_order, wave_status, gate_status, status_note,
			gate_note, activated_at, gate_checked_at, paused_at, aborted_at, completed_at, generated_at,
			environment, owner_filter, context_filter, task_count, owners_json, contexts_json, report_path, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(batch_id) DO UPDATE SET
			rollout_id = excluded.rollout_id,
			group_by = excluded.group_by,
			group_label = excluded.group_label,
			wave_order = excluded.wave_order,
			wave_status = excluded.wave_status,
			gate_status = excluded.gate_status,
			status_note = excluded.status_note,
			gate_note = excluded.gate_note,
			activated_at = excluded.activated_at,
			gate_checked_at = excluded.gate_checked_at,
			paused_at = excluded.paused_at,
			aborted_at = excluded.aborted_at,
			completed_at = excluded.completed_at,
			generated_at = excluded.generated_at,
			environment = excluded.environment,
			owner_filter = excluded.owner_filter,
			context_filter = excluded.context_filter,
			task_count = excluded.task_count,
			owners_json = excluded.owners_json,
			contexts_json = excluded.contexts_json,
			report_path = excluded.report_path,
			updated_at = excluded.updated_at`,
		plan.BatchID,
		nullableText(plan.RolloutID),
		nullableText(plan.GroupBy),
		nullableText(plan.GroupLabel),
		plan.WaveOrder,
		nullableText(plan.WaveStatus),
		nullableText(plan.GateStatus),
		nullableText(plan.StatusNote),
		nullableText(plan.GateNote),
		nullableTime(plan.ActivatedAt),
		nullableTime(plan.GateCheckedAt),
		nullableTime(plan.PausedAt),
		nullableTime(plan.AbortedAt),
		nullableTime(plan.CompletedAt),
		plan.GeneratedAt.UTC().Format(time.RFC3339Nano),
		nullableText(plan.Environment),
		nullableText(plan.OwnerFilter),
		nullableText(plan.ContextFilter),
		len(plan.Tasks),
		marshalSnapshotJSON(plan.Owners),
		marshalSnapshotJSON(plan.Contexts),
		nullableText(plan.ReportPath),
		releaseEventTime(plan).UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("upsert release snapshot: %w", err)
	}
	return nil
}

func loadTaskSnapshot(path string) (*Task, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read task snapshot %s: %w", path, err)
	}
	var task Task
	if err := json.Unmarshal(data, &task); err != nil {
		return nil, fmt.Errorf("decode task snapshot %s: %w", path, err)
	}
	return &task, nil
}

func loadReleasePlanSnapshot(path string) (*ReleasePlan, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read release plan snapshot %s: %w", path, err)
	}
	var plan ReleasePlan
	if err := json.Unmarshal(data, &plan); err != nil {
		return nil, fmt.Errorf("decode release plan snapshot %s: %w", path, err)
	}
	plan.ReportPath = path
	return &plan, nil
}

func classifyTaskEvent(previous, current *Task) string {
	switch {
	case previous == nil:
		return eventTaskCreated
	case previous.Status != current.Status:
		return eventTaskStatusChanged
	case previous.WorkerID != current.WorkerID || previous.WorkerStatus != current.WorkerStatus ||
		previous.WorkerPhase != current.WorkerPhase || previous.WorkerProgress != current.WorkerProgress:
		return eventTaskWorkerChanged
	case previous.ReviewStatus != current.ReviewStatus || previous.ReviewNotes != current.ReviewNotes || !timesEqual(previous.ReviewedAt, current.ReviewedAt):
		return eventTaskReviewChanged
	case previous.PromotionStatus != current.PromotionStatus || previous.PromotionNotes != current.PromotionNotes || !timesEqual(previous.PromotedAt, current.PromotedAt):
		return eventTaskPromotionChanged
	case previous.ReleaseBatchID != current.ReleaseBatchID || previous.ReleaseNotes != current.ReleaseNotes || !timesEqual(previous.ReleasedAt, current.ReleasedAt):
		return eventTaskReleaseChanged
	default:
		return eventTaskUpdated
	}
}

func summarizeTaskEvent(previous, current *Task) string {
	switch classifyTaskEvent(previous, current) {
	case eventTaskCreated:
		return fmt.Sprintf("task %s created with status %s", current.ID, current.Status)
	case eventTaskStatusChanged:
		return fmt.Sprintf("task %s status %s -> %s", current.ID, taskStatusValue(previous), current.Status)
	case eventTaskWorkerChanged:
		return fmt.Sprintf("task %s worker %s/%s", current.ID, firstNonEmptyText(current.WorkerStatus, "unknown"), firstNonEmptyText(current.WorkerPhase, "idle"))
	case eventTaskReviewChanged:
		return fmt.Sprintf("task %s review %s", current.ID, firstNonEmptyText(current.ReviewStatus, "updated"))
	case eventTaskPromotionChanged:
		return fmt.Sprintf("task %s promotion %s", current.ID, firstNonEmptyText(current.PromotionStatus, "updated"))
	case eventTaskReleaseChanged:
		return fmt.Sprintf("task %s release batch %s", current.ID, firstNonEmptyText(current.ReleaseBatchID, "updated"))
	default:
		return fmt.Sprintf("task %s updated", current.ID)
	}
}

func classifyReleaseEvent(previous, current *ReleasePlan) string {
	hasRollout := strings.TrimSpace(current.RolloutID) != ""
	switch {
	case !hasRollout:
		return eventReleasePersisted
	case previous == nil:
		return eventRolloutWavePersisted
	case previous.GateStatus != current.GateStatus || previous.GateNote != current.GateNote || !timesEqual(previous.GateCheckedAt, current.GateCheckedAt):
		return eventRolloutWaveGateStatus
	case previous.WaveStatus != current.WaveStatus || previous.StatusNote != current.StatusNote ||
		!timesEqual(previous.ActivatedAt, current.ActivatedAt) || !timesEqual(previous.PausedAt, current.PausedAt) ||
		!timesEqual(previous.AbortedAt, current.AbortedAt) || !timesEqual(previous.CompletedAt, current.CompletedAt):
		return eventRolloutWaveStatus
	default:
		return eventRolloutWavePersisted
	}
}

func summarizeReleaseEvent(previous, current *ReleasePlan) string {
	switch classifyReleaseEvent(previous, current) {
	case eventRolloutWaveGateStatus:
		return fmt.Sprintf("rollout %s wave %d gate %s -> %s", current.RolloutID, current.WaveOrder, releaseGateStatusValue(previous), firstNonEmptyText(current.GateStatus, "unknown"))
	case eventRolloutWaveStatus:
		return fmt.Sprintf("rollout %s wave %d status %s -> %s", current.RolloutID, current.WaveOrder, releaseWaveStatusValue(previous), firstNonEmptyText(current.WaveStatus, "unknown"))
	case eventRolloutWavePersisted:
		return fmt.Sprintf("rollout %s wave %d persisted", current.RolloutID, current.WaveOrder)
	default:
		return fmt.Sprintf("release batch %s persisted", current.BatchID)
	}
}

func releaseEventTime(plan *ReleasePlan) time.Time {
	for _, ts := range []*time.Time{plan.CompletedAt, plan.AbortedAt, plan.PausedAt, plan.GateCheckedAt, plan.ActivatedAt} {
		if ts != nil {
			return ts.UTC()
		}
	}
	return plan.GeneratedAt.UTC()
}

func taskStatusValue(task *Task) string {
	if task == nil {
		return ""
	}
	return string(task.Status)
}

func releaseWaveStatusValue(plan *ReleasePlan) string {
	if plan == nil {
		return ""
	}
	return strings.TrimSpace(plan.WaveStatus)
}

func releaseGateStatusValue(plan *ReleasePlan) string {
	if plan == nil {
		return ""
	}
	return strings.TrimSpace(plan.GateStatus)
}

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

func timesEqual(left, right *time.Time) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return left.UTC().Equal(right.UTC())
}
