//go:build goolm

package tui

import (
	"os"
	"strings"
	"testing"
)

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
