package tui

import (
	"os"
	"strings"
	"testing"
)

// #5273: the dormant-sentinel auto-pause reason must surface in the TUI
// /cron list and /cron get renders (regex-on-source anchor per package
// convention; the behavioral pins live in internal/tool).
func TestIssue5273CronRendersSurfacePausedReason(t *testing.T) {
	src, err := os.ReadFile("commands_cron.go")
	if err != nil {
		t.Skipf("source layout changed: %v", err)
	}
	text := string(src)

	// /cron list: paused status embeds the reason when present.
	if !strings.Contains(text, `status = fmt.Sprintf("paused (%s)", j.PausedReason)`) {
		t.Fatal("cron list render must surface PausedReason inside the paused status")
	}
	// /cron get: paused status embeds the reason, bare form preserved for manual pause.
	if !strings.Contains(text, `sb.WriteString(fmt.Sprintf("  Status: paused (%s)\n", job.PausedReason))`) {
		t.Fatal("cron get render must surface PausedReason in its status line")
	}
	if !strings.Contains(text, `sb.WriteString("  Status: paused\n")`) {
		t.Fatal("manual pause (no reason) must keep the bare paused status")
	}
}
