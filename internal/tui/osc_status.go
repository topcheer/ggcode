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

const (
	oscApp            = "ggcode"
	oscStateIdle      = "idle"
	oscStateWorking   = "working"
	oscStateDone      = "done"
	oscStateBlocked   = "blocked"
	oscStateError     = "error"
	oscKindQuestion   = "question"
	oscKindPermission = "permission"

	oscMsgMax = 120
)

var (
	oscOut   io.Writer = os.Stdout
	oscIsTTY           = func() bool { return term.IsTerminal(int(os.Stdout.Fd())) }
)

func (m *Model) oscStatusEnabled() bool {
	return m != nil && m.config != nil && m.config.StatusLine.OSC7501
}

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
