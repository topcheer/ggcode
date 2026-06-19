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
type iterm2Backend struct{}

func newITerm2Backend() *iterm2Backend {
	return &iterm2Backend{}
}

func (i *iterm2Backend) Name() string { return "iterm2" }

// CreateTab creates a new iTerm2 tab running `tail -f`.
func (i *iterm2Backend) CreateTab(ctx context.Context, title, logfile string) (string, error) {
	// Sanitize inputs for AppleScript context.
	safeTitle := sanitizeAS(title)
	// logfile comes from os.MkdirTemp + sanitizeFilename — safe chars only.
	// Use single quotes in the shell command to handle any path.
	tailCmd := fmt.Sprintf("tail -f '%s'", logfile)
	safeCmd := sanitizeAS(tailCmd)
	script := fmt.Sprintf(`
tell application "iTerm2"
    tell current window
        set newTab to (create tab with default profile)
        tell current session of newTab
            set name to "%s"
            write text "%s"
            return id of current session
        end tell
    end tell
end tell
`, safeTitle, safeCmd)

	ctx2, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := runOsa(ctx2, script)
	if err != nil {
		return "", fmt.Errorf("iterm2 create tab: %w", err)
	}
	sessionID := strings.TrimSpace(out)
	if sessionID == "" {
		return "", fmt.Errorf("iterm2 create tab: empty session ID")
	}
	return sessionID, nil
}

// CloseTab closes the session's tab.
func (i *iterm2Backend) CloseTab(tabID string) error {
	// tabID is a sanitized session ID (alphanumeric).
	script := fmt.Sprintf(`
tell application "iTerm2"
    repeat with w in windows
        repeat with t in tabs of w
            repeat with s in sessions of t
                if (id of s) is "%s" then
                    tell w to close t
                    return
                end if
            end repeat
        end repeat
    end repeat
end tell
`, sanitizeAS(tabID))

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err := runOsa(ctx, script)
	return err
}

// SetTitle sets the session name.
func (i *iterm2Backend) SetTitle(tabID, title string) error {
	script := fmt.Sprintf(`
tell application "iTerm2"
    repeat with w in windows
        repeat with t in tabs of w
            repeat with s in sessions of t
                if (id of s) is "%s" then
                    set name of s to "%s"
                    return
                end if
            end repeat
        end repeat
    end repeat
end tell
`, sanitizeAS(tabID), sanitizeAS(title))

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
