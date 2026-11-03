package notify

import (
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/safego"
)

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

			break
		}
		if int(duration.Seconds()) < cfg.EffectiveMinDuration() {
			return
		}
	case "all":

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

func OnInputNeeded(cfg config.NotificationConfig, summary string) {
	if cfg.EffectiveMode() == "off" {
		return
	}
	if cfg.ShouldBell() {
		fireBell()
	}
	if cfg.Desktop {
		title := "ggcode: input needed"
		body := summary
		if body == "" {
			body = "The agent is waiting for your response."
		}
		safego.Go("notify.desktop", func() {
			fireDesktop(title, body)
		})
	}
}

func fireBell() {

	fmt.Print("\x07")
}

func appleScriptEscape(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	return strings.ReplaceAll(s, `"`, `\"`)
}

func fireDesktop(title, body string) {
	var cmd *exec.Cmd

	switch runtime.GOOS {
	case "darwin":

		asTitle := appleScriptEscape(title)
		asBody := appleScriptEscape(body)
		script := fmt.Sprintf(`display notification "%s" with title "%s"`, asBody, asTitle)
		cmd = exec.Command("osascript", "-e", script)
	case "linux":

		cmd = exec.Command("notify-send", title, body)
	case "windows":

		psTitle := strings.ReplaceAll(title, "'", "''")
		psBody := strings.ReplaceAll(body, "'", "''")
		psScript := fmt.Sprintf(
			`[System.Reflection.Assembly]::LoadWithPartialName('System.Windows.Forms'); $n=New-Object System.Windows.Forms.NotifyIcon; $n.BalloonTipTitle='%s'; $n.BalloonTipText='%s'; $n.Visible=$true; $n.ShowBalloonTip(5000)`,
			psTitle, psBody,
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
