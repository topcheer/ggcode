package tui

import (
	"strings"
	"testing"
)

func TestComposerCursorEndTailVisible(t *testing.T) {

	text := string([]rune(strings.Repeat("你好世界测试", 100))[:80])

	ta := mkTA(20)
	ta.SetValue(text)
	composerCursorEnd(&ta)

	plain := stripANSIForTest(ta.View())
	all := ""
	for _, l := range strings.Split(plain, "\n") {
		l = strings.TrimRight(l, " ")
		l = strings.TrimPrefix(l, "❯ ")
		all += l
	}

	if want := "好"; !strings.HasSuffix(strings.TrimRight(all, " "), want) {
		t.Errorf("tail of long CJK value not visible after composerCursorEnd: got %d runes, last line tail = %q",
			len([]rune(all)), string([]rune(all)[min(10, len([]rune(all))):]))
	}

	if col, row := ta.Column(), ta.Line(); row != ta.LineCount()-1 {
		t.Errorf("cursor not on last line: row=%d of %d, col=%d", row, ta.LineCount(), col)
	}
}

func TestComposerCursorEndShortText(t *testing.T) {
	cases := []struct {
		name string
		text string
	}{
		{"empty", ""},
		{"ascii", "hello world"},
		{"cjk short", "你好"},
		{"cjk one line", "你好世界"},
		{"mixed", "hi 你好 ok"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ta := mkTA(40)
			ta.SetValue(tc.text)
			composerCursorEnd(&ta)
			plain := stripANSIForTest(ta.View())
			runes := []rune(tc.text)
			if len(runes) == 0 {
				return
			}
			last := string(runes[len(runes)-1])
			if !strings.Contains(plain, last) {
				t.Errorf("last rune %q not visible in view", last)
			}
			if row := ta.Line(); row != ta.LineCount()-1 {
				t.Errorf("cursor not on last line: row=%d of %d", row, ta.LineCount())
			}
		})
	}
}

func TestComposerWrapCapacity(t *testing.T) {
	ta := mkTA(20)
	ta.SetValue(string([]rune(strings.Repeat("你", 100))[:70]))
	plain := stripANSIForTest(ta.View())
	lines := strings.Split(plain, "\n")

	counted := 0
	for _, l := range lines {
		l = strings.TrimPrefix(strings.TrimRight(l, " "), "❯ ")
		counted += len([]rune(l))
	}
	if counted != 70 {
		t.Errorf("boundary case: rendered %d/70 runes", counted)
	}
}
