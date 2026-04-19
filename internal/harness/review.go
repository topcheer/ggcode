package harness

import (
	"fmt"
	"strings"
	"time"
)

const (
	ReviewPending  = "pending"
	ReviewApproved = "approved"
	ReviewRejected = "rejected"
)

func ListReviewableTasks(project Project) ([]*Task, error) {
	tasks, err := ListTasks(project)
	if err != nil {
		return nil, err
	}
	var ready []*Task
	for _, task := range tasks {
		if !taskReviewReady(task) {
			continue
		}
		ready = append(ready, task)
	}
	return ready, nil
}

func taskReviewReady(task *Task) bool {
	if task == nil {
		return false
	}
	return task.Status == TaskCompleted && task.VerificationStatus == VerificationPassed && task.ReviewStatus != ReviewApproved
}

func ApproveTaskReview(project Project, id, note string) (*Task, error) {
	task, err := LoadTask(project, id)
	if err != nil {
		return nil, err
	}
	if !taskReviewReady(task) {
		return nil, fmt.Errorf("task %s is not ready for review approval", id)
	}
	task.ReviewStatus = ReviewApproved
	task.ReviewNotes = strings.TrimSpace(note)
	now := time.Now().UTC()
	task.ReviewedAt = &now
	if err := SaveTask(project, task); err != nil {
		return nil, err
	}
	return task, nil
}

func RejectTaskReview(project Project, id, note string) (*Task, error) {
	task, err := LoadTask(project, id)
	if err != nil {
		return nil, err
	}
	if !taskReviewReady(task) {
		return nil, fmt.Errorf("task %s is not ready for review rejection", id)
	}
	task.ReviewStatus = ReviewRejected
	task.ReviewNotes = strings.TrimSpace(note)
	now := time.Now().UTC()
	task.ReviewedAt = &now
	task.Status = TaskFailed
	task.Error = "review rejected"
	if task.ReviewNotes != "" {
		task.Error = fmt.Sprintf("review rejected: %s", task.ReviewNotes)
	}
	if err := SaveTask(project, task); err != nil {
		return nil, err
	}
	return task, nil
}

func FormatReviewList(tasks []*Task) string {
	if len(tasks) == 0 {
		return "No harness tasks are waiting for review."
	}
	var b strings.Builder
	b.WriteString("Harness review queue:\n")
	for _, task := range tasks {
		if task == nil {
			continue
		}
		b.WriteString(fmt.Sprintf("- %s %s\n", task.ID, task.Goal))
		if task.BranchName != "" {
			b.WriteString(fmt.Sprintf("  branch: %s\n", task.BranchName))
		}
		if len(task.ChangedFiles) > 0 {
			b.WriteString(fmt.Sprintf("  changed_files: %d\n", len(task.ChangedFiles)))
		}
		if task.VerificationReportPath != "" {
			b.WriteString(fmt.Sprintf("  delivery_report: %s\n", task.VerificationReportPath))
		}
	}
	return strings.TrimRight(b.String(), "\n")
}
