package harness

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type TaskStatus string

const (
	TaskBlocked   TaskStatus = "blocked"
	TaskQueued    TaskStatus = "queued"
	TaskRunning   TaskStatus = "running"
	TaskCompleted TaskStatus = "completed"
	TaskFailed    TaskStatus = "failed"
	TaskAbandoned TaskStatus = "abandoned"
)

type Task struct {
	ID                     string     `json:"id"`
	Goal                   string     `json:"goal"`
	Status                 TaskStatus `json:"status"`
	DependsOn              []string   `json:"depends_on,omitempty"`
	ContextName            string     `json:"context_name,omitempty"`
	ContextPath            string     `json:"context_path,omitempty"`
	EntryPoint             string     `json:"entry_point,omitempty"`
	Attempt                int        `json:"attempt,omitempty"`
	LogPath                string     `json:"log_path,omitempty"`
	WorkspacePath          string     `json:"workspace_path,omitempty"`
	WorkspaceMode          string     `json:"workspace_mode,omitempty"`
	BranchName             string     `json:"branch_name,omitempty"`
	WorkerID               string     `json:"worker_id,omitempty"`
	WorkerStatus           string     `json:"worker_status,omitempty"`
	WorkerPhase            string     `json:"worker_phase,omitempty"`
	WorkerProgress         string     `json:"worker_progress,omitempty"`
	ChangedFiles           []string   `json:"changed_files,omitempty"`
	VerificationStatus     string     `json:"verification_status,omitempty"`
	VerificationReportPath string     `json:"verification_report_path,omitempty"`
	ReviewStatus           string     `json:"review_status,omitempty"`
	ReviewNotes            string     `json:"review_notes,omitempty"`
	ReviewedAt             *time.Time `json:"reviewed_at,omitempty"`
	PromotionStatus        string     `json:"promotion_status,omitempty"`
	PromotionNotes         string     `json:"promotion_notes,omitempty"`
	PromotedAt             *time.Time `json:"promoted_at,omitempty"`
	ReleaseBatchID         string     `json:"release_batch_id,omitempty"`
	ReleaseNotes           string     `json:"release_notes,omitempty"`
	ReleasedAt             *time.Time `json:"released_at,omitempty"`
	ExitCode               int        `json:"exit_code,omitempty"`
	Error                  string     `json:"error,omitempty"`
	CreatedAt              time.Time  `json:"created_at"`
	UpdatedAt              time.Time  `json:"updated_at"`
	StartedAt              *time.Time `json:"started_at,omitempty"`
	FinishedAt             *time.Time `json:"finished_at,omitempty"`
}

func NewTask(goal, entryPoint string) (*Task, error) {
	id, err := randomID()
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	return &Task{
		ID:         id,
		Goal:       strings.TrimSpace(goal),
		Status:     TaskQueued,
		EntryPoint: strings.TrimSpace(entryPoint),
		CreatedAt:  now,
		UpdatedAt:  now,
	}, nil
}

type QueueOptions struct {
	DependsOn   []string
	ContextName string
	ContextPath string
}

func EnqueueTask(project Project, goal, entryPoint string, opts ...QueueOptions) (*Task, error) {
	task, err := NewTask(goal, entryPoint)
	if err != nil {
		return nil, err
	}
	if len(opts) > 0 {
		task.DependsOn = append([]string(nil), opts[0].DependsOn...)
		task.ContextName = strings.TrimSpace(opts[0].ContextName)
		task.ContextPath = filepath.Clean(strings.TrimSpace(opts[0].ContextPath))
		if task.ContextPath == "." {
			task.ContextPath = ""
		}
	}
	if err := refreshTaskStatus(project, task); err != nil {
		return nil, err
	}
	if err := SaveTask(project, task); err != nil {
		return nil, err
	}
	return task, nil
}

func SaveTask(project Project, task *Task) error {
	if task == nil {
		return fmt.Errorf("nil task")
	}
	previous, err := loadTaskSnapshot(taskPath(project, task.ID))
	if err != nil {
		return err
	}
	task.UpdatedAt = time.Now().UTC()
	data, err := json.MarshalIndent(task, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal task: %w", err)
	}
	if err := os.MkdirAll(project.TasksDir, 0755); err != nil {
		return fmt.Errorf("create tasks dir: %w", err)
	}
	if err := os.WriteFile(taskPath(project, task.ID), data, 0644); err != nil {
		return err
	}
	if err := recordTaskEvent(project, previous, task); err != nil {
		return fmt.Errorf("record task event: %w", err)
	}
	return nil
}

func LoadTask(project Project, id string) (*Task, error) {
	data, err := os.ReadFile(taskPath(project, id))
	if err != nil {
		return nil, fmt.Errorf("read task %s: %w", id, err)
	}
	var task Task
	if err := json.Unmarshal(data, &task); err != nil {
		return nil, fmt.Errorf("parse task %s: %w", id, err)
	}
	return &task, nil
}

func ListTasks(project Project) ([]*Task, error) {
	entries, err := os.ReadDir(project.TasksDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read tasks dir: %w", err)
	}
	var tasks []*Task
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		task, err := LoadTask(project, strings.TrimSuffix(entry.Name(), ".json"))
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, task)
	}
	if err := refreshTaskStatuses(project, tasks); err != nil {
		return nil, err
	}
	sort.Slice(tasks, func(i, j int) bool {
		return tasks[i].CreatedAt.After(tasks[j].CreatedAt)
	})
	return tasks, nil
}

func NextQueuedTask(project Project) (*Task, error) {
	tasks, err := ListTasks(project)
	if err != nil {
		return nil, err
	}
	var next *Task
	for _, task := range tasks {
		if task.Status != TaskQueued {
			continue
		}
		if next == nil || task.CreatedAt.Before(next.CreatedAt) {
			next = task
		}
	}
	return next, nil
}

func NextRunnableTask(project Project, cfg *Config, opts QueueRunOptions) (*Task, error) {
	tasks, err := ListTasks(project)
	if err != nil {
		return nil, err
	}
	var next *Task
	for _, task := range tasks {
		if !taskIsRunnable(task, cfg, opts) {
			continue
		}
		if next == nil || task.CreatedAt.Before(next.CreatedAt) {
			next = task
		}
	}
	return next, nil
}

func taskIsRunnable(task *Task, cfg *Config, opts QueueRunOptions) bool {
	if task == nil {
		return false
	}
	if !ownerMatches(cfg, task, opts.Owner) {
		return false
	}
	switch task.Status {
	case TaskQueued:
		return true
	case TaskFailed:
		return opts.RetryFailed && task.Attempt < maxTaskAttempts(cfg)
	case TaskRunning:
		return opts.ResumeInterrupted
	default:
		return false
	}
}

func maxTaskAttempts(cfg *Config) int {
	if cfg == nil || cfg.Run.MaxAttempts <= 0 {
		return 3
	}
	return cfg.Run.MaxAttempts
}

func refreshTaskStatuses(project Project, tasks []*Task) error {
	index := make(map[string]*Task, len(tasks))
	for _, task := range tasks {
		if task == nil {
			continue
		}
		index[task.ID] = task
	}
	for _, task := range tasks {
		if task == nil {
			continue
		}
		before := task.Status
		if err := refreshTaskStatusWithIndex(project, task, index); err != nil {
			return err
		}
		if task.Status != before {
			if err := SaveTask(project, task); err != nil {
				return err
			}
		}
	}
	return nil
}

func refreshTaskStatus(project Project, task *Task) error {
	tasks, err := ListTasks(project)
	if err != nil {
		return err
	}
	index := make(map[string]*Task, len(tasks))
	for _, item := range tasks {
		if item == nil {
			continue
		}
		index[item.ID] = item
	}
	return refreshTaskStatusWithIndex(project, task, index)
}

func refreshTaskStatusWithIndex(project Project, task *Task, index map[string]*Task) error {
	if task == nil {
		return nil
	}
	switch task.Status {
	case TaskRunning, TaskCompleted, TaskFailed, TaskAbandoned:
		return nil
	}
	if len(task.DependsOn) == 0 {
		task.Status = TaskQueued
		return nil
	}
	blocked := false
	for _, depID := range task.DependsOn {
		depID = strings.TrimSpace(depID)
		if depID == "" {
			continue
		}
		dep := index[depID]
		if dep == nil {
			loaded, err := LoadTask(project, depID)
			if err != nil {
				blocked = true
				continue
			}
			dep = loaded
			index[depID] = dep
		}
		if dep.Status != TaskCompleted {
			blocked = true
		}
	}
	if blocked {
		task.Status = TaskBlocked
		return nil
	}
	task.Status = TaskQueued
	return nil
}

func taskPath(project Project, id string) string {
	return filepath.Join(project.TasksDir, id+".json")
}

func randomID() (string, error) {
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", fmt.Errorf("generate task id: %w", err)
	}
	return hex.EncodeToString(buf[:]), nil
}

func FormatTaskList(tasks []*Task) string {
	if len(tasks) == 0 {
		return "No harness tasks recorded."
	}
	var b strings.Builder
	b.WriteString("Harness tasks:\n")
	for _, task := range tasks {
		if task == nil {
			continue
		}
		b.WriteString(fmt.Sprintf("- %s [%s] %s\n", task.ID, task.Status, task.Goal))
		if task.Attempt > 0 {
			b.WriteString(fmt.Sprintf("  attempts: %d\n", task.Attempt))
		}
		if len(task.DependsOn) > 0 {
			b.WriteString(fmt.Sprintf("  depends_on: %s\n", strings.Join(task.DependsOn, ", ")))
		}
		if task.ContextName != "" || task.ContextPath != "" {
			label := firstNonEmptyText(task.ContextName, task.ContextPath)
			b.WriteString(fmt.Sprintf("  context: %s\n", label))
		}
		if task.WorkspacePath != "" {
			b.WriteString(fmt.Sprintf("  workspace: %s\n", task.WorkspacePath))
		}
		if task.BranchName != "" {
			b.WriteString(fmt.Sprintf("  branch: %s\n", task.BranchName))
		}
		if task.WorkerID != "" {
			status := task.WorkerStatus
			if strings.TrimSpace(status) == "" {
				status = "unknown"
			}
			b.WriteString(fmt.Sprintf("  worker: %s [%s]\n", task.WorkerID, status))
		}
		if task.WorkerProgress != "" {
			b.WriteString(fmt.Sprintf("  progress: %s\n", task.WorkerProgress))
		}
		if task.VerificationStatus != "" {
			b.WriteString(fmt.Sprintf("  verification: %s\n", task.VerificationStatus))
		}
		if len(task.ChangedFiles) > 0 {
			b.WriteString(fmt.Sprintf("  changed_files: %d\n", len(task.ChangedFiles)))
		}
		if task.VerificationReportPath != "" {
			b.WriteString(fmt.Sprintf("  delivery_report: %s\n", task.VerificationReportPath))
		}
		if task.ReviewStatus != "" {
			b.WriteString(fmt.Sprintf("  review: %s\n", task.ReviewStatus))
		}
		if task.ReviewNotes != "" {
			b.WriteString(fmt.Sprintf("  review_notes: %s\n", task.ReviewNotes))
		}
		if task.PromotionStatus != "" {
			b.WriteString(fmt.Sprintf("  promotion: %s\n", task.PromotionStatus))
		}
		if task.PromotionNotes != "" {
			b.WriteString(fmt.Sprintf("  promotion_notes: %s\n", task.PromotionNotes))
		}
		if task.ReleaseBatchID != "" {
			b.WriteString(fmt.Sprintf("  release_batch: %s\n", task.ReleaseBatchID))
		}
		if task.ReleaseNotes != "" {
			b.WriteString(fmt.Sprintf("  release_notes: %s\n", task.ReleaseNotes))
		}
		if task.Error != "" {
			b.WriteString(fmt.Sprintf("  error: %s\n", task.Error))
		}
	}
	return strings.TrimRight(b.String(), "\n")
}
