package agentruntime

import (
	"fmt"
	"sort"
	"time"

	"github.com/topcheer/ggcode/internal/agent"
	ctxpkg "github.com/topcheer/ggcode/internal/context"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/session"
)

type SessionSummary struct {
	ID        string
	Title     string
	Workspace string
	Vendor    string
	Endpoint  string
	Model     string
	MsgCount  int
	UpdatedAt time.Time
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

func SummarizeSession(ses *session.Session) SessionSummary {
	if ses == nil {
		return SessionSummary{}
	}
	return SessionSummary{
		ID:        ses.ID,
		Title:     ses.Title,
		Workspace: ses.Workspace,
		Vendor:    ses.Vendor,
		Endpoint:  ses.Endpoint,
		Model:     ses.Model,
		MsgCount:  len(ses.Messages),
		UpdatedAt: ses.UpdatedAt,
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

func RestoreSessionIntoAgent(agentInst *agent.Agent, ses *session.Session) {
	if agentInst == nil || ses == nil {
		return
	}
	// ⚠️ Use ContextMessages (compacted checkpoint + post-checkpoint),
	// NOT ses.Messages. ses.Messages is the full conversation history
	// for TUI rendering — feeding it to the agent would bypass compaction
	// and send the entire raw history to the LLM.
	msgs := ses.ContextMessages
	if len(msgs) == 0 {
		msgs = ses.Messages // fallback when no checkpoint exists
	}

	if ses.CheckpointTokens > 0 && ses.CheckpointMessageCount > 0 && ses.CheckpointMessageCount <= len(msgs) {
		// Phase 1: Load checkpoint messages and set precise baseline.
		cpMsgs := msgs[:ses.CheckpointMessageCount]
		for _, msg := range cpMsgs {
			agentInst.AddMessage(msg)
		}
		if cm, ok := agentInst.ContextManager().(*ctxpkg.Manager); ok {
			cm.SetCheckpointBaseline(ses.CheckpointTokens)
		}
		// Phase 2: Load post-checkpoint messages — their tokens increment
		// baselineDelta normally (tool results, assistant responses, etc.).
		for _, msg := range msgs[ses.CheckpointMessageCount:] {
			agentInst.AddMessage(msg)
		}
	} else {
		// No checkpoint — load all messages with local estimation.
		for _, msg := range msgs {
			agentInst.AddMessage(msg)
		}
	}

	// Reconcile tool_calls: if the last assistant message has unpaired tool_use
	// blocks (no matching tool_result blocks in subsequent messages), add a user
	// message with cancelled tool_result entries. This handles session files saved
	// while a tool execution was still pending (e.g. the process crashed or was
	// interrupted).
	agentInst.ReconcileToolCalls()
}
