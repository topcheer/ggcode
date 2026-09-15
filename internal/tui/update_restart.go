package tui

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/topcheer/ggcode/internal/debug"
)

// handleArmRestartMsg arms an agent-requested restart (#347): defers the quit
// until the current agent turn finishes so sibling tool results and trailing
// assistant text are persisted first (#2422 slice 1: extracted from
// Model.Update - body moved verbatim, zero behavior change).
func (m *Model) handleArmRestartMsg(msg armRestartMsg) (Model, tea.Cmd) {
	m.pendingRestart = true
	if msg.debug {
		m.restartDebug = true
	}
	// #1698 case 3: announce WHY the session is restarting - the
	// schema promises the reason is shown to the user before the
	// process restarts.
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

// handleRestartFallbackMsg is the 30s stall fallback: force the restart only
// when the turn is NOT making progress (#2422 slice 1: extracted from
// Model.Update - body moved verbatim, zero behavior change).
func (m *Model) handleRestartFallbackMsg(msg restartFallbackMsg) (Model, tea.Cmd) {
	// A tick landing while tools are legitimately still running (LLM called
	// restart + a >30s build in the same batch) used to kill the turn
	// mid-tool and lose unpersisted results — the exact hazard #347 was
	// meant to prevent (#362). Recent stream/reasoning/tool activity
	// re-arms the timer; only a genuinely stalled turn (no activity for the
	// full fallback window) forces the quit.
	if !m.pendingRestart {
		return *m, nil
	}
	if m.loading && time.Since(m.lastTurnActivityAt) < restartFallbackTimeout {
		debug.Log("restart", "restart fallback fired but turn is active; re-arming")
		return *m, armRestartFallbackCmd()
	}
	debug.Log("restart", "agent-requested restart: turn stalled or finished, forcing restart")
	return *m, firePendingRestartCmd()
}

// handleRemoteRestartMsg quits for an external restart request (#2422 slice 1:
// extracted from Model.Update - body moved verbatim, zero behavior change).
func (m *Model) handleRemoteRestartMsg(msg remoteRestartMsg) (Model, tea.Cmd) {
	// Guard (#362): firePendingRestartCmd is an async Cmd — a user message
	// submitted in the millisecond window after a turn ended can already
	// have started a NEW run (m.loading=true, pendingRestart stale).
	// Quitting then would kill the new turn.
	// Explicit user requests (IM /restart, desktop InjectRestart) are
	// exempt: being silently swallowed after the user already received a
	// "Restarting..." confirmation is worse than deferring. They arm the
	// restart to fire at turn end instead (#374).
	if m.loading && !m.pendingRestart {
		if msg.explicit {
			debug.Log("restart", "explicit restart during active turn; arming to fire at turn end")
			m.pendingRestart = true
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
