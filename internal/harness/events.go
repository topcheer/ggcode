package harness

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
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
	if err := appendHarnessEvent(project, event); err != nil {
		return err
	}
	return writeTaskSnapshot(project, current)
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
	if err := appendHarnessEvent(project, event); err != nil {
		return err
	}
	return writeReleasePlanSnapshot(project, current)
}

// --- Event log (JSONL) ---

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
	// Create snapshot directories.
	if err := os.MkdirAll(taskSnapshotDir(project), 0o755); err != nil {
		return fmt.Errorf("create task snapshot dir: %w", err)
	}
	if err := os.MkdirAll(releaseSnapshotDir(project), 0o755); err != nil {
		return fmt.Errorf("create release snapshot dir: %w", err)
	}
	return nil
}

// --- Snapshot paths ---

func taskSnapshotDir(project Project) string {
	return filepath.Join(project.StateDir, "snapshots", "tasks")
}

func releaseSnapshotDir(project Project) string {
	return filepath.Join(project.StateDir, "snapshots", "releases")
}

func taskSnapshotPath(project Project, taskID string) string {
	return filepath.Join(taskSnapshotDir(project), taskID+".json")
}

func releaseSnapshotPath(project Project, batchID string) string {
	return filepath.Join(releaseSnapshotDir(project), batchID+".json")
}

// --- Snapshot writes ---

func writeTaskSnapshot(project Project, task *Task) error {
	dir := taskSnapshotDir(project)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create task snapshot dir: %w", err)
	}
	data, err := json.Marshal(task)
	if err != nil {
		return fmt.Errorf("marshal task snapshot: %w", err)
	}
	path := taskSnapshotPath(project, task.ID)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write task snapshot %s: %w", task.ID, err)
	}
	return nil
}

func writeReleasePlanSnapshot(project Project, plan *ReleasePlan) error {
	dir := releaseSnapshotDir(project)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create release snapshot dir: %w", err)
	}
	data, err := json.Marshal(plan)
	if err != nil {
		return fmt.Errorf("marshal release snapshot: %w", err)
	}
	path := releaseSnapshotPath(project, plan.BatchID)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write release snapshot %s: %w", plan.BatchID, err)
	}
	return nil
}

// --- Snapshot reads ---

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

// loadAllTaskSnapshots reads all task snapshot JSON files from the project's
// snapshot directory.
func loadAllTaskSnapshots(project Project) ([]*Task, error) {
	dir := taskSnapshotDir(project)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read task snapshot dir: %w", err)
	}
	var tasks []*Task
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		task, err := loadTaskSnapshot(filepath.Join(dir, entry.Name()))
		if err != nil {
			return nil, err
		}
		if task != nil {
			tasks = append(tasks, task)
		}
	}
	return tasks, nil
}

// loadAllReleasePlanSnapshots reads all release plan snapshot JSON files.
func loadAllReleasePlanSnapshots(project Project) ([]*ReleasePlan, error) {
	dir := releaseSnapshotDir(project)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read release snapshot dir: %w", err)
	}
	var plans []*ReleasePlan
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		plan, err := loadReleasePlanSnapshot(filepath.Join(dir, entry.Name()))
		if err != nil {
			return nil, err
		}
		if plan != nil {
			plans = append(plans, plan)
		}
	}
	return plans, nil
}

// loadRecentEventsFromJSONL reads the last N events from the event JSONL file.
func loadRecentEventsFromJSONL(eventLogPath string, limit int) ([]*MonitorEvent, error) {
	f, err := os.Open(eventLogPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("open event log: %w", err)
	}
	defer f.Close()

	// Read all events, then take the last N.
	var all []*MonitorEvent
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var evt harnessEvent
		if err := json.Unmarshal([]byte(line), &evt); err != nil {
			continue // skip malformed lines
		}
		all = append(all, &MonitorEvent{
			Kind:       evt.Kind,
			EntityType: evt.EntityType,
			EntityID:   evt.EntityID,
			TaskID:     evt.TaskID,
			BatchID:    evt.BatchID,
			RolloutID:  evt.RolloutID,
			WaveOrder:  evt.WaveOrder,
			Status:     evt.Status,
			GateStatus: evt.GateStatus,
			Summary:    evt.Summary,
			RecordedAt: evt.RecordedAt,
		})
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan event log: %w", err)
	}

	// Take last N in reverse.
	if len(all) <= limit {
		// Reverse in place.
		for i, j := 0, len(all)-1; i < j; i, j = i+1, j-1 {
			all[i], all[j] = all[j], all[i]
		}
		return all, nil
	}
	result := make([]*MonitorEvent, limit)
	for i := 0; i < limit; i++ {
		result[i] = all[len(all)-1-i]
	}
	return result, nil
}

// --- Event classification helpers ---

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

func timesEqual(left, right *time.Time) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return left.UTC().Equal(right.UTC())
}
