package harness

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

const PromotionApplied = "promoted"

func ListPromotableTasks(project Project) ([]*Task, error) {
	tasks, err := ListTasks(project)
	if err != nil {
		return nil, err
	}
	var ready []*Task
	for _, task := range tasks {
		if !taskPromotionReady(task) {
			continue
		}
		ready = append(ready, task)
	}
	return ready, nil
}

func taskPromotionReady(task *Task) bool {
	if task == nil {
		return false
	}
	return task.Status == TaskCompleted && task.ReviewStatus == ReviewApproved && task.PromotionStatus != PromotionApplied
}

func PromoteTask(ctx context.Context, project Project, id, note string) (*Task, error) {
	task, err := LoadTask(project, id)
	if err != nil {
		return nil, err
	}
	if !taskPromotionReady(task) {
		return nil, fmt.Errorf("task %s is not ready for promotion", id)
	}
	if task.WorkspaceMode == "git-worktree" && strings.TrimSpace(task.BranchName) != "" {
		if err := ensurePromotionCommit(ctx, task); err != nil {
			return nil, err
		}
		if err := mergePromotedBranch(ctx, project, task.BranchName); err != nil {
			return nil, err
		}
	}
	task.PromotionStatus = PromotionApplied
	task.PromotionNotes = strings.TrimSpace(note)
	now := time.Now().UTC()
	task.PromotedAt = &now
	if err := SaveTask(project, task); err != nil {
		return nil, err
	}
	return task, nil
}

func PromoteApprovedTasks(ctx context.Context, project Project, note string) ([]*Task, error) {
	return PromoteApprovedTasksForOwner(ctx, project, nil, "", note)
}

func PromoteApprovedTasksForOwner(ctx context.Context, project Project, cfg *Config, owner, note string) ([]*Task, error) {
	tasks, err := ListPromotableTasks(project)
	if err != nil {
		return nil, err
	}
	var promoted []*Task
	for _, task := range tasks {
		if !ownerMatches(cfg, task, owner) {
			continue
		}
		item, err := PromoteTask(ctx, project, task.ID, note)
		if err != nil {
			return promoted, err
		}
		promoted = append(promoted, item)
	}
	return promoted, nil
}

func mergePromotedBranch(ctx context.Context, project Project, branch string) error {
	cmd := exec.CommandContext(ctx, "git", "merge", "--no-ff", "--no-edit", branch)
	cmd.Dir = project.RootDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("merge promoted branch %s: %s", branch, strings.TrimSpace(string(out)))
	}
	return nil
}

func ensurePromotionCommit(ctx context.Context, task *Task) error {
	if task == nil || strings.TrimSpace(task.WorkspacePath) == "" {
		return nil
	}
	dirty, err := gitDirty(ctx, task.WorkspacePath)
	if err != nil {
		return err
	}
	if !dirty {
		return nil
	}
	addCmd := exec.CommandContext(ctx, "git", "add", "-A")
	addCmd.Dir = task.WorkspacePath
	if out, err := addCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("stage promotion changes for %s: %s", task.ID, strings.TrimSpace(string(out)))
	}
	message := fmt.Sprintf("harness promote %s: %s", task.ID, truncatePromotionMessage(task.Goal))
	commitCmd := exec.CommandContext(ctx, "git", "commit", "-m", message)
	commitCmd.Dir = task.WorkspacePath
	if out, err := commitCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("commit promotion changes for %s: %s", task.ID, strings.TrimSpace(string(out)))
	}
	return nil
}

func gitDirty(ctx context.Context, workingDir string) (bool, error) {
	cmd := exec.CommandContext(ctx, "git", "status", "--porcelain")
	cmd.Dir = workingDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return false, fmt.Errorf("inspect git status: %s", strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)) != "", nil
}

func truncatePromotionMessage(goal string) string {
	goal = strings.TrimSpace(goal)
	if len(goal) <= 72 {
		return goal
	}
	return strings.TrimSpace(goal[:72])
}

func FormatPromotionList(tasks []*Task) string {
	if len(tasks) == 0 {
		return "No harness tasks are ready for promotion."
	}
	var b strings.Builder
	b.WriteString("Harness promotion queue:\n")
	for _, task := range tasks {
		if task == nil {
			continue
		}
		b.WriteString(fmt.Sprintf("- %s %s\n", task.ID, task.Goal))
		if task.BranchName != "" {
			b.WriteString(fmt.Sprintf("  branch: %s\n", task.BranchName))
		}
		if task.VerificationReportPath != "" {
			b.WriteString(fmt.Sprintf("  delivery_report: %s\n", task.VerificationReportPath))
		}
		if task.ReviewNotes != "" {
			b.WriteString(fmt.Sprintf("  review_notes: %s\n", task.ReviewNotes))
		}
	}
	return strings.TrimRight(b.String(), "\n")
}
