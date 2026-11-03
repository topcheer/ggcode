package tui

import (
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/tool"
)

type tunnelShareControlAdapter struct {
	m *Model

	send func(tea.Msg)
}

const shareControlReplyTimeout = 45 * time.Second

func (a tunnelShareControlAdapter) StartAgentShare() (tool.ShareStartResult, error) {
	if a.send == nil {
		return tool.ShareStartResult{}, errShareNoProgram
	}
	reply := make(chan agentShareReply, 1)
	a.send(agentShareRequestMsg{reply: reply})
	select {
	case r := <-reply:
		if r.err != nil {
			return tool.ShareStartResult{}, r.err
		}
		return tool.ShareStartResult{ConnectURL: r.connectURL}, nil
	case <-time.After(shareControlReplyTimeout):
		return tool.ShareStartResult{}, errShareTimeout
	}
}

func (a tunnelShareControlAdapter) StopAgentShare() error {
	if a.send == nil {
		return errShareNoProgram
	}
	reply := make(chan agentShareReply, 1)
	a.send(agentShareRequestMsg{stop: true, reply: reply})
	select {
	case <-reply:
		return nil
	case <-time.After(10 * time.Second):
		return errShareTimeout
	}
}

func (a tunnelShareControlAdapter) ShareActive() bool {
	if a.m.tunnelHost == nil {
		return false
	}
	return a.m.tunnelHost.GetShareInfo() != nil
}

type shareCtrlErr string

func (e shareCtrlErr) Error() string { return string(e) }

const (
	errShareNoProgram = shareCtrlErr("no running UI program")
	errShareTimeout   = shareCtrlErr("share control reply timed out")
)

func (r *REPL) injectShareController() {
	if r.agent == nil {
		debug.Log("tui", "injectShareController: SKIPPED (agent nil)")
		return
	}
	reg := r.agent.ToolRegistry()
	if reg == nil {
		debug.Log("tui", "injectShareController: SKIPPED (registry nil)")
		return
	}
	adapter := tunnelShareControlAdapter{
		m:    &r.model,
		send: func(msg tea.Msg) { r.sendTUI(msg) },
	}
	injected := 0
	if t, ok := reg.Get(tool.StartShareTool{}.Name()); ok {
		if st, ok := t.(tool.StartShareTool); ok {
			st.Controller = adapter

			reg.Unregister(st.Name())
			if err := reg.Register(st); err == nil {
				injected++
			} else {
				debug.Log("tui", "injectShareController: start_share re-register failed: %v", err)
			}
		} else {
			debug.Log("tui", "injectShareController: start_share type mismatch %T", t)
		}
	} else {
		debug.Log("tui", "injectShareController: start_share NOT FOUND in registry")
	}
	if t, ok := reg.Get(tool.StopShareTool{}.Name()); ok {
		if st, ok := t.(tool.StopShareTool); ok {
			st.Controller = adapter
			reg.Unregister(st.Name())
			if err := reg.Register(st); err == nil {
				injected++
			} else {
				debug.Log("tui", "injectShareController: stop_share re-register failed: %v", err)
			}
		} else {
			debug.Log("tui", "injectShareController: stop_share type mismatch %T", t)
		}
	} else {
		debug.Log("tui", "injectShareController: stop_share NOT FOUND in registry")
	}
	debug.Log("tui", "injectShareController: done injected=%d", injected)
}
