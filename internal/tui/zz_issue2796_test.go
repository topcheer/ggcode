package tui

import (
	"os"
	"strings"
	"testing"
)

var issue2796IMPanelFiles = []string{
	"dingtalk_panel.go",
	"discord_panel.go",
	"feishu_panel.go",
	"irc_panel.go",
	"matrix_panel.go",
	"mattermost_panel.go",
	"nostr_panel.go",
	"qq_panel.go",
	"signal_panel.go",
	"slack_panel.go",
	"tg_panel.go",
	"twitch_panel.go",
	"wechat_panel.go",
	"whatsapp_panel.go",
	"wecom_panel.go",
}

func TestIssue2796AllIMPanelsUseSnapshotLen(t *testing.T) {
	for _, f := range issue2796IMPanelFiles {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		src := string(b)
		if strings.Contains(src, "len(m.config.IM.Adapters)") {
			t.Errorf("%s: live-map len(m.config.IM.Adapters) read bypasses imAdaptersMu (#2337/#2796); use the IMSnapshot copy for len()", f)
		}
	}
}
