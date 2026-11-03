package tui

import (
	"os"
	"strings"
	"testing"
)

func TestIssue1731MattermostHintNoGhostQR(t *testing.T) {

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
