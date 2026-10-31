//go:build goolm

package tui

import (
	"os"
	"strings"
	"testing"
)

// #4486 pin, part 1: feishu_panel must complete the #1794/#1719 shape - the
// auto-enable routes through configMutationMsg via a sentinel instead of
// writing the config map on the Cmd goroutine (same shape as #4483 pinned
// for discord). The old branch also overrode a user's just-pressed 'd'
// disable (TOCTOU: apply enables on the Update loop, next() re-reads
// disabled, then wrote enabled=true back from the Cmd goroutine).
func TestIssue4486_FeishuEnableRoutesThroughUpdateLoop(t *testing.T) {
	src, err := os.ReadFile("feishu_panel.go")
	if err != nil {
		t.Skip("source not readable in this environment")
	}
	s := string(src)
	if !strings.Contains(s, "errFeishuEnableNeeded") {
		t.Fatal("errFeishuEnableNeeded sentinel missing")
	}
	if !strings.Contains(s, "return errFeishuEnableNeeded") {
		t.Fatal("startFeishuAdapterIfNeeded must return the sentinel instead of writing config")
	}
	if !strings.Contains(s, "errors.Is(err, errFeishuEnableNeeded)") {
		t.Fatal("next() lacks the enable-needed retry branch")
	}
	if !strings.Contains(s, "func (m *Model) feishuEnableMutation(name string") {
		t.Fatal("feishuEnableMutation missing")
	}
	startIdx := strings.Index(s, "func (m *Model) startFeishuAdapterIfNeeded")
	endIdx := strings.Index(s[startIdx:], "func ")
	body := s[startIdx : startIdx+endIdx]
	if strings.Contains(body, "SetIMAdapterEnabled") {
		t.Fatal("startFeishuAdapterIfNeeded still writes config on the Cmd goroutine")
	}
}

// #4486 pin, part 2: every adapter-create flow that forces IM.Enabled=true
// must capture the previous value and restore it in its rollback before
// saveConfig - a failed create (wrong secret) must not silently persist
// global IM enabled=true on disk. whatsapp_panel is excluded by design: it
// has no rollback chain (the adapter is intentionally kept persisted for
// the QR-scan flow), so there is nothing to restore.
func TestIssue4486_CreateRollbackRestoresIMEnabled(t *testing.T) {
	panels := []string{
		"dingtalk_panel.go", "discord_panel.go", "feishu_panel.go",
		"irc_panel.go", "matrix_panel.go", "mattermost_panel.go",
		"nostr_panel.go", "qq_panel.go", "signal_panel.go",
		"slack_panel.go", "tg_panel.go", "twitch_panel.go",
		"wecom_panel.go",
	}
	for _, f := range panels {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Skipf("source %s not readable in this environment", f)
		}
		s := string(src)
		if !strings.Contains(s, "imEnabledBefore = m.config.IM.Enabled") {
			t.Errorf("%s: create-flow apply does not capture the pre-create IM.Enabled value", f)
		}
		if !strings.Contains(s, "m.config.IM.Enabled = imEnabledBefore") {
			t.Errorf("%s: rollback does not restore IM.Enabled before saveConfig", f)
		}
	}
}
