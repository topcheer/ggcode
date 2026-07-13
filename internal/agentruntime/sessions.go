package agentruntime

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/agent"
	ctxpkg "github.com/topcheer/ggcode/internal/context"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/session"
)

type SessionSummary struct {
	ID          string
	Title       string
	Workspace   string
	Vendor      string
	Endpoint    string
	Model       string
	MsgCount    int
	UpdatedAt   time.Time
	LastMessage string
}

func WorkspaceMatches(sessionWorkspace, workingDir string) bool {
	normalizedWorkingDir := session.NormalizeWorkspacePath(workingDir)
	if normalizedWorkingDir == "" {
		return false
	}
	return session.NormalizeWorkspacePath(sessionWorkspace) == normalizedWorkingDir
}

func GroupWorkspaceSessions(sessions []*session.Session, workingDir string) ([]*session.Session, []*session.Session) {
	current := make([]*session.Session, 0, len(sessions))
	others := make([]*session.Session, 0, len(sessions))
	for _, ses := range sessions {
		if ses == nil {
			continue
		}
		if WorkspaceMatches(ses.Workspace, workingDir) {
			current = append(current, ses)
			continue
		}
		others = append(others, ses)
	}
	return current, others
}

func FilterWorkspaceSessions(sessions []*session.Session, workingDir string) []*session.Session {
	filtered, _ := GroupWorkspaceSessions(sessions, workingDir)
	sort.Slice(filtered, func(i, j int) bool {
		return filtered[i].UpdatedAt.After(filtered[j].UpdatedAt)
	})
	return filtered
}

// lastMessagePreview extracts a short preview of the last text message.
// It scans messages backward, skipping system messages and tool blocks,
// and returns the first non-empty text content truncated to ~80 chars.
func lastMessagePreview(msgs []provider.Message) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		m := msgs[i]
		if m.Role == "system" {
			continue
		}
		for _, block := range m.Content {
			if block.Type == "text" {
				text := strings.TrimSpace(block.Text)
				if text == "" {
					continue
				}
				// Take first line only
				if idx := strings.IndexByte(text, '\n'); idx > 0 {
					text = text[:idx]
				}
				text = strings.TrimSpace(text)
				if len([]rune(text)) > 80 {
					runes := []rune(text)
					text = string(runes[:77]) + "..."
				}
				return text
			}
		}
	}
	return ""
}

func SummarizeSession(ses *session.Session) SessionSummary {
	if ses == nil {
		return SessionSummary{}
	}
	return SessionSummary{
		ID:          ses.ID,
		Title:       ses.Title,
		Workspace:   ses.Workspace,
		Vendor:      ses.Vendor,
		Endpoint:    ses.Endpoint,
		Model:       ses.Model,
		MsgCount:    len(ses.Messages),
		UpdatedAt:   ses.UpdatedAt,
		LastMessage: lastMessagePreview(ses.Messages),
	}
}

func SummarizeWorkspaceSessions(sessions []*session.Session, workingDir string) []SessionSummary {
	filtered := FilterWorkspaceSessions(sessions, workingDir)
	summaries := make([]SessionSummary, 0, len(filtered))
	for _, ses := range filtered {
		summaries = append(summaries, SummarizeSession(ses))
	}
	return summaries
}

type SessionState struct {
	Session              *session.Session
	UsageTurnIndex       int
	LastMetricDigestTurn int
}

func AdoptSession(ses *session.Session) SessionState {
	turnIndex := session.LastTurnIndex(ses)
	return SessionState{
		Session:              ses,
		UsageTurnIndex:       turnIndex,
		LastMetricDigestTurn: turnIndex,
	}
}

func EnsureSession(store session.Store, current *session.Session, vendor, endpoint, model, workspace string) (SessionState, bool, error) {
	if current != nil {
		return AdoptSession(current), false, nil
	}
	if store == nil {
		return SessionState{}, false, fmt.Errorf("session store missing")
	}
	ses := session.NewSession(vendor, endpoint, model)
	ses.Workspace = workspace
	if err := store.Save(ses); err != nil {
		return SessionState{}, false, err
	}
	return AdoptSession(ses), true, nil
}

func LoadSession(store session.Store, id string) (SessionState, error) {
	if store == nil {
		return SessionState{}, fmt.Errorf("session store missing")
	}
	ses, err := store.Load(id)
	if err != nil {
		return SessionState{}, err
	}
	return AdoptSession(ses), nil
}

func ClearSession() SessionState {
	return SessionState{}
}

// DeleteSessionIfEmpty removes a session from the store if it has no
// user messages. Used to clean up ephemeral sessions that were created
// but never used (e.g., desktop auto-created when latest was locked).
func DeleteSessionIfEmpty(store session.Store, ses *session.Session) error {
	if store == nil || ses == nil {
		return nil
	}
	if len(ses.Messages) > 0 {
		return nil
	}
	return store.Delete(ses.ID)
}

// SaveSessionMessages overwrites ses.Messages with the given messages and
// does a FULL Save() rewrite of the JSONL file.
//
// ⚠️ WARNING: This is a destructive operation. If `messages` is the compacted
// version from agent.Messages(), all pre-compaction message records will be
// permanently lost from the JSONL file.
//
// In the TUI path, prefer persistFullSessionMessages() (incremental append).
// This function is retained for the desktop non-compaction path and initial
// session creation only.
func SaveSessionMessages(store session.Store, ses *session.Session, messages []provider.Message) error {
	if store == nil || ses == nil {
		return nil
	}
	ses.Messages = messages
	ses.UpdatedAt = time.Now()

	if len(ses.Messages) == 0 {
		return store.Delete(ses.ID)
	}

	if ses.Title == "" || ses.Title == "New session" {
		for _, msg := range ses.Messages {
			if msg.Role != "user" {
				continue
			}
			for _, block := range msg.Content {
				if block.Type != "text" || block.Text == "" {
					continue
				}
				text := block.Text
				if len([]rune(text)) > 60 {
					text = string([]rune(text)[:57]) + "..."
				}
				ses.Title = text
				return store.Save(ses)
			}
		}
	}

	return store.Save(ses)
}

func SaveAgentSessionSnapshot(store session.Store, ses *session.Session, agentInst *agent.Agent) error {
	if agentInst == nil {
		return SaveSessionMessages(store, ses, ses.Messages)
	}
	return SaveSessionMessages(store, ses, agentInst.Messages())
}

// SaveAgentSessionSnapshotWithExtra appends extra messages (e.g. turn
// digests) after the agent snapshot so they survive reload.
func SaveAgentSessionSnapshotWithExtra(store session.Store, ses *session.Session, agentInst *agent.Agent, extra []provider.Message) error {
	msgs := agentInst.Messages()
	if len(extra) > 0 {
		msgs = append(msgs, extra...)
	}
	return SaveSessionMessages(store, ses, msgs)
}

// RestoreSessionIntoAgent loads session messages into the agent's context
// manager and runs reconciliation + microcompaction. Returns whether
// microcompaction occurred and the before/after token counts.
func RestoreSessionIntoAgent(agentInst *agent.Agent, ses *session.Session) (compacted bool, beforeTokens int, afterTokens int) {
	if agentInst == nil || ses == nil {
		return false, 0, 0
	}
	msgs := ses.ContextMessages
	if len(msgs) == 0 {
		msgs = ses.Messages
	}

	if ses.CheckpointTokens > 0 && ses.CheckpointMessageCount > 0 && ses.CheckpointMessageCount <= len(msgs) {
		cpCount := ses.CheckpointMessageCount

		// 1. Add checkpoint messages (their tokens are estimated by Add(),
		//    but we'll override with the real value next).
		for _, msg := range msgs[:cpCount] {
			agentInst.AddMessage(msg)
		}

		// 2. Set baseline to the real checkpoint token count.
		//    This replaces the estimate; baselineDelta resets to 0.
		if cm, ok := agentInst.ContextManager().(*ctxpkg.Manager); ok {
			cm.SetCheckpointBaseline(ses.CheckpointTokens)
		}

		// 3. Add post-checkpoint messages. Since baselineAvailable=true,
		//    each Add() increments baselineDelta with its estimated tokens.
		//    Final: tokenCount = checkpoint_tokens + post-checkpoint estimate.
		for _, msg := range msgs[cpCount:] {
			agentInst.AddMessage(msg)
		}
	} else {
		// No checkpoint — load all with estimation.
		for _, msg := range msgs {
			agentInst.AddMessage(msg)
		}
	}

	// Apply session-level ContextWindow/MaxTokens BEFORE microcompact so the
	// threshold check uses the correct context window. These take priority
	// over endpoint/per-model config when restoring a session.
	if ses.ContextWindow > 0 {
		agentInst.ContextManager().SetContextWindow(ses.ContextWindow)
	}
	if ses.MaxTokens > 0 {
		agentInst.ContextManager().SetOutputReserve(ses.MaxTokens)
	}

	agentInst.ReconcileToolCalls()
	compacted, beforeTokens, afterTokens = agentInst.MicrocompactIfOverThreshold()

	// Clear runAdded: AddMessage() during restore populated it, but these
	// messages already exist in the JSONL file. Without this, the next
	// persistFullSessionMessages() would re-write all of them back to disk,
	// doubling the session file on every restart.
	agentInst.StartRunTracking()

	return compacted, beforeTokens, afterTokens
}
