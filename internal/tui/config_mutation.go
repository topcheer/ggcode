package tui

import (
	tea "charm.land/bubbletea/v2"
)

// configMutationMsg is the single message type that carries a config
// mutation from a tea.Cmd goroutine back to the Update loop (#1367
// family root fix).
//
// The family bug: panel Cmd closures ran `m.config.IM.Enabled = true`
// (scalar write) and `AddIMAdapter` (map write + synchronous disk save)
// on the bubbletea Cmd goroutine while the Update loop's render path
// ranged over the same map every frame - Go concurrent map read/write
// is a fatal error, not a recoverable panic. Sites: dingtalk, discord,
// feishu, slack, unified IM editor, irc, matrix, mattermost, nostr, pc,
// provider/qq, skills, tg, mcp (14 files at time of writing).
//
// The fix pattern: the Cmd closure does only side-effect-free prep
// (parse, validate, build the adapter struct), then returns
// configMutationMsg. Update executes apply() ON THE MAIN LOOP (safe:
// no concurrent reader), then runs next() - which returns the
// follow-up Cmd for the slow IO (runtime start) as a fresh goroutine.
// Panel result messages are produced by next()'s chain, keeping every
// existing handler untouched.
type configMutationMsg struct {
	// apply mutates m.config (called on the Update loop, not a Cmd
	// goroutine). Must be quick: no network, no adapter start.
	apply func(m *Model) error
	// next runs AFTER apply succeeded (still on the Update loop) and
	// returns the follow-up tea.Cmd (e.g. async adapter start that
	// ends in the panel's existing result message). May be nil.
	next func(m *Model) tea.Cmd
	// fail builds the panel-specific failure message for apply's error.
	fail func(err error) tea.Msg
}

func (m Model) handleConfigMutationMsg(msg configMutationMsg) (Model, tea.Cmd) {
	if msg.apply == nil {
		return m, nil
	}
	if err := msg.apply(&m); err != nil {
		if msg.fail != nil {
			return m, func() tea.Msg { return msg.fail(err) }
		}
		return m, nil
	}
	if msg.next != nil {
		return m, msg.next(&m)
	}
	return m, nil
}
