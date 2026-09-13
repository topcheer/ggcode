package tui

import (
	"os"
	"strings"
	"testing"
)

// #1731: ghost hint keys + unwired i18n catalog on the mattermost/pc panels.

func TestIssue1731MattermostHintNoGhostQR(t *testing.T) {
	// the panel has NO q case and no QR state - the hint must not advertise one
	var m Model
	en := m.t("panel.mattermost.actions_hint")
	if strings.Contains(en, "QR") {
		t.Fatalf("en hint must not advertise a nonexistent q QR key: %s", en)
	}
	mZh := Model{language: LangZhCN}
	zh := mZh.t("panel.mattermost.actions_hint")
	if strings.Contains(zh, "二维码") {
		t.Fatalf("zh hint must not advertise a nonexistent q QR key: %s", zh)
	}
}

func TestIssue1731PCHintNoGhostGroupKey(t *testing.T) {
	// the idle-mode hint literal lives in pc_panel.go's render; g only
	// works inside create mode, so the idle hint must not list g:group.
	// Assert via the source file to keep this a ghost-key regression pin
	// even if the hint later gains i18n keys.
	src, err := os.ReadFile("pc_panel.go")
	if err != nil {
		t.Skipf("source layout changed: %v", err)
	}
	for _, line := range strings.Split(string(src), "\n") {
		if strings.Contains(line, "n:new") && strings.Contains(line, "g:group") {
			t.Fatalf("idle hint still advertises g:group: %s", line)
		}
	}
}

func TestIssue1731PCStatusWired(t *testing.T) {
	// no adapter: must resolve the localized starting key, not leak the
	// key itself or fall back to hardcoded English
	m := newTestModel()
	got := m.pcConnectionStatus()
	if got == "" {
		t.Fatal("status must not be empty")
	}
	if strings.Contains(got, "panel.pc.status") {
		t.Fatalf("unresolved catalog key leaked into status: %s", got)
	}
	mZh := newTestModel()
	mZh.language = LangZhCN
	zh := mZh.pcConnectionStatus()
	if zh == "starting..." || zh == got {
		t.Fatalf("zh user must get the localized status, got %q (en %q)", zh, got)
	}
}
