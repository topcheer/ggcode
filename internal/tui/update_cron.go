package tui

import (
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/tunnel"
)

// handleCronPromptMsg processes a cron firing (#2357 slice 3: extracted
// from Model.Update - body moved verbatim, zero behavior change). Idle:
// submit immediately; busy + queue_if_busy: queue; busy + !queue_if_busy:
// skip. #903's correct log split preserved.
func (m *Model) handleCronPromptMsg(msg cronPromptMsg) (Model, tea.Cmd) {
	// If agent is idle, submit the cron prompt immediately.
	// If busy and queue_if_busy=true, queue for after current run.
	// If busy and queue_if_busy=false (default), skip this firing.
	// Append a doc-sync reminder if the prompt doesn't already mention docs.
	prompt := withDocSyncReminder(msg.Prompt)
	ts := time.Now().Format("15:04:05")
	// #1882: cron firings obey the same project-memory gate (#1762):
	// a firing inside the startup loading window would run its first
	// turn without the project-memory injection queued paths get.
	if !m.loading && !m.projectMemoryLoading {
		sysMsg := fmt.Sprintf("%s (%s)", m.t("cron.firing"), ts)
		m.suppressNextTunnelSystem = sysMsg
		m.chatWriteSystem(nextSystemID(), sysMsg)
		if broker := m.tunnelEventBroker(); broker != nil {
			broker.PushUserMessageData(tunnel.MessageData{
				Text:        msg.Prompt,
				DisplayText: sysMsg,
				Kind:        tunnel.MessageKindCron,
			})
		}
		m.emitIMText(sysMsg)
		return *m, m.submitHiddenText(prompt)
	}
	if msg.QueueIfBusy {
		sysMsg := fmt.Sprintf("%s (%s)", m.t("cron.firing"), ts)
		m.suppressNextTunnelSystem = sysMsg
		m.chatWriteSystem(nextSystemID(), sysMsg)
		m.queuePendingSubmissionHidden(prompt)
		// #903: queued successfully - must return here; the debug log
		// below then claimed 'skipped (queue_if_busy=false)', the exact
		// opposite of what happened.
		debug.Log("cron", "queued firing (agent busy, queue_if_busy=true): %s", msg.Prompt)
		return *m, nil
	}
	// queue_if_busy=false and agent busy: skip silently (debug log for observability)
	debug.Log("cron", "skipped firing (agent busy, queue_if_busy=false): %s", msg.Prompt)
	return *m, nil
}
