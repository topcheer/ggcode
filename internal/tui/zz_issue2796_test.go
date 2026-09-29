package tui

// #2796: every IM binding panel must take its adapter key list from the
// IMSnapshot deep copy, never len(m.config.IM.Adapters). The live map read
// bypasses imAdaptersMu; overlapping with any locked writer (enable/disable
// toggle, AddIMAdapter) is an unrecoverable "concurrent map read and map
// write" fatal. #2337 fixed wecom_panel.go only; this generalizes the guard
// to all IM panel files so the family cannot regress one file at a time.

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
	"wecom_panel.go", // fixed in #2337; must stay fixed
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
