// Package notify provides cross-platform agent completion notifications.
//
// Research basis: Anthropic's 2026 Agentic Coding Trends Report identifies
// "scaling human-agent oversight" as a top trend — developers manage multiple
// concurrent agent sessions and need reliable alerts when long-running tasks
// finish. Claude Code, Cursor, and Windsurf all provide configurable
// completion notifications. This package centralizes notification logic so
// the TUI can fire bell and/or desktop notifications from any completion
// handler, using the user's configured preferences.
package notify

import (
	"fmt"
	"os/exec"
	"runtime"
	"time"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/safego"
)

// OnCompletion fires the appropriate notification based on the user's config
// and the run characteristics. It is non-blocking and safe to call from the TUI
// update goroutine.
//
// Parameters:
//   - cfg: the user's notification preferences
//   - duration: how long the agent run took
//   - failed: whether the run ended in error
//   - summary: a short human-readable description for the desktop notification
func OnCompletion(cfg config.NotificationConfig, duration time.Duration, failed bool, summary string) {
	mode := cfg.EffectiveMode()

	switch mode {
	case "off":
		return
	case "errors":
		if !failed {
			return
		}
	case "long":
		if failed {
			// errors always notify in long mode
			break
		}
		if int(duration.Seconds()) < cfg.EffectiveMinDuration() {
			return
		}
	case "all":
		// always notify
	}

	if cfg.ShouldBell() {
		fireBell()
	}
	if cfg.Desktop {
		title := "ggcode: task complete"
		body := summary
		if failed {
			title = "ggcode: task failed"
			if body == "" {
				body = "The agent run ended with an error."
			}
		}
		safego.Go("notify.desktop", func() {
			fireDesktop(title, body)
		})
	}
}

// fireBell writes the terminal bell character to stdout. This is the same
// behavior as the previous hardcoded bell, extracted into a reusable function.
func fireBell() {
	// Use fmt.Print with \x07 (BEL). Terminals that don't support it silently
	// ignore the character. This is the standard cross-platform approach.
	fmt.Print("\x07")
}

// fireDesktop sends an OS-native desktop notification. It tries the platform
// appropriate command and logs a debug message on failure.
func fireDesktop(title, body string) {
	var cmd *exec.Cmd

	switch runtime.GOOS {
	case "darwin":
		// AppleScript display notification — no external dependencies.
		script := fmt.Sprintf(`display notification %q with title %q`, body, title)
		cmd = exec.Command("osascript", "-e", script)
	case "linux":
		// notify-send is available on most desktop Linux distributions.
		cmd = exec.Command("notify-send", title, body)
	case "windows":
		// PowerShell toast notification — available on Windows 10+.
		psScript := fmt.Sprintf(
			`[System.Reflection.Assembly]::LoadWithPartialName('System.Windows.Forms'); $n=New-Object System.Windows.Forms.NotifyIcon; $n.BalloonTipTitle='%s'; $n.BalloonTipText='%s'; $n.Visible=$true; $n.ShowBalloonTip(5000)`,
			title, body,
		)
		cmd = exec.Command("powershell", "-NoProfile", "-Command", psScript)
	default:
		debug.Log("notify", "desktop: unsupported OS %s", runtime.GOOS)
		return
	}

	if cmd == nil {
		return
	}

	if err := cmd.Run(); err != nil {
		debug.Log("notify", "desktop notification failed: %v", err)
	}
}
