package harness

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const PromotionApplied = "promoted"

var promotionIgnoredPaths = []string{
	".ggcode/todos.json",
}

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
	overlaps, err := promotionWorkspaceOverlaps(ctx, project.RootDir, branch)
	if err != nil {
		return err
	}
	if len(overlaps) > 0 {
		return fmt.Errorf("project workspace has overlapping uncommitted or untracked files; commit/stash them first or sync them into the task worktree before promotion: %s", strings.Join(overlaps, ", "))
	}
	cmd := gitCmd(ctx, "merge", "--no-ff", "--no-edit", branch)
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
	addCmd := gitCmd(ctx, "add", "-A")
	addCmd.Dir = task.WorkspacePath
	if out, err := addCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("stage promotion changes for %s: %s", task.ID, strings.TrimSpace(string(out)))
	}
	for _, ignored := range promotionIgnoredPaths {
		rmCmd := gitCmd(ctx, "rm", "--cached", "--ignore-unmatch", "--", ignored)
		rmCmd.Dir = task.WorkspacePath
		if out, err := rmCmd.CombinedOutput(); err != nil {
			return fmt.Errorf("exclude promotion runtime state for %s: %s", task.ID, strings.TrimSpace(string(out)))
		}
	}
	staged, err := gitHasStagedChanges(ctx, task.WorkspacePath)
	if err != nil {
		return err
	}
	if !staged {
		return nil
	}
	message := fmt.Sprintf("harness promote %s: %s", task.ID, truncatePromotionMessage(task.Goal))
	commitCmd := gitCmd(ctx, "commit", "--no-verify", "-m", message+harnessCoAuthor)
	commitCmd.Dir = task.WorkspacePath
	if out, err := commitCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("commit promotion changes for %s: %s", task.ID, strings.TrimSpace(string(out)))
	}
	return nil
}

func gitDirty(ctx context.Context, workingDir string) (bool, error) {
	cmd := gitCmd(ctx, "status", "--porcelain")
	cmd.Dir = workingDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return false, fmt.Errorf("inspect git status: %s", strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)) != "", nil
}

func gitHasStagedChanges(ctx context.Context, workingDir string) (bool, error) {
	cmd := gitCmd(ctx, "diff", "--cached", "--quiet", "--exit-code")
	cmd.Dir = workingDir
	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
			return true, nil
		}
		return false, fmt.Errorf("inspect staged promotion changes: %w", err)
	}
	return false, nil
}

func promotionWorkspaceOverlaps(ctx context.Context, workingDir, branch string) ([]string, error) {
	dirtyPaths, err := gitDirtyPaths(ctx, workingDir)
	if err != nil {
		return nil, err
	}
	if len(dirtyPaths) == 0 {
		return nil, nil
	}
	branchPaths, err := gitBranchChangedPaths(ctx, workingDir, branch)
	if err != nil {
		return nil, err
	}
	if len(branchPaths) == 0 {
		return nil, nil
	}
	branchSet := make(map[string]struct{}, len(branchPaths))
	for _, path := range branchPaths {
		if isPromotionIgnoredPath(path) {
			continue
		}
		branchSet[path] = struct{}{}
	}
	var overlaps []string
	for _, path := range dirtyPaths {
		if _, ok := branchSet[path]; ok {
			overlaps = append(overlaps, path)
		}
	}
	sort.Strings(overlaps)
	return overlaps, nil
}

func gitDirtyPaths(ctx context.Context, workingDir string) ([]string, error) {
	cmd := gitCmd(ctx, "status", "--porcelain", "--untracked-files=all")
	cmd.Dir = workingDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("inspect git status: %s", strings.TrimSpace(string(out)))
	}
	lines := strings.Split(strings.ReplaceAll(string(out), "\r\n", "\n"), "\n")
	var paths []string
	seen := make(map[string]struct{})
	for _, raw := range lines {
		if strings.TrimSpace(raw) == "" || len(raw) < 4 {
			continue
		}
		path := strings.TrimSpace(raw[3:])
		if idx := strings.Index(path, " -> "); idx >= 0 {
			path = strings.TrimSpace(path[idx+4:])
		}
		path = filepath.ToSlash(path)
		if isPromotionIgnoredPath(path) {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths, nil
}

func gitBranchChangedPaths(ctx context.Context, workingDir, branch string) ([]string, error) {
	cmd := gitCmd(ctx, "diff", "--name-only", "HEAD.."+branch)
	cmd.Dir = workingDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("inspect promoted branch %s: %s", branch, strings.TrimSpace(string(out)))
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	var paths []string
	seen := make(map[string]struct{})
	for _, line := range lines {
		path := filepath.ToSlash(strings.TrimSpace(line))
		if path == "" || isPromotionIgnoredPath(path) {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths, nil
}

func isPromotionIgnoredPath(path string) bool {
	path = filepath.ToSlash(strings.TrimSpace(path))
	for _, ignored := range promotionIgnoredPaths {
		if path == ignored {
			return true
		}
	}
	return false
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
