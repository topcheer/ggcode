package tui

import (
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/chat"
)

var stampRe = regexp.MustCompile(`\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}`)

func lastSystemText(t *testing.T, m *Model) string {
	t.Helper()
	if m.chatList == nil || m.chatList.Len() == 0 {
		t.Fatal("expected a system line in chatList")
	}
	item, ok := m.chatList.ItemAt(m.chatList.Len() - 1).(*chat.SystemItem)
	if !ok {
		t.Fatalf("expected last item to be a system line, got %T", m.chatList.ItemAt(m.chatList.Len()-1))
	}
	return item.Text()
}

// Every agent turn ends with a system line carrying the wall-clock time the
// loop finished, so someone returning later can see when work stopped.
func TestRunFinishedStampPerOutcome(t *testing.T) {
	fixed := time.Date(2026, 11, 24, 9, 8, 7, 0, time.Local)
	cases := []struct {
		name             string
		canceled, failed bool
		lang             Language
		want             string
	}{
		{"finished", false, false, LangEnglish, "Finished at 2026-11-24 09:08:07"},
		{"canceled", true, false, LangEnglish, "Canceled at 2026-11-24 09:08:07"},
		{"failed", false, true, LangEnglish, "Failed at 2026-11-24 09:08:07"},
		{"finished-zh", false, false, LangZhCN, "完成于 2026-11-24 09:08:07"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestModel()
			m.chatList = chat.NewList(80, 20)
			m.language = tc.lang
			m.runEndedAt = fixed
			(&m).appendRunFinishedStamp(tc.canceled, tc.failed)
			got := lastSystemText(t, &m)
			if !strings.Contains(got, tc.want) {
				t.Fatalf("stamp = %q, want it to contain %q", got, tc.want)
			}
		})
	}
}

func TestRunFinishedStampUsesNowWhenNoRunEnd(t *testing.T) {
	m := newTestModel()
	m.chatList = chat.NewList(80, 20)
	before := time.Now().Add(-2 * time.Second)
	(&m).appendRunFinishedStamp(false, false)
	got := lastSystemText(t, &m)
	stamp := stampRe.FindString(got)
	if stamp == "" {
		t.Fatalf("stamp = %q, want a date-time", got)
	}
	parsed, err := time.ParseInLocation("2006-01-02 15:04:05", stamp, time.Local)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Before(before) {
		t.Fatalf("stamp %s predates the call (%s)", stamp, before)
	}
}

// End to end: the agentDone handler (the real end-of-turn path) leaves the
// stamp as the last chat item.
func TestAgentDoneAppendsFinishedStampLast(t *testing.T) {
	m := NewModel(nil, nil)
	m.loading = true
	m.activeAgentRunID = 1
	next, _ := m.handleAgentDoneMsg(agentDoneMsg{RunID: 1})
	got := lastSystemText(t, &next)
	if !strings.HasPrefix(got, "Finished at ") || stampRe.FindString(got) == "" {
		t.Fatalf("last chat line = %q, want the finished-at stamp", got)
	}
	if next.loading {
		t.Fatal("expected loading cleared")
	}
}

// Every locale that ships the key renders it with the timestamp in place.
func TestRunFinishedStampAllLocales(t *testing.T) {
	for _, lang := range []Language{LangEnglish, LangZhCN, LangZhTW, LangJa, LangKo, LangEs, LangFr, LangDe, LangRu, LangPt, LangVi} {
		m := newTestModel()
		m.chatList = chat.NewList(80, 20)
		m.language = lang
		m.runEndedAt = time.Date(2026, 11, 24, 9, 8, 7, 0, time.Local)
		(&m).appendRunFinishedStamp(false, false)
		got := lastSystemText(t, &m)
		if !strings.Contains(got, "2026-11-24 09:08:07") || strings.Contains(got, "%!") {
			t.Fatalf("lang %s: stamp = %q, want the timestamp rendered", lang, got)
		}
	}
}
