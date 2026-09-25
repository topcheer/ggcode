package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/handoff"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/safego"
	"github.com/topcheer/ggcode/internal/session"
	"github.com/topcheer/ggcode/internal/task"
)

// handleHandoffCommand implements /handoff: context reset with a
// structured handoff artifact (Anthropic, "Harness design for
// long-running application development", Mar 2026).
//
// Unlike /compact (in-place summarization, same agent, no clean slate)
// and /clear (clean slate, no handoff), /handoff resets to a fresh
// session AND seeds its context with a deterministic briefing built from
// session-local facts: user goals, the live task board and a git
// snapshot. Zero LLM calls — instant and offline-safe.
//
// The choreography mirrors handleClearChat (guards, sub-agent teardown,
// meta flush, new-session persistence, switchToSession) — keep the two in
// sync if session-switch invariants change (#541, #688).
func (m *Model) handleHandoffCommand() {
	if m.loading {
		m.chatWriteSystem(nextSystemID(), m.t("session.switch_blocked_running"))
		return
	}
	if m.agent == nil {
		m.chatWriteSystem(nextSystemID(), m.t("compact.unavailable"))
		return
	}

	// Harvest state BEFORE the reset wipes it.
	artifact, oldID, boardJSON := m.buildHandoffArtifact()
	workDir := m.agentWorkingDirOrCwd()
	artifactPath := m.writeHandoffArtifact(workDir, artifact, oldID)

	m.teardownBeforeHandoff()
	ses := m.createHandoffSession(oldID, boardJSON, artifact)
	m.switchToSession(ses, true)

	if artifactPath != "" {
		m.chatWriteSystem(nextSystemID(), m.t("handoff.done", ses.ID, artifactPath))
	} else {
		m.chatWriteSystem(nextSystemID(), m.t("handoff.done_noart", ses.ID))
	}
}

// agentWorkingDirOrCwd returns the agent's working directory, falling back
// to the process CWD.
func (m *Model) agentWorkingDirOrCwd() string {
	if dir := m.agent.WorkingDir(); dir != "" {
		return dir
	}
	return cwdOrEmpty()
}

// buildHandoffArtifact harvests session-local facts (user goals, live task
// board, git snapshot) and renders the handoff artifact. Zero LLM calls.
func (m *Model) buildHandoffArtifact() (artifact, oldID string, boardJSON []byte) {
	var goals []string
	if m.session != nil {
		oldID = m.session.ID
		goals = handoff.ExtractGoals(m.session.Messages)
	}
	tasksStats, tasksDigest := "", ""
	if m.taskMgr != nil {
		if data, err := m.taskMgr.SnapshotJSON(); err == nil {
			boardJSON = data
		} else {
			debug.Log("tui", "handoff board snapshot: %v", err)
		}
		tasksStats = handoffTaskStats(boardJSON)
		tasksDigest = m.taskMgr.Digest(20, 1500)
	}
	artifact = handoff.Render(handoff.Snapshot{
		OldSessionID: oldID,
		Model:        m.agentModelString(),
		GeneratedAt:  time.Now(),
		Goals:        goals,
		TasksStats:   tasksStats,
		TasksDigest:  tasksDigest,
		Git:          handoff.CollectGit(m.agentWorkingDirOrCwd()),
	})
	return artifact, oldID, boardJSON
}

// handoffTaskStats formats the board summary line for the artifact.
func handoffTaskStats(boardJSON []byte) string {
	completed, inProgress, pending, ok := task.BoardStats(boardJSON)
	if !ok || completed+inProgress+pending == 0 {
		return ""
	}
	return fmt.Sprintf("%d completed · %d in progress · %d pending",
		completed, inProgress, pending)
}

// writeHandoffArtifact persists the artifact under .ggcode/handoffs.
// Best-effort: the briefing is already seeded into the fresh context even
// if the file write fails; failures are logged and reflected in "".
func (m *Model) writeHandoffArtifact(workDir, artifact, oldID string) string {
	if workDir == "" {
		return ""
	}
	dir := filepath.Join(workDir, ".ggcode", "handoffs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		debug.Log("tui", "handoff artifact dir: %v", err)
		return ""
	}
	path := filepath.Join(dir, fmt.Sprintf("handoff-%s-%d.md",
		handoff.SanitizeToken(oldID), time.Now().Unix()))
	if err := os.WriteFile(path, []byte(artifact), 0o644); err != nil {
		debug.Log("tui", "handoff artifact write: %v", err)
		return ""
	}
	return path
}

// teardownBeforeHandoff cancels sub-agents/swarm and flushes the old
// session's metadata — same teardown as handleClearChat: sub-agent results
// belong to the old session and must not pollute the new one (NEVER Save()
// — it would destroy disk-only agent messages).
func (m *Model) teardownBeforeHandoff() {
	if m.subAgentMgr != nil {
		safego.Go("tui.handoff.cancelSubAgents", func() {
			m.subAgentMgr.CancelAll()
		})
	}
	if m.swarmMgr != nil {
		safego.Go("tui.handoff.cancelSwarm", func() {
			m.swarmMgr.CancelAll()
		})
	}
	if m.session != nil && m.sessionStore != nil {
		oldSes := m.session
		oldStore := m.sessionStore
		m.snapshotTasksInto(oldSes)
		safego.Go("tui.handoff.metaFlush", func() {
			if jsonlStore, ok := oldStore.(*session.JSONLStore); ok {
				if err := jsonlStore.AppendMetaToDisk(oldSes); err != nil {
					debug.Log("tui", "handoff oldSes meta persist: %v", err)
				}
			}
		})
	}
}

// createHandoffSession builds the fresh session seeded with the handoff:
// the briefing becomes the entire LLM context (ContextMessages takes
// precedence over Messages in RestoreSessionIntoAgent), and the task board
// survives the reset via TasksJSON (same carry-over as /branch).
func (m *Model) createHandoffSession(oldID string, boardJSON []byte, artifact string) *session.Session {
	vendor, endpoint, model := "", "", ""
	if m.config != nil {
		vendor = m.config.Vendor
		endpoint = m.config.Endpoint
		model = m.config.Model
	}
	ses := session.NewSession(vendor, endpoint, model)
	ses.TasksJSON = boardJSON
	ses.ContextMessages = []provider.Message{handoff.SystemMessage(artifact)}
	if oldID != "" {
		ses.Title = "Handoff from " + shortSessionID(oldID)
	}
	if m.sessionStore != nil {
		if err := m.sessionStore.Save(ses); err != nil {
			debug.Log("tui", "handoff persist new session: %v", err)
		}
	}
	return ses
}

// agentModelString renders vendor/endpoint/model for artifact provenance.
func (m *Model) agentModelString() string {
	if m.config == nil {
		return ""
	}
	parts := make([]string, 0, 3)
	for _, s := range []string{m.config.Vendor, m.config.Endpoint, m.config.Model} {
		if s != "" {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, "/")
}
