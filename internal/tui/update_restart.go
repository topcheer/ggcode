package tui

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/topcheer/ggcode/internal/debug"
)

func (m *Model) handleArmRestartMsg(msg armRestartMsg) (Model, tea.Cmd) {
	m.pendingRestart = true
	m.restartArmedAt = time.Now()
	if msg.debug {
		m.restartDebug = true
	}

	if strings.TrimSpace(msg.reason) != "" {
		m.chatWriteSystem("restart-announce", fmt.Sprintf("Restarting: %s (your session resumes automatically)", msg.reason))
	}
	if !m.loading {
		debug.Log("restart", "agent-requested restart: agent idle, firing now")
		return *m, tea.Batch(firePendingRestartCmd(), armRestartFallbackCmd())
	}
	debug.Log("restart", "agent-requested restart: agent busy, deferring to turn end (30s fallback)")
	return *m, armRestartFallbackCmd()
}

func (m *Model) handleRestartFallbackMsg(msg restartFallbackMsg) (Model, tea.Cmd) {

	if !m.pendingRestart {
		return *m, nil
	}
	if m.loading && time.Since(m.lastTurnActivityAt) < restartFallbackTimeout {
		// #5367: hard cap anchored to the original arm time. A continuously
		// active turn (autonomous/cron loop) would otherwise re-arm forever
		// and starve the restart past the arm announcement.
		if !m.restartArmedAt.IsZero() && time.Since(m.restartArmedAt) >= restartArmHardCap {
			debug.Log("restart", "restart armed %s behind a continuously active turn; firing hard cap", restartArmHardCap)
			m.chatWriteSystem("restart-warn", fmt.Sprintf("Restart armed %s ago never went idle (turn kept running); firing hard cap now. In-flight turn work may be interrupted.", restartArmHardCap))
			return *m, firePendingRestartCmd()
		}
		debug.Log("restart", "restart fallback fired but turn is active; re-arming")
		return *m, armRestartFallbackCmd()
	}
	debug.Log("restart", "agent-requested restart: turn stalled or finished, forcing restart")
	return *m, firePendingRestartCmd()
}

func (m *Model) handleRemoteRestartMsg(msg remoteRestartMsg) (Model, tea.Cmd) {

	if m.loading && !m.pendingRestart {
		if msg.explicit {
			debug.Log("restart", "explicit restart during active turn; arming to fire at turn end")
			m.pendingRestart = true
			m.restartArmedAt = time.Now()
			m.noteTurnActivity()
			return *m, armRestartFallbackCmd()
		}
		debug.Log("restart", "remoteRestartMsg during a new active turn; ignoring")
		return *m, nil
	}
	m.quitting = true
	m.restartRequested = true
	m.shutdownAll()
	return *m, tea.Quit
}
