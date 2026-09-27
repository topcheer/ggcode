package tui

// #2796: generalize the #2337 source-level guard to EVERY IM panel. The
// wecom fix's scan test only covered wecom_panel.go, so the same live-map
// len read (`len(m.config.IM.Adapters)`, bypassing imAdaptersMu under the
// #2152 accessor contract) regressed unnoticed in 14 sibling panels - a
// render frame overlapping any locked write is an unrecoverable Go
// runtime fatal. Every panel's BindingEntries must snapshot once and
// reuse the snapshot for both the capacity hint and the iteration.

import (
	"os"
	"strings"
	"testing"
)

var issue2796Panels = []string{
	"discord_panel.go", "dingtalk_panel.go", "matrix_panel.go",
	"whatsapp_panel.go", "irc_panel.go", "feishu_panel.go",
	"slack_panel.go", "signal_panel.go", "tg_panel.go",
	"mattermost_panel.go", "nostr_panel.go", "twitch_panel.go",
	"wechat_panel.go", "qq_panel.go", "wecom_panel.go",
}

func TestIssue2796_AllPanelsUseSnapshotLen(t *testing.T) {
	for _, f := range issue2796Panels {
		b, err := os.ReadFile(f)
		if err != nil {
			// The test lives in internal/tui; run go test from the package dir.
			b, err = os.ReadFile("internal/tui/" + f)
			if err != nil {
				t.Fatalf("#2796: cannot read panel source %s: %v", f, err)
			}
		}
		src := string(b)
		if strings.Contains(src, "len(m.config.IM.Adapters)") {
			t.Errorf("#2796: %s still reads the live adapter map for len() - a locked write overlapping this render line is a runtime fatal", f)
		}
	}
}

func TestIssue2796_AllPanelsSnapshotOnce(t *testing.T) {
	for _, f := range issue2796Panels {
		b, err := os.ReadFile(f)
		if err != nil {
			b, err = os.ReadFile("internal/tui/" + f)
			if err != nil {
				t.Fatalf("#2796: cannot read panel source %s: %v", f, err)
			}
		}
		if !strings.Contains(string(b), "snapAdapters := m.config.IMSnapshot().Adapters") {
			t.Errorf("#2796: %s must snapshot the adapter map once (snapAdapters pattern, #2337)", f)
		}
	}
}
