package im

import (
	"reflect"
	"testing"

	toolpkg "github.com/topcheer/ggcode/internal/tool"
)

func seamQuestion(kind string, id string, allowFreeform bool, labels ...string) toolpkg.AskUserQuestion {
	q := toolpkg.AskUserQuestion{ID: id, Kind: kind, AllowFreeform: allowFreeform}
	for i, l := range labels {
		q.Choices = append(q.Choices, toolpkg.AskUserChoice{ID: id + "-c" + string(rune('0'+i+1)), Label: l})
	}
	return q
}

func TestSingleQuestionReplyHintPins(t *testing.T) {
	cases := []struct {
		name string
		lang string
		kind string
		want string
	}{
		{"zh-text", "zh-CN", toolpkg.AskUserKindText, "💬 直接回复文本即可。"},
		{"zh-single", "zh-CN", toolpkg.AskUserKindSingle, "💬 回复编号或选项文本。"},
		{"zh-multi", "zh-CN", toolpkg.AskUserKindMulti, "💬 回复多个编号（用逗号或空格分隔）或选项文本。"},
		{"en-text", "en", toolpkg.AskUserKindText, "💬 Just reply with your text."},
		{"en-single", "en", toolpkg.AskUserKindSingle, "💬 Reply with the number or option text."},
		{"en-multi", "en", toolpkg.AskUserKindMulti, "💬 Reply with multiple numbers (comma or space separated) or option text."},
		{"zh-unknown-kind", "zh-CN", "", ""},
		{"en-unknown-kind", "en", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := singleQuestionReplyHint(tc.lang, seamQuestion(tc.kind, "q", false))
			if got != tc.want {
				t.Fatalf("pin mismatch: got %q want %q", got, tc.want)
			}
		})
	}
}

func TestMultiQuestionReplyHeaderPins(t *testing.T) {
	cases := []struct {
		name  string
		lang  string
		count int
		want  string
	}{
		{"zh-two", "zh-CN", 2, "💬 **回复格式：**\n每行回答一个问题，或用空行分隔。例如：\n"},
		{"zh-three", "zh-CN", 3, "💬 **回复格式：**\n按顺序逐行回答，每行对应一个问题。例如：\n"},
		{"en-two", "en", 2, "💬 **Reply format:**\nAnswer one question per line, or separate with blank lines. Example:\n"},
		{"en-three", "en", 3, "💬 **Reply format:**\nAnswer in order, one per line. Example:\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := multiQuestionReplyHeader(tc.lang, tc.count); got != tc.want {
				t.Fatalf("pin mismatch: got %q want %q", got, tc.want)
			}
		})
	}
}

func TestMultiQuestionExampleLinePins(t *testing.T) {
	// Single/Multi example lines are language-independent.
	if got := multiQuestionExampleLine("zh-CN", 0, seamQuestion(toolpkg.AskUserKindSingle, "q", false, "a", "b")); got != "> 1\n" {
		t.Fatalf("single zh: got %q", got)
	}
	if got := multiQuestionExampleLine("en", 0, seamQuestion(toolpkg.AskUserKindSingle, "q", false, "a", "b")); got != "> 1\n" {
		t.Fatalf("single en: got %q", got)
	}
	if got := multiQuestionExampleLine("zh-CN", 0, seamQuestion(toolpkg.AskUserKindMulti, "q", false, "a", "b")); got != "> 1,2\n" {
		t.Fatalf("multi: got %q", got)
	}
	textCases := []struct {
		lang string
		i    int
		want string
	}{
		{"zh-CN", 0, "> 我的答案\n"},
		{"zh-CN", 1, "> 另一个回答\n"},
		{"zh-CN", 2, "> 第3个回答\n"},
		{"en", 0, "> my answer\n"},
		{"en", 1, "> another answer\n"},
		{"en", 2, "> answer 3\n"},
	}
	for _, tc := range textCases {
		got := multiQuestionExampleLine(tc.lang, tc.i, seamQuestion(toolpkg.AskUserKindText, "q", true))
		if got != tc.want {
			t.Fatalf("text %s i=%d: got %q want %q", tc.lang, tc.i, got, tc.want)
		}
	}
	if got := multiQuestionExampleLine("zh-CN", 0, seamQuestion("", "q", false)); got != "" {
		t.Fatalf("unknown kind: got %q", got)
	}
}

func TestMultiQuestionReplyInstructionsGolden(t *testing.T) {
	qs := []toolpkg.AskUserQuestion{
		seamQuestion(toolpkg.AskUserKindSingle, "q1", false, "a", "b"),
		seamQuestion(toolpkg.AskUserKindMulti, "q2", false, "a", "b"),
		seamQuestion(toolpkg.AskUserKindText, "q3", true),
	}
	want := "💬 **回复格式：**\n按顺序逐行回答，每行对应一个问题。例如：\n> 1\n> 1,2\n> 第3个回答"
	if got := multiQuestionReplyInstructions("zh-CN", qs); got != want {
		t.Fatalf("golden mismatch:\ngot:  %q\nwant: %q", got, want)
	}
}

func TestFormatReplyInstructionsDispatch(t *testing.T) {
	if got := formatReplyInstructions("en", nil, false); got != "" {
		t.Fatalf("empty: got %q", got)
	}
	single := []toolpkg.AskUserQuestion{seamQuestion(toolpkg.AskUserKindText, "q", true)}
	if got := formatReplyInstructions("en", single, false); got != "💬 Just reply with your text." {
		t.Fatalf("single dispatch: got %q", got)
	}
	multi := append(single, seamQuestion(toolpkg.AskUserKindText, "q2", true))
	got := formatReplyInstructions("en", multi, true)
	wantPrefix := "💬 **Reply format:**\n"
	if len(got) < len(wantPrefix) || got[:len(wantPrefix)] != wantPrefix {
		t.Fatalf("multi dispatch: got %q", got)
	}
}

func TestEnsureParsedAnswersPadAndClone(t *testing.T) {
	parsed := []ParsedQuestionAnswer{{QuestionIndex: 0, Freeform: "keep"}}
	out := ensureParsedAnswers(parsed, 3)
	if len(out) != 3 {
		t.Fatalf("len = %d, want 3", len(out))
	}
	if out[0].Freeform != "keep" {
		t.Fatalf("slot 0 lost: %+v", out[0])
	}
	if out[1].QuestionIndex != 1 || out[2].QuestionIndex != 2 {
		t.Fatalf("pad indices: %+v %+v", out[1], out[2])
	}
	if out[1].Selected != nil || out[1].Freeform != "" {
		t.Fatalf("pad slots must be empty: %+v", out[1])
	}
	// Caller-owned copy: mutating the result must not touch the input.
	out[0].Freeform = "changed"
	if parsed[0].Freeform != "keep" {
		t.Fatalf("input mutated: %+v", parsed[0])
	}
	// Equal length: still returns an independent clone.
	out2 := ensureParsedAnswers(parsed, 1)
	out2[0].Freeform = "mutated"
	if parsed[0].Freeform != "keep" {
		t.Fatalf("clone path mutated input: %+v", parsed[0])
	}
}

func TestMergeQuestionAnswerPins(t *testing.T) {
	// nil selection must not clobber an existing selection.
	dst := ParsedQuestionAnswer{Selected: map[string]struct{}{"old": {}}}
	mergeQuestionAnswer(&dst, seamQuestion(toolpkg.AskUserKindSingle, "q", false, "a", "b"), nil, "")
	if _, ok := dst.Selected["old"]; !ok {
		t.Fatalf("nil selection clobbered: %+v", dst)
	}
	// Non-nil selection replaces.
	sel := map[string]struct{}{"new": {}}
	mergeQuestionAnswer(&dst, seamQuestion(toolpkg.AskUserKindSingle, "q", false, "a", "b"), sel, "")
	if !reflect.DeepEqual(dst.Selected, sel) {
		t.Fatalf("selection not replaced: %+v", dst)
	}
	// Empty freeform on a strict single-choice question leaves freeform intact.
	dst = ParsedQuestionAnswer{Freeform: "keep"}
	mergeQuestionAnswer(&dst, seamQuestion(toolpkg.AskUserKindSingle, "q", false, "a", "b"), nil, "")
	if dst.Freeform != "keep" {
		t.Fatalf("freeform clobbered on strict single: %+v", dst)
	}
	// Empty freeform on a text question clears freeform.
	dst = ParsedQuestionAnswer{Freeform: "old"}
	mergeQuestionAnswer(&dst, seamQuestion(toolpkg.AskUserKindText, "q", false), nil, "")
	if dst.Freeform != "" {
		t.Fatalf("freeform not cleared on text kind: %+v", dst)
	}
	// Non-empty freeform overwrites even on a strict single-choice question.
	dst = ParsedQuestionAnswer{Freeform: "old"}
	mergeQuestionAnswer(&dst, seamQuestion(toolpkg.AskUserKindSingle, "q", false, "a", "b"), nil, "new")
	if dst.Freeform != "new" {
		t.Fatalf("freeform not overwritten: %+v", dst)
	}
	// Empty freeform with AllowFreeform assigns (clears the previous value).
	dst = ParsedQuestionAnswer{Freeform: "keep"}
	mergeQuestionAnswer(&dst, seamQuestion(toolpkg.AskUserKindSingle, "q", true, "a", "b"), nil, "")
	if dst.Freeform != "" {
		t.Fatalf("allowfreeform empty assign must clear: %+v", dst)
	}
}

func TestApplyMultiLineBatchPins(t *testing.T) {
	req := toolpkg.AskUserRequest{Questions: []toolpkg.AskUserQuestion{
		seamQuestion(toolpkg.AskUserKindSingle, "q1", false, "a", "b"),
		seamQuestion(toolpkg.AskUserKindSingle, "q2", false, "a", "b"),
		seamQuestion(toolpkg.AskUserKindText, "q3", true),
	}}
	preAnswered := map[string]struct{}{"q1-c1": {}}
	parsed := []ParsedQuestionAnswer{
		{QuestionIndex: 0, Selected: preAnswered},
		{QuestionIndex: 1},
		{QuestionIndex: 2},
	}

	// Condition not met: single line -> fallback.
	ok, _, err := applyMultiLineBatch(req, parsed, "2", 1, 2)
	if ok || err != nil {
		t.Fatalf("single line should fall back: ok=%v err=%v", ok, err)
	}
	// Condition not met: too many lines -> fallback.
	ok, _, err = applyMultiLineBatch(req, parsed, "1\n2\n3\n4", 1, 2)
	if ok || err != nil {
		t.Fatalf("too many lines should fall back: ok=%v err=%v", ok, err)
	}

	// Success: distributes "2" -> q2, "hello" -> q3; q1 untouched.
	ok, nextIdx, err := applyMultiLineBatch(req, parsed, "2\nhello", 1, 2)
	if !ok || err != nil {
		t.Fatalf("batch should apply: ok=%v err=%v", ok, err)
	}
	if nextIdx != -1 {
		t.Fatalf("nextIdx = %d, want -1", nextIdx)
	}
	if _, ok := parsed[1].Selected["q2-c2"]; !ok {
		t.Fatalf("q2 selection wrong: %+v", parsed[1])
	}
	if parsed[2].Freeform != "hello" {
		t.Fatalf("q3 freeform wrong: %+v", parsed[2])
	}
	if !reflect.DeepEqual(parsed[0].Selected, preAnswered) {
		t.Fatalf("q1 mutated: %+v", parsed[0])
	}

	// Parse error on the first line: applied==0 -> fallback.
	parsed2 := []ParsedQuestionAnswer{{QuestionIndex: 0}, {QuestionIndex: 1}, {QuestionIndex: 2}}
	ok, _, err = applyMultiLineBatch(req, parsed2, "99\nhello", 0, 3)
	if ok || err != nil {
		t.Fatalf("first-line error should fall back: ok=%v err=%v", ok, err)
	}
	if parsed2[2].Freeform != "" {
		t.Fatalf("slot 2 must stay untouched after error: %+v", parsed2[2])
	}
}

func TestApplyRemoteQuestionnaireAnswerPins(t *testing.T) {
	req := toolpkg.AskUserRequest{Questions: []toolpkg.AskUserQuestion{
		seamQuestion(toolpkg.AskUserKindSingle, "q1", false, "a", "b"),
		seamQuestion(toolpkg.AskUserKindText, "q2", true),
	}}

	// Empty answer.
	if _, _, _, err := ApplyRemoteQuestionnaireAnswer(req, nil, "   "); err == nil || err.Error() != "empty answer" {
		t.Fatalf("empty answer: err=%v", err)
	}

	// Multi-line batch success end to end.
	parsed, completed, nextIdx, err := ApplyRemoteQuestionnaireAnswer(req, nil, "2\nmy text")
	if err != nil || !completed || nextIdx != -1 {
		t.Fatalf("batch: completed=%v nextIdx=%d err=%v", completed, nextIdx, err)
	}
	if _, ok := parsed[0].Selected["q1-c2"]; !ok || parsed[1].Freeform != "my text" {
		t.Fatalf("batch slots: %+v %+v", parsed[0], parsed[1])
	}

	// All answered -> no active question error, completed=true.
	_, completed, nextIdx, err = ApplyRemoteQuestionnaireAnswer(req, parsed, "more")
	if err == nil || err.Error() != "no active question" {
		t.Fatalf("no active question: err=%v", err)
	}
	if !completed || nextIdx != -1 {
		t.Fatalf("no active question: completed=%v nextIdx=%d", completed, nextIdx)
	}

	// Single-line fallback with a parse error surfaces the error at the index.
	fresh := []ParsedQuestionAnswer{}
	_, completed, nextIdx, err = ApplyRemoteQuestionnaireAnswer(req, fresh, "99")
	if err == nil {
		t.Fatalf("out-of-range must error")
	}
	if completed || nextIdx != 0 {
		t.Fatalf("error path: completed=%v nextIdx=%d", completed, nextIdx)
	}
}
