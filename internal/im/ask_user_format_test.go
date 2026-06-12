package im

import (
	"strings"
	"testing"

	toolpkg "github.com/topcheer/ggcode/internal/tool"
)

func TestFormatReplyInstructions_SingleEn(t *testing.T) {
	tests := []struct {
		kind string
		want string
	}{
		{toolpkg.AskUserKindText, "Just reply"},
		{toolpkg.AskUserKindSingle, "Reply with the number"},
		{toolpkg.AskUserKindMulti, "Reply with multiple numbers"},
	}
	for _, tt := range tests {
		got := formatReplyInstructions("en", []toolpkg.AskUserQuestion{{Kind: tt.kind}}, false)
		if !strings.Contains(got, tt.want) {
			t.Errorf("formatReplyInstructions(en,%v,single) = %q, want containing %q", tt.kind, got, tt.want)
		}
	}
}

func TestFormatReplyInstructions_SingleZh(t *testing.T) {
	tests := []struct {
		kind string
		want string
	}{
		{toolpkg.AskUserKindText, "直接回复文本"},
		{toolpkg.AskUserKindSingle, "回复编号"},
		{toolpkg.AskUserKindMulti, "回复多个编号"},
	}
	for _, tt := range tests {
		got := formatReplyInstructions("zh-CN", []toolpkg.AskUserQuestion{{Kind: tt.kind}}, false)
		if !strings.Contains(got, tt.want) {
			t.Errorf("formatReplyInstructions(zh-CN,%v,single) = %q, want containing %q", tt.kind, got, tt.want)
		}
	}
}

func TestFormatReplyInstructions_MultiEn(t *testing.T) {
	questions := []toolpkg.AskUserQuestion{
		{Kind: toolpkg.AskUserKindText, Prompt: "Name?"},
		{Kind: toolpkg.AskUserKindSingle, Prompt: "Color?"},
		{Kind: toolpkg.AskUserKindMulti, Prompt: "Languages?"},
	}
	got := formatReplyInstructions("en", questions, true)
	if !strings.Contains(got, "Reply format") {
		t.Errorf("expected 'Reply format', got %q", got)
	}
}

func TestFormatReplyInstructions_MultiZh(t *testing.T) {
	questions := []toolpkg.AskUserQuestion{
		{Kind: toolpkg.AskUserKindText, Prompt: "名字？"},
		{Kind: toolpkg.AskUserKindText, Prompt: "描述？"},
		{Kind: toolpkg.AskUserKindText, Prompt: "备注？"},
	}
	got := formatReplyInstructions("zh-CN", questions, true)
	if !strings.Contains(got, "回复格式") {
		t.Errorf("expected '回复格式', got %q", got)
	}
}

func TestFormatReplyInstructions_Empty(t *testing.T) {
	got := formatReplyInstructions("en", nil, false)
	if got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

func TestFormatQuestionBlock_SingleText(t *testing.T) {
	q := toolpkg.AskUserQuestion{
		Prompt:      "What is your name?",
		Kind:        toolpkg.AskUserKindText,
		Placeholder: "John",
	}
	lines := formatQuestionBlock("en", 0, q, false)
	if len(lines) == 0 {
		t.Fatal("expected non-empty lines")
	}
	if !strings.Contains(lines[0], "What is your name?") {
		t.Errorf("expected prompt in first line, got %q", lines[0])
	}
}

func TestFormatQuestionBlock_SingleTextZh(t *testing.T) {
	q := toolpkg.AskUserQuestion{
		Prompt: "名字？",
		Kind:   toolpkg.AskUserKindText,
	}
	lines := formatQuestionBlock("zh-CN", 0, q, false)
	if len(lines) == 0 {
		t.Fatal("expected non-empty lines")
	}
}

func TestFormatQuestionBlock_SingleChoiceWithFreeform(t *testing.T) {
	q := toolpkg.AskUserQuestion{
		Prompt: "Pick a color",
		Kind:   toolpkg.AskUserKindSingle,
		Choices: []toolpkg.AskUserChoice{
			{Label: "Red"},
			{Label: "Blue"},
		},
		AllowFreeform: true,
	}
	lines := formatQuestionBlock("en", 0, q, false)
	if len(lines) < 3 {
		t.Fatalf("expected at least 3 lines, got %d", len(lines))
	}
}

func TestFormatQuestionBlock_MultiChoice(t *testing.T) {
	q := toolpkg.AskUserQuestion{
		Prompt: "Pick languages",
		Kind:   toolpkg.AskUserKindMulti,
		Choices: []toolpkg.AskUserChoice{
			{Label: "Go"},
			{Label: "Python"},
			{Label: "Rust"},
		},
	}
	lines := formatQuestionBlock("en", 0, q, false)
	if len(lines) < 3 {
		t.Fatalf("expected at least 3 lines, got %d", len(lines))
	}
}

func TestFormatQuestionBlock_MultiQuestion(t *testing.T) {
	q := toolpkg.AskUserQuestion{
		Prompt: "Name?",
		Kind:   toolpkg.AskUserKindText,
	}
	lines := formatQuestionBlock("en", 0, q, true)
	if len(lines) == 0 {
		t.Fatal("expected non-empty lines")
	}
	if !strings.Contains(lines[0], "1.") {
		t.Errorf("expected numbered header, got %q", lines[0])
	}
}

func TestFormatQuestionBlock_EmptyPrompt(t *testing.T) {
	q := toolpkg.AskUserQuestion{Prompt: "", Title: ""}
	lines := formatQuestionBlock("en", 0, q, false)
	if lines != nil {
		t.Errorf("expected nil for empty prompt, got %v", lines)
	}
}

func TestFormatQuestionBlock_MultiChoiceZhFreeform(t *testing.T) {
	q := toolpkg.AskUserQuestion{
		Prompt: "选择语言",
		Kind:   toolpkg.AskUserKindMulti,
		Choices: []toolpkg.AskUserChoice{
			{Label: "Go"},
			{Label: "Python"},
		},
		AllowFreeform: true,
	}
	lines := formatQuestionBlock("zh-CN", 0, q, false)
	if len(lines) == 0 {
		t.Fatal("expected non-empty lines")
	}
}

func TestFormatAskUserPrompt_Multi(t *testing.T) {
	req := toolpkg.AskUserRequest{
		Title: "Test Title",
		Questions: []toolpkg.AskUserQuestion{
			{Prompt: "Name?", Kind: toolpkg.AskUserKindText},
			{Prompt: "Age?", Kind: toolpkg.AskUserKindSingle, Choices: []toolpkg.AskUserChoice{{Label: "20-30"}, {Label: "30-40"}}},
		},
	}
	got := FormatAskUserPrompt("en", req)
	if got == "" {
		t.Error("expected non-empty")
	}
	if !strings.Contains(got, "Test Title") {
		t.Error("expected title in output")
	}
}

func TestFormatAskUserPrompt_EmptyQuestions(t *testing.T) {
	req := toolpkg.AskUserRequest{Title: "Title"}
	got := FormatAskUserPrompt("en", req)
	// May return empty or minimal output for empty questions
	_ = got
}
