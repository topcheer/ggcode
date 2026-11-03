package tui

import (
	"regexp"
	"strings"
	"testing"
)

func TestIssue4793_SkippedCronFiringLeavesVisibleTrace(t *testing.T) {
	m := newTestModel()
	m.loading = true

	before := 0
	if m.chatList != nil {
		before = m.chatList.Len()
	}

	next, cmd := m.Update(cronPromptMsg{Prompt: "sweep round", QueueIfBusy: false})
	m2 := next.(Model)

	if cmd != nil {
		t.Fatal("skip path must not start or queue a run")
	}
	if m2.chatList == nil || m2.chatList.Len() != before+1 {
		t.Fatalf("skip must append exactly one visible system line: before=%d after=%v", before, chatLen(m2))
	}

	if !strings.Contains(chatLastSystem(m2), "Cron firing skipped") && !strings.Contains(chatLastSystem(m2), "定时任务触发被跳过") {
		t.Fatalf("system line should explain the skip, got: %q", chatLastSystem(m2))
	}
}

func TestIssue4793_QueuePathUnchanged(t *testing.T) {
	m := newTestModel()
	m.loading = true

	next, cmd := m.Update(cronPromptMsg{Prompt: "queued job", QueueIfBusy: true})
	m2 := next.(Model)
	if cmd != nil {
		t.Fatal("queue path schedules no immediate cmd")
	}
	if m2.chatList == nil || m2.chatList.Len() == 0 {
		t.Fatal("queue path still writes its firing system line")
	}
	if strings.Contains(chatLastSystem(m2), "skipped") {
		t.Fatalf("queue path must not show the skip notice: %q", chatLastSystem(m2))
	}
}

func chatLen(m Model) int {
	if m.chatList == nil {
		return 0
	}
	return m.chatList.Len()
}

func chatLastSystem(m Model) string {
	if m.chatList == nil || m.chatList.Len() == 0 {
		return ""
	}

	it := m.chatList.ItemAt(m.chatList.Len() - 1)
	if it == nil {
		return ""
	}
	return ansiStrip(it.Render(200))
}

var ansiStripRe = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func ansiStrip(s string) string { return ansiStripRe.ReplaceAllString(s, "") }
