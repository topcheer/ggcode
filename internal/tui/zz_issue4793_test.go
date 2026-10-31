package tui

import (
	"regexp"
	"strings"
	"testing"
)

// #4793 (S-23): a cron firing eaten by a busy window (queue_if_busy=false)
// used to vanish with only a debug log - a one-shot sweep cron left no
// user-visible trace. The skip must now record a visible system line.
func TestIssue4793_SkippedCronFiringLeavesVisibleTrace(t *testing.T) {
	m := newTestModel()
	m.loading = true // agent busy

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
	// The line must carry the skip key's text (timestamp suffix varies).
	if !strings.Contains(chatLastSystem(m2), "Cron firing skipped") && !strings.Contains(chatLastSystem(m2), "定时任务触发被跳过") {
		t.Fatalf("system line should explain the skip, got: %q", chatLastSystem(m2))
	}
}

// The queue path (queue_if_busy=true) keeps its own firing line and the idle
// path its own - unchanged by #4793.
func TestIssue4793_QueuePathUnchanged(t *testing.T) {
	m := newTestModel()
	m.loading = true // busy

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
	// chatWriteSystem appends; ItemAt reads back the last written item.
	it := m.chatList.ItemAt(m.chatList.Len() - 1)
	if it == nil {
		return ""
	}
	return ansiStrip(it.Render(200))
}

var ansiStripRe = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func ansiStrip(s string) string { return ansiStripRe.ReplaceAllString(s, "") }
