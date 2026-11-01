//go:build darwin

package extpane

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// iterm2Backend implements Backend using iTerm2 AppleScript.
// Each agent gets its own tab, never disturbing the main TUI layout.
type iterm2Backend struct {
	selfSessionID string // ID of the session where ggcode itself runs - never close this
}

// osascriptCommand is the command runOsa executes. Var so tests can stub
// the probe without ever spawning a real osascript (which on macOS would
// launch iTerm2 as a side effect of `tell application` - #4850).
var osascriptCommand = func(ctx context.Context, script string) (string, error) {
	cmd := exec.CommandContext(ctx, "osascript", "-e", script)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// iterm2Running reports whether the iTerm2 process is actually running,
// WITHOUT launching it. `tell application "iTerm2"` launches a non-running
// app (LaunchServices "starting stopped process") - that is how background
// daemon probes resurrected a terminal the user had deliberately quit
// (#4850). System Events' `exists process` never launches the target.
func iterm2Running(ctx context.Context) bool {
	probeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	out, err := osascriptCommand(probeCtx, `tell application "System Events" to (exists process "iTerm2")`)
	return err == nil && strings.TrimSpace(out) == "true"
}

func newITerm2Backend() *iterm2Backend {
	// #4850: probing a non-running iTerm2 with `tell application` would
	// relaunch it. Guard: if the process is not alive, this backend is
	// simply unavailable - a probe must never launch a GUI app.
	if !iterm2Running(context.Background()) {
		return nil
	}
	b := &iterm2Backend{}
	// Capture our own session ID at init so we never accidentally close ggcode's tab.
	// Best-effort: if this fails, selfSessionID stays empty and CloseTab won't have
	// the self-guard, but the Manager-level failed/maxPanes guards still apply.
	// #1369/#894: 3s timeout like the tmux sibling - this runs on the TUI
	// startup synchronous path (NewManager→newModel); a busy/restarting
	// iTerm2 or a pending Automation-permission dialog would otherwise
	// block model construction forever.
	probeCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := osascriptCommand(probeCtx, `tell application "iTerm2" to return id of current session of current window`)
	if err == nil {
		b.selfSessionID = strings.TrimSpace(out)
	}
	return b
}

func (i *iterm2Backend) Name() string { return "iterm2" }

// CreateTab creates a new iTerm2 tab running `tail -f`.
func (i *iterm2Backend) CreateTab(ctx context.Context, title, logfile string) (string, error) {
	// #4850: the create script starts with `activate` - if the user quit
	// iTerm2 after this backend was constructed, running the script would
	// relaunch it. Guard at the boundary: pane creation requires a live
	// terminal; a quit terminal means the pane permanently fails (Manager
	// failed-map handles no-retry), never a resurrection.
	if !iterm2Running(ctx) {
		return "", fmt.Errorf("iterm2: not running - refusing to relaunch a quit terminal (#4850)")
	}
	safeTitle := sanitizeAS(title)
	// #1369 side-fix: a log path containing ' (apostrophed parent dir or
	// teammate name) breaks the AppleScript literal and the panel dies,
	// which stacks with the permanent-failure path.
	// #1414-A: the original fix escaped ' as \' - correct for the
	// AppleScript layer but then the whole command string goes through
	// `write text` into a POSIX SHELL, where backslash inside single
	// quotes is literal: tail -f '...O\'Brien...' closed the quote at
	// the apostrophe and the trailing quote never closed - unexpected
	// EOF, tail never ran, the tab sat dead. Shell-correct escaping is
	// '\'' (close quote, escaped apostrophe, reopen).
	safeLog := strings.ReplaceAll(logfile, "\\", "\\\\")
	safeLog = strings.ReplaceAll(safeLog, "'", "'\\''")
	tailCmd := fmt.Sprintf("tail -f '%s'", safeLog)
	safeCmd := sanitizeAS(tailCmd)
	// Capture session ID via a variable instead of "current session of newTab"
	// which may not work reliably across iTerm2 versions.
	script := fmt.Sprintf(`
tell application "iTerm2"
    activate
    tell current window
        set newTab to (create tab with default profile)
        set s to current session of newTab
        tell s
            set name to "%s"
            write text "%s"
        end tell
        return (id of s) as text
    end tell
end tell
`, safeTitle, safeCmd)

	ctx2, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := runOsa(ctx2, script)
	if err != nil {
		return "", fmt.Errorf("iterm2 create tab: %w", err)
	}
	sessionID := strings.TrimSpace(out)
	if sessionID == "" {
		return "", fmt.Errorf("iterm2 create tab: empty session ID")
	}
	// Sanity: if the returned ID is our own session, something is very wrong.
	if sessionID == i.selfSessionID {
		return "", fmt.Errorf("iterm2: created tab returned self session ID - refusing")
	}
	return sessionID, nil
}

// CloseTab closes the session's tab. If tabID matches our own session, it's a no-op.
func (i *iterm2Backend) CloseTab(tabID string) error {
	// Critical guard: never close ggcode's own session.
	if tabID == "" || tabID == i.selfSessionID {
		return nil
	}
	safeID := sanitizeAS(tabID)
	script := fmt.Sprintf(`
tell application "iTerm2"
    set selfID to id of current session of current window
    repeat with w in windows
        repeat with t in tabs of w
            repeat with s in sessions of t
                set sid to (id of s) as text
                if sid is "%s" and sid is not (selfID as text) then
                    tell w to close t
                    return
                end if
            end repeat
        end repeat
    end repeat
end tell
`, safeID)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err := runOsa(ctx, script)
	return err
}

// SetTitle sets the session name.
func (i *iterm2Backend) SetTitle(tabID, title string) error {
	if tabID == "" || tabID == i.selfSessionID {
		return nil
	}
	safeID := sanitizeAS(tabID)
	safeTitle := sanitizeAS(title)
	script := fmt.Sprintf(`
tell application "iTerm2"
    repeat with w in windows
        repeat with t in tabs of w
            repeat with s in sessions of t
                if (id of s as text) is "%s" then
                    set name of s to "%s"
                    return
                end if
            end repeat
        end repeat
    end repeat
end tell
`, safeID, safeTitle)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err := runOsa(ctx, script)
	return err
}

func runOsa(ctx context.Context, script string) (string, error) {
	return osascriptCommand(ctx, script)
}

// sanitizeAS escapes a string for safe use inside AppleScript double-quoted strings.
func sanitizeAS(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == '\\':
			b.WriteString("\\\\")
		case r == '"':
			b.WriteString("\\\"")
		case r == '\t':
			b.WriteString("\\t")
		case r == '\n':
			b.WriteString("\\n")
		case r == '\r':
			b.WriteString("\\r")
		case r < 0x20:
			// strip control characters
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
