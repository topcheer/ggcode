package harness

import (
	"os"
	"path/filepath"
	"strings"
	"time"
)

func staleBlockedTasks(tasks []*Task, threshold time.Duration, now time.Time) []*Task {
	if threshold <= 0 {
		return nil
	}
	var stale []*Task
	for _, task := range tasks {
		if task == nil || task.Status != TaskBlocked {
			continue
		}
		if now.Sub(task.UpdatedAt) > threshold {
			stale = append(stale, task)
		}
	}
	return stale
}

func workerDriftTasks(tasks []*Task) []*Task {
	var drift []*Task
	for _, task := range tasks {
		if task == nil || task.Status != TaskRunning {
			continue
		}
		switch {
		case strings.TrimSpace(task.WorkerID) == "":
			drift = append(drift, task)
		case task.WorkerStatus == "completed" || task.WorkerStatus == "failed" || task.WorkerStatus == "cancelled":
			drift = append(drift, task)
		}
	}
	return drift
}

func orphanedWorktrees(project Project, tasks []*Task) ([]string, error) {
	entries, err := os.ReadDir(project.WorktreesDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	referenced := make(map[string]struct{})
	for _, task := range tasks {
		if task == nil || strings.TrimSpace(task.WorkspacePath) == "" {
			continue
		}
		clean := filepath.Clean(task.WorkspacePath)
		if filepath.Dir(clean) == filepath.Clean(project.WorktreesDir) {
			referenced[clean] = struct{}{}
		}
	}
	var orphans []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		path := filepath.Join(project.WorktreesDir, entry.Name())
		if _, ok := referenced[path]; !ok {
			orphans = append(orphans, path)
		}
	}
	return orphans, nil
}
