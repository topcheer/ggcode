package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
)

// handleCronCommand handles /cron subcommands: list, pause, resume, pauseall, resumeall, get
func (m Model) handleCronCommand(parts []string) tea.Cmd {
	if m.cronScheduler == nil {
		m.chatWriteSystem(nextSystemID(), "Cron scheduler not available")
		return nil
	}

	subcmd := "list"
	if len(parts) > 1 {
		subcmd = strings.ToLower(parts[1])
	}

	switch subcmd {
	case "list":
		jobs := m.cronScheduler.List()
		if len(jobs) == 0 {
			m.chatWriteSystem(nextSystemID(), "No scheduled jobs")
			return nil
		}
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("Scheduled jobs (%d):\n", len(jobs)))
		for _, j := range jobs {
			status := "active"
			if j.Paused {
				status = "paused"
			}
			next := "-"
			if !j.NextFire.IsZero() {
				next = j.NextFire.Format("01-02 15:04 MST")
			}
			recur := "one-shot"
			if j.Recurring {
				recur = "recurring"
			}
			sb.WriteString(fmt.Sprintf("  %s [%s, %s, %s] next=%s\n",
				j.ID, status, recur, j.CronExpr, next))
		}
		m.chatWriteSystem(nextSystemID(), sb.String())

	case "pause":
		if len(parts) < 3 {
			m.chatWriteSystem(nextSystemID(), "Usage: /cron pause <job-id>")
			return nil
		}
		if err := m.cronScheduler.Pause(parts[2]); err != nil {
			m.chatWriteSystem(nextSystemID(), fmt.Sprintf("Error: %v", err))
		} else {
			m.chatWriteSystem(nextSystemID(), fmt.Sprintf("Paused job %s", parts[2]))
		}

	case "resume":
		if len(parts) < 3 {
			m.chatWriteSystem(nextSystemID(), "Usage: /cron resume <job-id>")
			return nil
		}
		if err := m.cronScheduler.Resume(parts[2]); err != nil {
			m.chatWriteSystem(nextSystemID(), fmt.Sprintf("Error: %v", err))
		} else {
			m.chatWriteSystem(nextSystemID(), fmt.Sprintf("Resumed job %s", parts[2]))
		}

	case "pauseall":
		jobs := m.cronScheduler.List()
		count := 0
		for _, j := range jobs {
			if !j.Paused {
				if err := m.cronScheduler.Pause(j.ID); err == nil {
					count++
				}
			}
		}
		m.chatWriteSystem(nextSystemID(), fmt.Sprintf("Paused %d job(s)", count))

	case "resumeall":
		jobs := m.cronScheduler.List()
		count := 0
		for _, j := range jobs {
			if j.Paused {
				if err := m.cronScheduler.Resume(j.ID); err == nil {
					count++
				}
			}
		}
		m.chatWriteSystem(nextSystemID(), fmt.Sprintf("Resumed %d job(s)", count))

	case "get":
		if len(parts) < 3 {
			m.chatWriteSystem(nextSystemID(), "Usage: /cron get <job-id>")
			return nil
		}
		job, ok := m.cronScheduler.Get(parts[2])
		if !ok {
			m.chatWriteSystem(nextSystemID(), fmt.Sprintf("Job %s not found", parts[2]))
			return nil
		}
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("Job %s\n", job.ID))
		sb.WriteString(fmt.Sprintf("  Schedule: %s\n", job.CronExpr))
		sb.WriteString(fmt.Sprintf("  Recurring: %v\n", job.Recurring))
		sb.WriteString(fmt.Sprintf("  Queue if busy: %v\n", job.QueueIfBusy))
		if job.Paused {
			sb.WriteString("  Status: paused\n")
		} else {
			sb.WriteString("  Status: active\n")
		}
		if !job.NextFire.IsZero() {
			sb.WriteString(fmt.Sprintf("  Next fire: %s\n", job.NextFire.Format("2006-01-02 15:04 MST")))
		}
		if !job.CreatedAt.IsZero() {
			sb.WriteString(fmt.Sprintf("  Created: %s\n", job.CreatedAt.Format("2006-01-02 15:04 MST")))
		}
		for _, line := range strings.Split(job.Prompt, "\n") {
			if strings.TrimSpace(line) != "" {
				sb.WriteString(fmt.Sprintf("  Prompt: %s\n", strings.TrimSpace(line)))
				break
			}
		}
		m.chatWriteSystem(nextSystemID(), sb.String())

	default:
		usage := `Usage: /cron <subcommand> [args]

Subcommands:
  list              List all scheduled jobs
  get <id>          Show details for a specific job
  pause <id>        Pause a job (timer stops, config preserved)
  resume <id>       Resume a paused job
  pauseall          Pause all jobs
  resumeall         Resume all paused jobs`
		m.chatWriteSystem(nextSystemID(), usage)
	}

	return nil
}
