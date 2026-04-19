package harness

import (
	"context"
	"fmt"
	"strings"
	"time"
)

type DoctorReport struct {
	Project            Project
	Config             *Config
	Structural         *CheckReport
	Contexts           int
	TotalTasks         int
	RunningTasks       int
	BlockedTasks       int
	FailedTasks        int
	Retryable          int
	WorkerTasks        int
	StaleBlocked       int
	OrphanedWorktrees  int
	WorkerDrift        int
	VerificationFailed int
	ReviewReady        int
	PromotionReady     int
	ReleaseReady       int
	Rollouts           int
	ActiveRollouts     int
	PlannedRollouts    int
	PausedRollouts     int
	AbortedRollouts    int
	CompletedRollouts  int
	PendingGates       int
	ApprovedGates      int
	RejectedGates      int
	LastTask           *Task
}

func Doctor(project Project, cfg *Config) (*DoctorReport, error) {
	report := &DoctorReport{
		Project: project,
		Config:  cfg,
	}
	structural, err := CheckProject(context.Background(), project, cfg, CheckOptions{RunCommands: false})
	if err != nil {
		return nil, err
	}
	report.Structural = structural
	if cfg != nil {
		report.Contexts = len(cfg.Contexts)
	}
	tasks, err := ListTasks(project)
	if err != nil {
		return nil, err
	}
	abandonAfter := 24 * time.Hour
	if cfg != nil {
		abandonAfter = parseConfigDuration(cfg.GC.AbandonAfter, 24*time.Hour)
	}
	report.StaleBlocked = len(staleBlockedTasks(tasks, abandonAfter, time.Now().UTC()))
	report.WorkerDrift = len(workerDriftTasks(tasks))
	orphans, err := orphanedWorktrees(project, tasks)
	if err != nil {
		return nil, err
	}
	report.OrphanedWorktrees = len(orphans)
	rollouts, err := ListReleaseWaveRollouts(project)
	if err != nil {
		return nil, err
	}
	report.TotalTasks = len(tasks)
	if len(tasks) > 0 {
		report.LastTask = tasks[0]
	}
	for _, task := range tasks {
		switch task.Status {
		case TaskRunning:
			report.RunningTasks++
			if strings.TrimSpace(task.WorkerID) != "" {
				report.WorkerTasks++
			}
		case TaskBlocked:
			report.BlockedTasks++
		case TaskFailed, TaskAbandoned:
			report.FailedTasks++
			if task.Status == TaskFailed && task.Attempt < maxTaskAttempts(cfg) {
				report.Retryable++
			}
		}
		if task.VerificationStatus == VerificationFailed {
			report.VerificationFailed++
		}
		if taskReviewReady(task) {
			report.ReviewReady++
		}
		if taskPromotionReady(task) {
			report.PromotionReady++
		}
		if taskReleaseReady(task) {
			report.ReleaseReady++
		}
	}
	for _, rollout := range rollouts {
		if rollout == nil {
			continue
		}
		report.Rollouts++
		for _, group := range rollout.Groups {
			switch releaseWaveGateStatus(group) {
			case ReleaseGateApproved:
				report.ApprovedGates++
			case ReleaseGateRejected:
				report.RejectedGates++
			default:
				report.PendingGates++
			}
			switch releaseWaveStatus(group) {
			case ReleaseWaveActive:
				report.ActiveRollouts++
			case ReleaseWavePaused:
				report.PausedRollouts++
			case ReleaseWaveAborted:
				report.AbortedRollouts++
			case ReleaseWaveCompleted:
				report.CompletedRollouts++
			default:
				report.PlannedRollouts++
			}
		}
	}
	return report, nil
}

func FormatDoctorReport(report *DoctorReport) string {
	if report == nil {
		return "No harness doctor report."
	}
	var b strings.Builder
	b.WriteString("Harness doctor\n")
	b.WriteString(fmt.Sprintf("- root: %s\n", report.Project.RootDir))
	b.WriteString(fmt.Sprintf("- config: %s\n", report.Project.ConfigPath))
	if report.Config != nil {
		b.WriteString(fmt.Sprintf("- project: %s\n", report.Config.Project.Name))
	}
	if report.Structural != nil {
		status := "ok"
		if !report.Structural.Passed {
			status = "needs attention"
		}
		b.WriteString(fmt.Sprintf("- structure: %s\n", status))
	}
	if report.Contexts > 0 {
		b.WriteString(fmt.Sprintf("- contexts: %d\n", report.Contexts))
	}
	b.WriteString(fmt.Sprintf("- tasks: total=%d running=%d worker_backed=%d blocked=%d stale_blocked=%d failed=%d verification_failed=%d review_ready=%d promotion_ready=%d release_ready=%d retryable=%d worker_drift=%d\n", report.TotalTasks, report.RunningTasks, report.WorkerTasks, report.BlockedTasks, report.StaleBlocked, report.FailedTasks, report.VerificationFailed, report.ReviewReady, report.PromotionReady, report.ReleaseReady, report.Retryable, report.WorkerDrift))
	if report.Rollouts > 0 {
		b.WriteString(fmt.Sprintf("- rollouts: total=%d active=%d planned=%d paused=%d aborted=%d completed=%d\n", report.Rollouts, report.ActiveRollouts, report.PlannedRollouts, report.PausedRollouts, report.AbortedRollouts, report.CompletedRollouts))
		b.WriteString(fmt.Sprintf("- gates: pending=%d approved=%d rejected=%d\n", report.PendingGates, report.ApprovedGates, report.RejectedGates))
	}
	b.WriteString(fmt.Sprintf("- worktrees: orphaned=%d\n", report.OrphanedWorktrees))
	if report.LastTask != nil {
		b.WriteString(fmt.Sprintf("- latest task: %s (%s)\n", report.LastTask.ID, report.LastTask.Status))
		if report.LastTask.ContextName != "" || report.LastTask.ContextPath != "" {
			b.WriteString(fmt.Sprintf("  context: %s\n", firstNonEmptyText(report.LastTask.ContextName, report.LastTask.ContextPath)))
			if contextCfg := ResolveTaskContext(report.Config, report.LastTask); contextCfg != nil && strings.TrimSpace(contextCfg.Owner) != "" {
				b.WriteString(fmt.Sprintf("  owner: %s\n", contextCfg.Owner))
			}
		}
		if report.LastTask.LogPath != "" {
			b.WriteString(fmt.Sprintf("  log: %s\n", report.LastTask.LogPath))
		}
		if report.LastTask.WorkerID != "" {
			b.WriteString(fmt.Sprintf("  worker: %s [%s]\n", report.LastTask.WorkerID, report.LastTask.WorkerStatus))
		}
		if report.LastTask.WorkerProgress != "" {
			b.WriteString(fmt.Sprintf("  progress: %s\n", report.LastTask.WorkerProgress))
		}
		if report.LastTask.VerificationStatus != "" {
			b.WriteString(fmt.Sprintf("  verification: %s\n", report.LastTask.VerificationStatus))
		}
		if report.LastTask.VerificationReportPath != "" {
			b.WriteString(fmt.Sprintf("  delivery report: %s\n", report.LastTask.VerificationReportPath))
		}
		if report.LastTask.ReviewStatus != "" {
			b.WriteString(fmt.Sprintf("  review: %s\n", report.LastTask.ReviewStatus))
		}
		if report.LastTask.PromotionStatus != "" {
			b.WriteString(fmt.Sprintf("  promotion: %s\n", report.LastTask.PromotionStatus))
		}
		if report.LastTask.ReleaseBatchID != "" {
			b.WriteString(fmt.Sprintf("  release batch: %s\n", report.LastTask.ReleaseBatchID))
		}
	}
	if report.Structural != nil && len(report.Structural.Issues) > 0 {
		b.WriteString("\nStructural issues:\n")
		for _, issue := range report.Structural.Issues {
			b.WriteString(fmt.Sprintf("- %s\n", issue.Message))
		}
	}
	var driftIssues []string
	if report.StaleBlocked > 0 {
		driftIssues = append(driftIssues, fmt.Sprintf("%d blocked task(s) have exceeded the gc abandon threshold", report.StaleBlocked))
	}
	if report.OrphanedWorktrees > 0 {
		driftIssues = append(driftIssues, fmt.Sprintf("%d orphaned worktree(s) exist under the harness worktrees directory", report.OrphanedWorktrees))
	}
	if report.WorkerDrift > 0 {
		driftIssues = append(driftIssues, fmt.Sprintf("%d running task(s) have missing or terminal worker state", report.WorkerDrift))
	}
	if len(driftIssues) > 0 {
		b.WriteString("\nDrift issues:\n")
		for _, issue := range driftIssues {
			b.WriteString(fmt.Sprintf("- %s\n", issue))
		}
	}
	return strings.TrimRight(b.String(), "\n")
}
