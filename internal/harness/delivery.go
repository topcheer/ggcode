package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/topcheer/ggcode/internal/util"
)

const (
	VerificationPassed  = "passed"
	VerificationFailed  = "failed"
	VerificationSkipped = "skipped"
)

type DeliveryReport struct {
	TaskID       string       `json:"task_id"`
	WorkingDir   string       `json:"working_dir"`
	ChangedFiles []string     `json:"changed_files,omitempty"`
	DiffStat     string       `json:"diff_stat,omitempty"`
	Check        *CheckReport `json:"check,omitempty"`
}

func captureDeliveryReport(ctx context.Context, project Project, cfg *Config, workingDir string, task *Task, verify bool) (string, *DeliveryReport, error) {
	if task == nil {
		return "", nil, fmt.Errorf("missing task")
	}
	report := &DeliveryReport{
		TaskID:     task.ID,
		WorkingDir: workingDir,
	}
	if strings.TrimSpace(workingDir) == "" {
		workingDir = project.RootDir
		report.WorkingDir = workingDir
	}
	isGit := isGitWorkingTree(ctx, workingDir)
	if isGit {
		report.ChangedFiles = changedFilesInRepo(ctx, workingDir)
		diffStat, err := gitDiffStat(ctx, workingDir)
		if err != nil {
			return "", nil, err
		}
		report.DiffStat = diffStat
	}
	if verify && cfg != nil {
		checkReport, err := CheckProject(ctx, project, cfg, CheckOptions{
			RunCommands: isGit,
			CommandDir:  workingDir,
			Context:     firstNonEmptyText(task.ContextName, task.ContextPath),
		})
		if err != nil {
			return "", nil, err
		}
		report.Check = checkReport
	}
	path := filepath.Join(project.LogsDir, task.ID+"-delivery.json")
	if err := os.MkdirAll(project.LogsDir, 0o755); err != nil {
		return "", nil, fmt.Errorf("create harness logs dir: %w", err)
	}
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return "", nil, fmt.Errorf("marshal delivery report: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return "", nil, fmt.Errorf("write delivery report: %w", err)
	}
	return path, report, nil
}

func isGitWorkingTree(ctx context.Context, workingDir string) bool {
	cmd, _, err := util.NewShellCommandContext(ctx, "git rev-parse --is-inside-work-tree")
	if err != nil {
		return false
	}
	cmd.Dir = workingDir
	out, err := cmd.CombinedOutput()
	return err == nil && strings.TrimSpace(string(out)) == "true"
}

func changedFilesInRepo(ctx context.Context, workingDir string) []string {
	cmd, _, err := util.NewShellCommandContext(ctx, "git status --short --untracked-files=all")
	if err != nil {
		return nil
	}
	cmd.Dir = workingDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil
	}
	var files []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || len(line) < 4 {
			continue
		}
		path := strings.TrimSpace(line[3:])
		if idx := strings.LastIndex(path, " -> "); idx >= 0 {
			path = strings.TrimSpace(path[idx+4:])
		}
		if path != "" {
			files = append(files, path)
		}
	}
	return files
}

func gitDiffStat(ctx context.Context, workingDir string) (string, error) {
	cmd, _, err := util.NewShellCommandContext(ctx, "git diff --stat --cached && git diff --stat")
	if err != nil {
		return "", fmt.Errorf("build git diff stat command: %w", err)
	}
	cmd.Dir = workingDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("capture git diff stat: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}
