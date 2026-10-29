package tui

import (
	"encoding/base64"
	"io"
	"os"
	"strings"
	"unicode/utf8"

	"golang.org/x/term"

	"github.com/topcheer/ggcode/internal/debug"
)

// OSC 7501 Program Status Protocol (spec by Mitchell Hashimoto, 2026-10-06:
// https://mitchellh.com/writing/program-status-osc7501). The program tells
// the terminal what it is doing via one short escape sequence:
//
//	ESC ] 7501 ; state=<s>:kind=<k>:app=<app>:msg=<base64> ESC \
//
// so terminals, tmux panes and "agentic inbox" tools (Herdr, cmux, Agent
// Deck) can show which agents are working, blocked on the user, or done -
// without screen-scraping heuristics. Well-behaved terminals ignore unknown
// OSCs, so emitting is safe everywhere; we still gate on a TTY stdout and an
// explicit opt-in config flag (statusline.osc_7501) so piped/CI output and
// default installs stay byte-identical.
//
// ggcode state mapping:
//   - idle    TUI started, at rest waiting for the next instruction
//   - working agent run in flight
//   - blocked waiting on the user: kind=permission (approval / diff confirm)
//     or kind=question (ask_user questionnaire)
//   - done    run finished, result rendered but not necessarily seen
//   - error   run failed and stopped
const (
	oscApp            = "ggcode"
	oscStateIdle      = "idle"
	oscStateWorking   = "working"
	oscStateDone      = "done"
	oscStateBlocked   = "blocked"
	oscStateError     = "error"
	oscKindQuestion   = "question"
	oscKindPermission = "permission"

	// oscMsgMax caps the human-readable reason before base64 so the whole
	// sequence stays well under terminal input-buffer limits.
	oscMsgMax = 120
)

// Test seams: override the destination and the TTY check.
var (
	oscOut   io.Writer = os.Stdout
	oscIsTTY           = func() bool { return term.IsTerminal(int(os.Stdout.Fd())) }
)

// oscStatusEnabled reports whether OSC 7501 emission is switched on.
func (m *Model) oscStatusEnabled() bool {
	return m != nil && m.config != nil && m.config.StatusLine.OSC7501
}

// emitOSCStatus writes one OSC 7501 record. kind is only meaningful for
// state=blocked (spec: permission, question, auth); msg is a single
// human-readable line, base64-encoded per the spec. Failures are logged and
// swallowed: status signaling must never break the TUI.
func (m *Model) emitOSCStatus(state, kind, msg string) {
	if !m.oscStatusEnabled() || !oscIsTTY() {
		return
	}
	var b strings.Builder
	b.WriteString("\x1b]7501;state=")
	b.WriteString(state)
	if state == oscStateBlocked && kind != "" {
		b.WriteString(":kind=")
		b.WriteString(kind)
	}
	b.WriteString(":app=")
	b.WriteString(oscApp)
	if trimmed := strings.TrimSpace(oscTruncateUTF8(msg, oscMsgMax)); trimmed != "" {
		b.WriteString(":msg=")
		b.WriteString(base64.StdEncoding.EncodeToString([]byte(trimmed)))
	}
	b.WriteString("\x1b\\")
	if _, err := io.WriteString(oscOut, b.String()); err != nil {
		debug.Log("tui", "osc7501: write: %v", err)
	}
}

// oscTruncateUTF8 cuts s to at most max runes on a rune boundary.
func oscTruncateUTF8(s string, max int) string {
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	count := 0
	for i := range s {
		if count == max {
			return s[:i]
		}
		count++
	}
	return s
}

func (m *Model) oscIdle()              { m.emitOSCStatus(oscStateIdle, "", "") }
func (m *Model) oscWorking(msg string) { m.emitOSCStatus(oscStateWorking, "", msg) }
func (m *Model) oscDone(msg string)    { m.emitOSCStatus(oscStateDone, "", msg) }
func (m *Model) oscError(msg string)   { m.emitOSCStatus(oscStateError, "", msg) }
func (m *Model) oscBlocked(kind, msg string) {
	m.emitOSCStatus(oscStateBlocked, kind, msg)
}

// oscRunOutcome emits the terminal state for a finished run: error for a
// failed run, idle for a user-canceled one (the user already knows; there is
// no unseen result), done otherwise.
func (m *Model) oscRunOutcome(failed, canceled bool) {
	switch {
	case failed:
		m.oscError("Run failed")
	case canceled:
		m.oscIdle()
	default:
		m.oscDone("Run finished")
	}
}
