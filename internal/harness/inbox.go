package harness

import (
	"fmt"
	"sort"
	"strings"
)

const unownedInboxOwner = "unowned"

type OwnerInboxEntry struct {
	Owner             string
	ReviewReady       []*Task
	PromotionReady    []*Task
	Retryable         []*Task
	ActiveRollouts    int
	PlannedRollouts   int
	PausedRollouts    int
	AbortedRollouts   int
	CompletedRollouts int
	PendingGates      int
	ApprovedGates     int
	RejectedGates     int
}

type OwnerInbox struct {
	Entries []OwnerInboxEntry
}

func BuildOwnerInbox(project Project, cfg *Config) (*OwnerInbox, error) {
	tasks, err := ListTasks(project)
	if err != nil {
		return nil, err
	}
	entryMap := make(map[string]*OwnerInboxEntry)
	for _, task := range tasks {
		if task == nil {
			continue
		}
		if !taskReviewReady(task) && !taskPromotionReady(task) && !taskRetryable(task, cfg) {
			continue
		}
		owner := ownerForTask(cfg, task)
		entry := entryMap[owner]
		if entry == nil {
			entry = &OwnerInboxEntry{Owner: owner}
			entryMap[owner] = entry
		}
		switch {
		case taskPromotionReady(task):
			entry.PromotionReady = append(entry.PromotionReady, task)
		case taskReviewReady(task):
			entry.ReviewReady = append(entry.ReviewReady, task)
		case taskRetryable(task, cfg):
			entry.Retryable = append(entry.Retryable, task)
		}
	}
	rollouts, err := ListReleaseWaveRollouts(project)
	if err != nil {
		return nil, err
	}
	for _, rollout := range rollouts {
		if rollout == nil {
			continue
		}
		for _, group := range rollout.Groups {
			if group == nil {
				continue
			}
			for owner := range group.Owners {
				entry := entryMap[owner]
				if entry == nil {
					entry = &OwnerInboxEntry{Owner: owner}
					entryMap[owner] = entry
				}
				switch releaseWaveGateStatus(group) {
				case ReleaseGateApproved:
					entry.ApprovedGates++
				case ReleaseGateRejected:
					entry.RejectedGates++
				default:
					entry.PendingGates++
				}
				switch releaseWaveStatus(group) {
				case ReleaseWaveActive:
					entry.ActiveRollouts++
				case ReleaseWavePaused:
					entry.PausedRollouts++
				case ReleaseWaveAborted:
					entry.AbortedRollouts++
				case ReleaseWaveCompleted:
					entry.CompletedRollouts++
				default:
					entry.PlannedRollouts++
				}
			}
		}
	}
	var entries []OwnerInboxEntry
	for _, entry := range entryMap {
		sortInboxTasks(entry.ReviewReady)
		sortInboxTasks(entry.PromotionReady)
		sortInboxTasks(entry.Retryable)
		entries = append(entries, *entry)
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Owner == unownedInboxOwner || entries[j].Owner == unownedInboxOwner {
			return entries[j].Owner == unownedInboxOwner
		}
		return entries[i].Owner < entries[j].Owner
	})
	return &OwnerInbox{Entries: entries}, nil
}

func FormatOwnerInbox(inbox *OwnerInbox) string {
	if inbox == nil || len(inbox.Entries) == 0 {
		return "No harness owner inbox items."
	}
	var b strings.Builder
	b.WriteString("Harness owner inbox:\n")
	for _, entry := range inbox.Entries {
		b.WriteString(fmt.Sprintf("- %s\n", entry.Owner))
		b.WriteString(fmt.Sprintf("  review_ready: %d\n", len(entry.ReviewReady)))
		b.WriteString(fmt.Sprintf("  promotion_ready: %d\n", len(entry.PromotionReady)))
		b.WriteString(fmt.Sprintf("  retryable: %d\n", len(entry.Retryable)))
		if entry.ActiveRollouts > 0 || entry.PlannedRollouts > 0 || entry.PausedRollouts > 0 || entry.AbortedRollouts > 0 || entry.CompletedRollouts > 0 {
			b.WriteString(fmt.Sprintf("  rollouts: active=%d planned=%d paused=%d aborted=%d completed=%d\n", entry.ActiveRollouts, entry.PlannedRollouts, entry.PausedRollouts, entry.AbortedRollouts, entry.CompletedRollouts))
			b.WriteString(fmt.Sprintf("  gates: pending=%d approved=%d rejected=%d\n", entry.PendingGates, entry.ApprovedGates, entry.RejectedGates))
		}
		renderInboxTasks(&b, "review", entry.ReviewReady)
		renderInboxTasks(&b, "promotion", entry.PromotionReady)
		renderInboxTasks(&b, "retry", entry.Retryable)
	}
	return strings.TrimRight(b.String(), "\n")
}

func renderInboxTasks(b *strings.Builder, label string, tasks []*Task) {
	if b == nil || len(tasks) == 0 {
		return
	}
	for _, task := range tasks {
		if task == nil {
			continue
		}
		b.WriteString(fmt.Sprintf("  %s: %s %s", label, task.ID, task.Goal))
		if task.ContextPath != "" || task.ContextName != "" {
			b.WriteString(" [")
			b.WriteString(firstNonEmptyText(task.ContextPath, task.ContextName))
			b.WriteString("]")
		}
		b.WriteString("\n")
	}
}

func ownerForTask(cfg *Config, task *Task) string {
	contextCfg := ResolveTaskContext(cfg, task)
	if contextCfg != nil && strings.TrimSpace(contextCfg.Owner) != "" {
		return contextCfg.Owner
	}
	return unownedInboxOwner
}

func ownerMatches(cfg *Config, task *Task, owner string) bool {
	owner = strings.TrimSpace(owner)
	if owner == "" {
		return true
	}
	return strings.EqualFold(ownerForTask(cfg, task), owner)
}

func taskRetryable(task *Task, cfg *Config) bool {
	return task != nil && task.Status == TaskFailed && task.Attempt < maxTaskAttempts(cfg)
}

func sortInboxTasks(tasks []*Task) {
	sort.Slice(tasks, func(i, j int) bool {
		return tasks[i].UpdatedAt.After(tasks[j].UpdatedAt)
	})
}
