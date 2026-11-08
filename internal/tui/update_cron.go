package tui

import (
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/tunnel"
)

func (m *Model) handleCronPromptMsg(msg cronPromptMsg) (Model, tea.Cmd) {

	prompt := withDocSyncReminder(msg.Prompt)
	ts := time.Now().Format("15:04:05")

	if !m.loading && !m.projectMemoryLoading {
		sysMsg := fmt.Sprintf("[cron] %s (%s)", m.t("cron.firing"), ts)
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
		sysMsg := fmt.Sprintf("[cron] %s (%s)", m.t("cron.firing"), ts)
		m.suppressNextTunnelSystem = sysMsg
		m.chatWriteSystem(nextSystemID(), sysMsg)
		m.queuePendingSubmissionHidden(prompt)

		debug.Log("cron", "queued firing (agent busy, queue_if_busy=true): %s", msg.Prompt)
		return *m, nil
	}

	sysMsg := fmt.Sprintf("[cron] %s (%s)", m.t("cron.skipped_busy"), ts)
	m.chatWriteSystem(nextSystemID(), sysMsg)
	debug.Log("cron", "skipped firing (agent busy, queue_if_busy=false): %s", msg.Prompt)
	return *m, nil
}
