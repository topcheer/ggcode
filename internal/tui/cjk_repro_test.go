package tui

import (
	"strings"
	"testing"
)

// TestComposerCursorEndTailVisible is the regression test for the
// "Chinese chars disappear from the input box but appear when sent" bug.
//
// Root cause: composerCursorEnd used to re-SetValue the value, but the
// textarea's internal repositionView clamps its scroll against the viewport's
// PREVIOUS content, so long values restored via SetValue (pending-draft
// restore, history recall) rendered with the tail clipped below the
// MaxHeight viewport. The full value was still sent on submit — text
// "vanished" from the composer only.
//
// CJK text triggers this early: bubbles' soft-wrap only fits ~7 full-width
// runes per line at narrow widths, so MaxHeight=10 is exhausted by ~70 chars.
func TestComposerCursorEndTailVisible(t *testing.T) {
	// 80 CJK chars -> 11 wrapped lines at width 20 (7 chars/line).
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
	// The LAST rune of the value must be on-screen after composerCursorEnd.
	if want := "好"; !strings.HasSuffix(strings.TrimRight(all, " "), want) {
		t.Errorf("tail of long CJK value not visible after composerCursorEnd: got %d runes, last line tail = %q",
			len([]rune(all)), string([]rune(all)[min(10, len([]rune(all))):]))
	}
	// Cursor must sit at the end of the value.
	if col, row := ta.Column(), ta.Line(); row != ta.LineCount()-1 {
		t.Errorf("cursor not on last line: row=%d of %d, col=%d", row, ta.LineCount(), col)
	}
}

// TestComposerCursorEndShortText guards the no-scroll path: short values
// (single line, ASCII, empty) must remain fully visible with cursor at end.
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

// TestComposerWrapCapacity documents the bubbles wrap() behavior: at width 20
// the effective wrap width is 14 cells (prompt 2 + base padding 4? measured),
// fitting 7 full-width runes per line. This is why MaxHeight=10 clips at
// ~70 CJK chars even though 9 chars/line would fit 90.
func TestComposerWrapCapacity(t *testing.T) {
	ta := mkTA(20)
	ta.SetValue(string([]rune(strings.Repeat("你", 100))[:70]))
	plain := stripANSIForTest(ta.View())
	lines := strings.Split(plain, "\n")
	// With 7/line, 70 chars = exactly 10 lines; the MaxHeight viewport shows
	// all of them (no clipping at exactly the boundary).
	counted := 0
	for _, l := range lines {
		l = strings.TrimPrefix(strings.TrimRight(l, " "), "❯ ")
		counted += len([]rune(l))
	}
	if counted != 70 {
		t.Errorf("boundary case: rendered %d/70 runes", counted)
	}
}
