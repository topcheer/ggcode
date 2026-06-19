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
	selfSessionID string // ID of the session where ggcode itself runs — never close this
}

func newITerm2Backend() *iterm2Backend {
	b := &iterm2Backend{}
	// Capture our own session ID at init so we never accidentally close ggcode's tab.
	// Best-effort: if this fails, selfSessionID stays empty and CloseTab won't have
	// the self-guard, but the Manager-level failed/maxPanes guards still apply.
	out, err := runOsa(context.Background(), `tell application "iTerm2" to return id of current session of current window`)
	if err == nil {
		b.selfSessionID = strings.TrimSpace(out)
	}
	return b
}

func (i *iterm2Backend) Name() string { return "iterm2" }

// CreateTab creates a new iTerm2 tab running `tail -f`.
func (i *iterm2Backend) CreateTab(ctx context.Context, title, logfile string) (string, error) {
	safeTitle := sanitizeAS(title)
	tailCmd := fmt.Sprintf("tail -f '%s'", logfile)
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
		return "", fmt.Errorf("iterm2: created tab returned self session ID — refusing")
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
	cmd := exec.CommandContext(ctx, "osascript", "-e", script)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
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
