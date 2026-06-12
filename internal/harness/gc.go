package harness

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type GCReport struct {
	ArchivedTasks    int
	AbandonedTasks   int
	DeletedLogs      int
	RemovedWorktrees int
}

func RunGC(project Project, cfg *Config, now time.Time) (*GCReport, error) {
	report := &GCReport{}
	tasks, err := ListTasks(project)
	if err != nil {
		return nil, err
	}
	abandonAfter := parseConfigDuration(cfg.GC.AbandonAfter, 24*time.Hour)
	archiveAfter := parseConfigDuration(cfg.GC.ArchiveAfter, 7*24*time.Hour)
	deleteLogsAfter := parseConfigDuration(cfg.GC.DeleteLogsAfter, 14*24*time.Hour)
	staleBlocked := staleBlockedTasks(tasks, abandonAfter, now)

	for _, task := range tasks {
		if task == nil {
			continue
		}
		if task.Status == TaskRunning && now.Sub(task.UpdatedAt) > abandonAfter {
			task.Status = TaskAbandoned
			task.Error = "marked abandoned by harness gc"
			if err := SaveTask(project, task); err != nil {
				return nil, err
			}
			report.AbandonedTasks++
		}
		if task.Status == TaskBlocked && now.Sub(task.UpdatedAt) > abandonAfter {
			task.Status = TaskAbandoned
			task.Error = "stale blocked task abandoned by harness gc"
			if err := SaveTask(project, task); err != nil {
				return nil, err
			}
			report.AbandonedTasks++
		}
		if (task.Status == TaskCompleted || task.Status == TaskFailed || task.Status == TaskAbandoned) && now.Sub(task.UpdatedAt) > archiveAfter {
			if strings.TrimSpace(task.WorkspacePath) != "" && task.WorkspacePath != project.RootDir {
				_ = cleanupWorkspace(project, task)
			}
			dst := filepath.Join(project.ArchiveDir, task.ID+".json")
			if err := os.MkdirAll(project.ArchiveDir, 0755); err != nil {
				return nil, fmt.Errorf("create archive dir: %w", err)
			}
			if err := os.Rename(taskPath(project, task.ID), dst); err == nil {
				report.ArchivedTasks++
			}
		}
	}
	if len(staleBlocked) > 0 {
		tasks, err = ListTasks(project)
		if err != nil {
			return nil, err
		}
	}
	orphans, err := orphanedWorktrees(project, tasks)
	if err != nil {
		return nil, fmt.Errorf("scan orphaned worktrees: %w", err)
	}
	for _, path := range orphans {
		if err := os.RemoveAll(path); err == nil {
			report.RemovedWorktrees++
		}
	}

	logEntries, err := os.ReadDir(project.LogsDir)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("read harness logs: %w", err)
	}
	for _, entry := range logEntries {
		if entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return nil, fmt.Errorf("stat harness log %s: %w", entry.Name(), err)
		}
		if now.Sub(info.ModTime()) <= deleteLogsAfter {
			continue
		}
		if err := os.Remove(filepath.Join(project.LogsDir, entry.Name())); err == nil {
			report.DeletedLogs++
		}
	}
	return report, nil
}

func FormatGCReport(report *GCReport) string {
	if report == nil {
		return "No harness gc report."
	}
	return strings.TrimSpace(fmt.Sprintf("Harness gc complete.\n- archived tasks: %d\n- abandoned tasks: %d\n- deleted logs: %d\n- removed worktrees: %d", report.ArchivedTasks, report.AbandonedTasks, report.DeletedLogs, report.RemovedWorktrees))
}
