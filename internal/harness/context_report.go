package harness

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

const unscopedContextKey = "__unscoped__"

type ContextSummary struct {
	Name               string
	Path               string
	Description        string
	Owner              string
	CommandCount       int
	TaskCount          int
	QueuedTasks        int
	RunningTasks       int
	BlockedTasks       int
	FailedTasks        int
	VerificationFailed int
	ReviewReady        int
	PromotionReady     int
	ReleaseReady       int
	ActiveRollouts     int
	PlannedRollouts    int
	PausedRollouts     int
	AbortedRollouts    int
	CompletedRollouts  int
	PendingGates       int
	ApprovedGates      int
	RejectedGates      int
	LatestTask         *Task
	Unscoped           bool
}

type ContextReport struct {
	Summaries []ContextSummary
}

func BuildContextReport(project Project, cfg *Config) (*ContextReport, error) {
	tasks, err := ListTasks(project)
	if err != nil {
		return nil, err
	}
	summaryMap := make(map[string]*ContextSummary)
	if cfg != nil {
		for _, contextCfg := range cfg.Contexts {
			key := contextSummaryKey(contextCfg.Name, contextCfg.Path)
			summaryMap[key] = &ContextSummary{
				Name:         contextCfg.Name,
				Path:         contextCfg.Path,
				Description:  contextCfg.Description,
				Owner:        contextCfg.Owner,
				CommandCount: len(contextCfg.Commands),
			}
		}
	}
	for _, task := range tasks {
		if task == nil {
			continue
		}
		summary := ensureContextSummary(summaryMap, task)
		applyTaskToContextSummary(summary, task)
	}
	rollouts, err := ListReleaseWaveRollouts(project)
	if err != nil {
		return nil, err
	}
	applyRolloutsToContextSummaries(summaryMap, rollouts)
	var summaries []ContextSummary
	for _, summary := range summaryMap {
		if summary == nil {
			continue
		}
		summaries = append(summaries, *summary)
	}
	sort.Slice(summaries, func(i, j int) bool {
		if summaries[i].Unscoped != summaries[j].Unscoped {
			return !summaries[i].Unscoped
		}
		left := firstNonEmptyText(summaries[i].Path, summaries[i].Name)
		right := firstNonEmptyText(summaries[j].Path, summaries[j].Name)
		return left < right
	})
	return &ContextReport{Summaries: summaries}, nil
}

func ensureContextSummary(summaryMap map[string]*ContextSummary, task *Task) *ContextSummary {
	if task == nil {
		return nil
	}
	if strings.TrimSpace(task.ContextName) == "" && strings.TrimSpace(task.ContextPath) == "" {
		summary, ok := summaryMap[unscopedContextKey]
		if !ok {
			summary = &ContextSummary{Name: "unscoped", Description: "Tasks without an explicit bounded context", Unscoped: true}
			summaryMap[unscopedContextKey] = summary
		}
		return summary
	}
	key := contextSummaryKey(task.ContextName, task.ContextPath)
	summary, ok := summaryMap[key]
	if !ok {
		summary = &ContextSummary{
			Name:        task.ContextName,
			Path:        task.ContextPath,
			Description: "Task references a context that is not currently declared in harness config",
		}
		summaryMap[key] = summary
	}
	return summary
}

func applyTaskToContextSummary(summary *ContextSummary, task *Task) {
	if summary == nil || task == nil {
		return
	}
	summary.TaskCount++
	switch task.Status {
	case TaskQueued:
		summary.QueuedTasks++
	case TaskRunning:
		summary.RunningTasks++
	case TaskBlocked:
		summary.BlockedTasks++
	case TaskFailed, TaskAbandoned:
		summary.FailedTasks++
	}
	if task.VerificationStatus == VerificationFailed {
		summary.VerificationFailed++
	}
	if taskReviewReady(task) {
		summary.ReviewReady++
	}
	if taskPromotionReady(task) {
		summary.PromotionReady++
	}
	if taskReleaseReady(task) {
		summary.ReleaseReady++
	}
	if summary.LatestTask == nil || task.UpdatedAt.After(summary.LatestTask.UpdatedAt) {
		summary.LatestTask = task
	}
}

func FormatContextReport(report *ContextReport) string {
	if report == nil || len(report.Summaries) == 0 {
		return "No harness contexts found."
	}
	var b strings.Builder
	b.WriteString("Harness contexts:\n")
	for _, summary := range report.Summaries {
		label := firstNonEmptyText(summary.Path, summary.Name)
		if label == "" {
			label = "unscoped"
		}
		b.WriteString(fmt.Sprintf("- %s\n", label))
		if summary.Name != "" && summary.Name != label {
			b.WriteString(fmt.Sprintf("  name: %s\n", summary.Name))
		}
		if summary.Description != "" {
			b.WriteString(fmt.Sprintf("  description: %s\n", summary.Description))
		}
		if summary.Owner != "" {
			b.WriteString(fmt.Sprintf("  owner: %s\n", summary.Owner))
		}
		b.WriteString(fmt.Sprintf("  commands: %d\n", summary.CommandCount))
		b.WriteString(fmt.Sprintf("  tasks: total=%d queued=%d running=%d blocked=%d failed=%d verification_failed=%d review_ready=%d promotion_ready=%d release_ready=%d\n",
			summary.TaskCount, summary.QueuedTasks, summary.RunningTasks, summary.BlockedTasks, summary.FailedTasks, summary.VerificationFailed, summary.ReviewReady, summary.PromotionReady, summary.ReleaseReady))
		if summary.ActiveRollouts > 0 || summary.PlannedRollouts > 0 || summary.PausedRollouts > 0 || summary.AbortedRollouts > 0 || summary.CompletedRollouts > 0 {
			b.WriteString(fmt.Sprintf("  rollouts: active=%d planned=%d paused=%d aborted=%d completed=%d\n", summary.ActiveRollouts, summary.PlannedRollouts, summary.PausedRollouts, summary.AbortedRollouts, summary.CompletedRollouts))
			b.WriteString(fmt.Sprintf("  gates: pending=%d approved=%d rejected=%d\n", summary.PendingGates, summary.ApprovedGates, summary.RejectedGates))
		}
		if summary.LatestTask != nil {
			b.WriteString(fmt.Sprintf("  latest: %s [%s]\n", summary.LatestTask.ID, summary.LatestTask.Status))
			if !summary.LatestTask.UpdatedAt.IsZero() {
				b.WriteString(fmt.Sprintf("  updated: %s\n", summary.LatestTask.UpdatedAt.Format(time.RFC3339)))
			}
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func contextSummaryKey(name, path string) string {
	return firstNonEmptyText(strings.TrimSpace(path), strings.TrimSpace(name))
}

func applyRolloutsToContextSummaries(summaryMap map[string]*ContextSummary, rollouts []*ReleaseWavePlan) {
	for _, rollout := range rollouts {
		if rollout == nil {
			continue
		}
		for _, group := range rollout.Groups {
			if group == nil {
				continue
			}
			for contextLabel := range group.Contexts {
				summary := ensureContextSummary(summaryMap, &Task{ContextPath: normalizeContextSummaryLabel(contextLabel), ContextName: normalizeContextSummaryName(contextLabel)})
				if contextLabel == "unscoped" {
					summary = ensureContextSummary(summaryMap, &Task{})
				}
				switch releaseWaveGateStatus(group) {
				case ReleaseGateApproved:
					summary.ApprovedGates++
				case ReleaseGateRejected:
					summary.RejectedGates++
				default:
					summary.PendingGates++
				}
				switch releaseWaveStatus(group) {
				case ReleaseWaveActive:
					summary.ActiveRollouts++
				case ReleaseWavePaused:
					summary.PausedRollouts++
				case ReleaseWaveAborted:
					summary.AbortedRollouts++
				case ReleaseWaveCompleted:
					summary.CompletedRollouts++
				default:
					summary.PlannedRollouts++
				}
			}
		}
	}
}

func normalizeContextSummaryLabel(label string) string {
	if strings.TrimSpace(label) == "" || label == "unscoped" {
		return ""
	}
	return label
}

func normalizeContextSummaryName(label string) string {
	if strings.TrimSpace(label) == "" || label == "unscoped" {
		return ""
	}
	return label
}
