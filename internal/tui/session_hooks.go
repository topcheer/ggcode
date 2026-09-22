package tui

import (
	"github.com/topcheer/ggcode/internal/hooks"
	"github.com/topcheer/ggcode/internal/session"
)

// SetNextSessionSource records why the NEXT session transition happens so
// on_session_start hooks receive an accurate source ("startup", "resume",
// "new", "branch"). Transitions that do not set one default to "resume".
// The value is consumed by the next SetSession call.
func (m *Model) SetNextSessionSource(source string) {
	m.nextSessionSource = source
}

// consumeSessionSource returns and clears the pending session source.
func (m *Model) consumeSessionSource() string {
	src := m.nextSessionSource
	m.nextSessionSource = ""
	if src == "" {
		src = "resume"
	}
	return src
}

// fireSessionStartHooks fires on_session_start hooks (fire-and-forget) after
// a session has been bound to the model. Called from SetSession, which every
// session transition path goes through.
func (m *Model) fireSessionStartHooks() {
	if m.config == nil || m.session == nil {
		return
	}
	cfg := m.config.Hooks
	if len(cfg.SessionStart) == 0 {
		return
	}
	hooks.RunSessionStartHooks(cfg, hooks.HookEnv{
		SessionID:     m.session.ID,
		Workspace:     m.session.Workspace,
		WorkingDir:    m.agentWorkingDir(),
		SessionSource: m.consumeSessionSource(),
	})
}

// fireSessionEndHooks fires on_session_end hooks (fire-and-forget) for the
// session that is being closed or switched away from. reason is one of
// "exit", "cleared", "branched".
func (m *Model) fireSessionEndHooks(ses *session.Session, reason string) {
	if m.config == nil || ses == nil {
		return
	}
	cfg := m.config.Hooks
	if len(cfg.SessionEnd) == 0 {
		return
	}
	hooks.RunSessionEndHooks(cfg, hooks.HookEnv{
		SessionID:        ses.ID,
		Workspace:        ses.Workspace,
		WorkingDir:       m.agentWorkingDir(),
		SessionEndReason: reason,
	})
}

func (m *Model) agentWorkingDir() string {
	if m.agent != nil {
		return m.agent.WorkingDir()
	}
	return ""
}
