package im

import (
	"testing"

	toolpkg "github.com/topcheer/ggcode/internal/tool"
)

func TestSplitNonEmptyLines(t *testing.T) {
	tests := []struct {
		input string
		want  int
	}{
		{"line1\nline2\nline3", 3},
		{"line1\n\nline2\n  \nline3", 3},
		{"", 0},
		{"\n\n\n", 0},
		{"  single  ", 1},
	}
	for _, tt := range tests {
		got := SplitNonEmptyLines(tt.input)
		if len(got) != tt.want {
			t.Errorf("SplitNonEmptyLines(%q) = %d lines, want %d", tt.input, len(got), tt.want)
		}
	}
}

func TestNormalizeRemoteAnswerToken(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"  Hello World  ", "helloworld"},
		{"YES", "yes"},
		{"  Choice A ", "choicea"},
		{"tab\there", "tabhere"},
		{"", ""},
	}
	for _, tt := range tests {
		got := normalizeRemoteAnswerToken(tt.input)
		if got != tt.expected {
			t.Errorf("normalizeRemoteAnswerToken(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestSortedKeys(t *testing.T) {
	m := map[string]struct{}{
		"c": {},
		"a": {},
		"b": {},
	}
	keys := sortedKeys(m)
	if len(keys) != 3 {
		t.Fatalf("expected 3 keys, got %d", len(keys))
	}
}

func TestSortedKeys_Nil(t *testing.T) {
	keys := sortedKeys(nil)
	if keys != nil {
		t.Errorf("expected nil for nil map, got %v", keys)
	}
}

func TestLabelsForIDs(t *testing.T) {
	choices := []toolpkg.AskUserChoice{
		{ID: "a", Label: "Choice A"},
		{ID: "b", Label: "Choice B"},
		{ID: "c", Label: "Choice C"},
	}
	selected := map[string]struct{}{"a": {}, "c": {}}
	labels := labelsForIDs(choices, selected)
	if len(labels) != 2 {
		t.Fatalf("expected 2 labels, got %d", len(labels))
	}
}

func TestLabelsForIDs_Empty(t *testing.T) {
	labels := labelsForIDs(nil, nil)
	if labels != nil {
		t.Errorf("expected nil, got %v", labels)
	}
}

func TestParseMultiQuestionReply(t *testing.T) {
	questions := []toolpkg.AskUserQuestion{
		{ID: "q1", Title: "Q1", Kind: "single", Choices: []toolpkg.AskUserChoice{
			{ID: "a", Label: "A"},
			{ID: "b", Label: "B"},
		}},
		{ID: "q2", Title: "Q2", Kind: "text"},
	}

	results := ParseMultiQuestionReply("A\nsome text answer", questions)
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results[0].QuestionIndex != 0 {
		t.Errorf("expected index 0, got %d", results[0].QuestionIndex)
	}
	if results[1].Freeform != "some text answer" {
		t.Errorf("expected 'some text answer', got %q", results[1].Freeform)
	}
}

func TestParseMultiQuestionReply_FewerLines(t *testing.T) {
	questions := []toolpkg.AskUserQuestion{
		{ID: "q1", Title: "Q1", Kind: "text"},
		{ID: "q2", Title: "Q2", Kind: "text"},
	}

	results := ParseMultiQuestionReply("only one line", questions)
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results[0].Freeform != "only one line" {
		t.Errorf("expected 'only one line', got %q", results[0].Freeform)
	}
	if results[1].Freeform != "" {
		t.Errorf("expected empty freeform for unanswered, got %q", results[1].Freeform)
	}
}

func TestFormatQuestionBlock(t *testing.T) {
	q := toolpkg.AskUserQuestion{
		ID:    "test-q",
		Title: "Test Question",
		Kind:  "single",
		Choices: []toolpkg.AskUserChoice{
			{ID: "opt1", Label: "Option 1"},
			{ID: "opt2", Label: "Option 2"},
		},
	}
	got := formatQuestionBlock("en", 1, q, false)
	if len(got) == 0 {
		t.Error("expected non-empty formatted question")
	}
}

func TestFormatQuestionBlock_Text(t *testing.T) {
	q := toolpkg.AskUserQuestion{
		ID:    "text-q",
		Title: "Free text",
		Kind:  "text",
	}
	got := formatQuestionBlock("en", 0, q, false)
	if len(got) == 0 {
		t.Error("expected non-empty formatted text question")
	}
}
