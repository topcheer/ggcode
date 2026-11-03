package agentruntime

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/agent"
	ctxpkg "github.com/topcheer/ggcode/internal/context"
	"github.com/topcheer/ggcode/internal/debug"
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

func DeleteSessionIfEmpty(store session.Store, ses *session.Session) error {
	if store == nil || ses == nil {
		return nil
	}
	if len(ses.Messages) > 0 {
		return nil
	}
	return store.Delete(ses.ID)
}

func RestoreSessionIntoAgent(agentInst *agent.Agent, ses *session.Session) (compacted bool, beforeTokens int, afterTokens int) {
	if agentInst == nil || ses == nil {
		return false, 0, 0
	}
	msgs := ses.ContextMessages
	if len(msgs) == 0 {
		msgs = ses.Messages
	}

	for _, msg := range msgs {
		agentInst.AddMessage(msg)
	}

	if cm, ok := agentInst.ContextManager().(*ctxpkg.Manager); ok {
		cand := 0
		if n := len(ses.UsageHistory); n > 0 {
			last := ses.UsageHistory[n-1]
			if last.Usage.InputTokens > 0 {
				cand = last.Usage.InputTokens + last.Usage.OutputTokens
			}
		}
		if cand == 0 && ses.CheckpointTokens > 0 {
			cand = ses.CheckpointTokens
		}

		if est := agentInst.ContextManager().TokenCount(); est > 0 && cand > 0 && cand < est/2 {
			debug.Log("agentruntime", "restore baseline stale (under-reports restored context): usage=%d vs restored estimate=%d for session %s -- using estimate",
				cand, est, ses.ID)
			cand = est
		}
		if cand > 0 {
			cm.SetCheckpointBaseline(cand)
		}
	}

	if ses.ContextWindow > 0 {
		agentInst.ContextManager().SetContextWindow(ses.ContextWindow)
	}
	if ses.MaxTokens > 0 {
		agentInst.ContextManager().SetOutputReserve(ses.MaxTokens)
	}

	agentInst.ReconcileToolCalls()
	compacted, beforeTokens, afterTokens = agentInst.MicrocompactIfOverThreshold()

	agentInst.StartRunTracking()

	return compacted, beforeTokens, afterTokens
}

func CheckCrashRecovery(sessionID string) (string, string) {
	info := agent.CheckCrashedRun(sessionID)
	return agent.FormatCrashRecoveryMessage(info), crashResumeTask(info)
}

func crashResumeTask(info *agent.CrashRecoveryInfo) string {
	if info == nil || info.UserPrompt == "" {
		return ""
	}
	if strings.HasPrefix(info.UserPrompt, "<journal corrupted") {
		return ""
	}
	return info.UserPrompt
}

func SeedCrashDedupWindow(a *agent.Agent, sessionID string) {
	agent.SeedCrashDedup(a, sessionID)
}

func CheckContinuation(sessionID string) string {
	return agent.CheckContinuation(sessionID)
}
