package tui

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/safego"
)

// External status line (sa-160), mirroring the 2026 Claude Code `statusLine`
// convention: a user-configured shell command receives a JSON session
// snapshot on stdin; its first stdout line is rendered as a persistent bar
// above the composer. Refreshes fire at run boundaries (never per frame) and
// are serialized: while one invocation is in flight, further requests mark
// the state dirty and a single follow-up runs after completion. A failed or
// timed-out invocation keeps the last good output.

const statuslineDefaultTimeout = 2 * time.Second

// statuslineMsg carries a finished external refresh back into the TUI.
// ok=false marks a failed/timed-out invocation: the cached last good output
// must be kept (#2748).
type statuslineMsg struct {
	seq  int
	text string
	ok   bool
}

// statuslineState caches the rendered external status line and serializes
// refreshes. Lives behind a pointer on Model: Model is copied by value
// across update/render boundaries, so the mutex must not be a direct Model
// field (go vet copylocks).
type statuslineState struct {
	mu      sync.Mutex
	text    string
	seq     int
	running bool
	dirty   bool
}

type statuslineModelInfo struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
}

type statuslineWorkspace struct {
	CurrentDir string `json:"current_dir"`
	ProjectDir string `json:"project_dir"`
}

type statuslineContextInfo struct {
	UsedTokens  int     `json:"used_tokens"`
	TotalTokens int     `json:"total_tokens"`
	PercentUsed float64 `json:"percent_used"`
}

type statuslineCostInfo struct {
	SessionUSD float64 `json:"session_usd"`
}

// statuslinePayload is the stdin schema. Core fields (hook_event_name,
// session_id, model, workspace, version) follow the Claude Code statusline
// JSON so existing community scripts keep working; context/cost are ggcode
// extensions.
type statuslinePayload struct {
	HookEventName string                `json:"hook_event_name"`
	SessionID     string                `json:"session_id"`
	Version       string                `json:"version"`
	Model         statuslineModelInfo   `json:"model"`
	Workspace     statuslineWorkspace   `json:"workspace"`
	Context       statuslineContextInfo `json:"context"`
	Cost          statuslineCostInfo    `json:"cost"`
}

func (m *Model) buildStatuslinePayload() statuslinePayload {
	p := statuslinePayload{
		HookEventName: "statusline",
		Version:       "ggcode",
	}
	if cwd, err := os.Getwd(); err == nil {
		p.Workspace.CurrentDir = cwd
		p.Workspace.ProjectDir = cwd
	}
	if m.session != nil {
		p.SessionID = m.session.ID
	}
	if m.config != nil {
		if _, _, model := m.currentSelection(); model != "" {
			p.Model.ID = model
			p.Model.DisplayName = model
		}
		if ep := m.config.ActiveEndpointConfig(); ep != nil && ep.ContextWindow > 0 {
			p.Context.TotalTokens = ep.ContextWindow
		}
	}
	if m.agent != nil {
		if cm := m.agent.ContextManager(); cm != nil {
			p.Context.UsedTokens = cm.TokenCount()
			p.Context.PercentUsed = cm.UsageRatio() * 100
		}
	}
	p.Cost.SessionUSD = m.estimateSessionCost()
	return p
}

// runStatuslineCommand executes one external refresh synchronously and
// returns the first stdout line. On timeout or non-zero exit it returns
// ("", false) so the caller keeps the cached last good output (#2748).
func runStatuslineCommand(command string, payload statuslinePayload, timeout time.Duration) (string, bool) {
	input, err := json.Marshal(payload)
	if err != nil {
		debug.Log("tui", "statusline: payload marshal: %v", err)
		return "", false
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(ctx, "cmd", "/c", command)
	} else {
		cmd = exec.CommandContext(ctx, "sh", "-c", command)
	}
	// The shell may fork the real script as a grandchild that inherits the
	// stdout pipe; killing only the direct child leaves the grandchild alive
	// holding the pipe, so cmd.Output() blocks until it exits naturally (CI
	// showed a 5s `sleep 5` surviving an 80ms timeout). Kill the whole
	// process group on timeout, and keep WaitDelay as the cross-platform
	// backstop that force-closes the pipes if any survivor still holds them.
	cmd.SysProcAttr = statuslineProcAttr()
	cmd.Cancel = func() error { return statuslineCancelKill(cmd) }
	cmd.WaitDelay = time.Second
	cmd.Stdin = bytes.NewReader(input)
	out, err := cmd.Output()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			debug.Log("tui", "statusline: command timeout after %s", timeout)
		} else {
			debug.Log("tui", "statusline: command failed: %v", err)
		}
		return "", false
	}
	first := out
	if i := bytes.IndexByte(out, '\n'); i >= 0 {
		first = out[:i]
	}
	return strings.TrimSpace(strings.TrimRight(string(first), "\r")), true
}

// refreshStatusline fires an async external refresh unless one is already in
// flight (then it marks the state dirty; the in-flight completion requeues
// exactly one follow-up).
func (m *Model) refreshStatusline() {
	if m.config == nil || !m.config.StatusLine.Enabled() {
		return
	}
	if m.statusline == nil {
		m.statusline = &statuslineState{}
	}
	sl := m.statusline
	sl.mu.Lock()
	if sl.running {
		sl.dirty = true
		sl.mu.Unlock()
		return
	}
	sl.running = true
	seq := sl.seq + 1
	sl.seq = seq
	sl.mu.Unlock()

	command := strings.TrimSpace(m.config.StatusLine.Command)
	timeout := statuslineDefaultTimeout
	if ms := m.config.StatusLine.TimeoutMS; ms > 0 {
		timeout = time.Duration(ms) * time.Millisecond
	}
	payload := m.buildStatuslinePayload()
	prog := m.program
	safego.Go("tui.statusline", func() {
		text, ok := runStatuslineCommand(command, payload, timeout)
		if prog != nil {
			prog.Send(statuslineMsg{seq: seq, text: text, ok: ok})
		}
	})
}

// handleStatuslineMsg stores a finished refresh result and requeues one
// follow-up if refreshes were requested while this one ran.
func (m Model) handleStatuslineMsg(msg statuslineMsg) (Model, tea.Cmd) {
	if m.statusline == nil {
		return m, nil
	}
	sl := m.statusline
	requeue := false
	sl.mu.Lock()
	if msg.seq >= sl.seq {
		// A failed or timed-out invocation keeps the last good output -
		// only a successful refresh may replace the cached text (#2748).
		if msg.ok {
			sl.text = msg.text
		}
		sl.running = false
		if sl.dirty {
			sl.dirty = false
			requeue = true
		}
	}
	sl.mu.Unlock()
	if requeue {
		// m is addressable here; refreshStatusline only needs the pointer to
		// lazily init m.statusline, which is already non-nil.
		(&m).refreshStatusline()
	}
	return m, nil
}

// statuslineBar renders the cached external status line, if any. Read-only
// and nil-safe so the render path never blocks or mutates.
func (m Model) statuslineBar() string {
	if m.config == nil || !m.config.StatusLine.Enabled() || m.statusline == nil {
		return ""
	}
	sl := m.statusline
	sl.mu.Lock()
	text := sl.text
	sl.mu.Unlock()
	if text == "" {
		return ""
	}
	maxWidth := m.viewWidth()
	if maxWidth > 2 {
		maxWidth -= 2
	}
	if lipgloss.Width(text) > maxWidth {
		text = statuslineTruncate(text, maxWidth)
	}
	return lipgloss.NewStyle().Foreground(mutedTextColor).Render(" " + text)
}

// statuslineTruncate cuts s to max rendered columns, appending an ellipsis.
func statuslineTruncate(s string, max int) string {
	runes := []rune(s)
	for i := len(runes); i > 0; i-- {
		if lipgloss.Width(string(runes[:i])) <= max-1 {
			return string(runes[:i]) + "…"
		}
	}
	return ""
}
