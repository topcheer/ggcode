package tui

import (
	"testing"

	toolpkg "github.com/topcheer/ggcode/internal/tool"

	"github.com/topcheer/ggcode/internal/harness"
)

func newTestQS(questions ...toolpkg.AskUserQuestion) *questionnaireState {
	qs := &questionnaireState{
		tabIndex: 0,
		request:  toolpkg.AskUserRequest{Questions: questions},
		answers:  make([]questionnaireAnswerState, len(questions)),
	}
	for i := range qs.answers {
		qs.answers[i].selected = make(map[string]struct{})
	}
	return qs
}

// ── activity_groups.go ──────────────────────────────────────────────

func TestLocalizeTodoHeading(t *testing.T) {
	en := localizeTodoHeading(LangEnglish, "fix the bug")
	if en != "Todo: fix the bug" {
		t.Errorf("en = %q", en)
	}
	zh := localizeTodoHeading(LangZhCN, "修复bug")
	if zh != "任务: 修复bug" {
		t.Errorf("zh = %q", zh)
	}
}

func TestLocalizeTodoFocus(t *testing.T) {
	got := localizeTodoFocus(LangEnglish, "writing tests")
	if got != "Working on writing tests" {
		t.Errorf("unexpected: %q", got)
	}
	got2 := localizeTodoFocus(LangZhCN, "写测试")
	if got2 != "当前任务 写测试" {
		t.Errorf("unexpected: %q", got2)
	}
}

func TestIsSubAgentLifecycleTool(t *testing.T) {
	for _, tt := range []struct {
		tool string
		want bool
	}{
		{"spawn_agent", true},
		{"wait_agent", true},
		{"list_agents", true},
		{"read_file", false},
		{"", false},
	} {
		got := isSubAgentLifecycleTool(tt.tool)
		if got != tt.want {
			t.Errorf("isSubAgentLifecycleTool(%q) = %v, want %v", tt.tool, got, tt.want)
		}
	}
}

// ── commands_harness.go ─────────────────────────────────────────────

func TestLocalizeHarnessTaskStatus(t *testing.T) {
	statuses := []struct {
		status harness.TaskStatus
		en     string
		zh     string
	}{
		{harness.TaskQueued, "queued", "排队中"},
		{harness.TaskRunning, "running", "运行中"},
		{harness.TaskCompleted, "completed", "已完成"},
		{harness.TaskFailed, "failed", "失败"},
		{harness.TaskBlocked, "blocked", "阻塞"},
	}
	for _, tt := range statuses {
		en := localizeHarnessTaskStatus(LangEnglish, tt.status)
		if en != tt.en {
			t.Errorf("en %v = %q, want %q", tt.status, en, tt.en)
		}
		zh := localizeHarnessTaskStatus(LangZhCN, tt.status)
		if zh != tt.zh {
			t.Errorf("zh %v = %q, want %q", tt.status, zh, tt.zh)
		}
	}
}

func TestHumanizeHarnessProgress_Empty(t *testing.T) {
	got := humanizeHarnessProgress(LangEnglish, harness.Project{}, "")
	if got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

// ── ask_user.go ─────────────────────────────────────────────────────

func TestQuestionnaireState_MoveChoice(t *testing.T) {
	qs := newTestQS(toolpkg.AskUserQuestion{
		Choices: []toolpkg.AskUserChoice{
			{ID: "a", Label: "A"},
			{ID: "b", Label: "B"},
			{ID: "c", Label: "C"},
		},
	})

	qs.moveChoice(1)
	if qs.choiceCursor != 1 {
		t.Errorf("expected cursor=1, got %d", qs.choiceCursor)
	}
	qs.moveChoice(1)
	qs.moveChoice(1)
	if qs.choiceCursor != 0 {
		t.Errorf("expected cursor=0 (wrap), got %d", qs.choiceCursor)
	}
	qs.moveChoice(-1)
	if qs.choiceCursor != 2 {
		t.Errorf("expected cursor=2 (wrap up), got %d", qs.choiceCursor)
	}
}

func TestQuestionnaireState_MoveChoice_NoChoices(t *testing.T) {
	qs := newTestQS(toolpkg.AskUserQuestion{Prompt: "Text only?", Kind: toolpkg.AskUserKindText})
	qs.moveChoice(1)
}

func TestQuestionnaireState_ToggleCurrentChoice(t *testing.T) {
	qs := newTestQS(toolpkg.AskUserQuestion{
		Kind: toolpkg.AskUserKindSingle,
		Choices: []toolpkg.AskUserChoice{
			{ID: "a", Label: "A"},
			{ID: "b", Label: "B"},
		},
	})

	qs.toggleCurrentChoice()
	if _, ok := qs.answers[0].selected["a"]; !ok {
		t.Error("expected 'a' to be selected")
	}
	qs.toggleCurrentChoice()
	if _, ok := qs.answers[0].selected["a"]; ok {
		t.Error("expected 'a' to be deselected")
	}
}

func TestQuestionnaireState_ToggleMultiChoice(t *testing.T) {
	qs := newTestQS(toolpkg.AskUserQuestion{
		Kind: toolpkg.AskUserKindMulti,
		Choices: []toolpkg.AskUserChoice{
			{ID: "a", Label: "A"},
			{ID: "b", Label: "B"},
		},
	})

	qs.toggleCurrentChoice()
	if _, ok := qs.answers[0].selected["a"]; !ok {
		t.Error("expected 'a' selected")
	}
	qs.choiceCursor = 1
	qs.toggleCurrentChoice()
	if _, ok := qs.answers[0].selected["a"]; !ok {
		t.Error("expected 'a' still selected in multi mode")
	}
	if _, ok := qs.answers[0].selected["b"]; !ok {
		t.Error("expected 'b' selected in multi mode")
	}
}

func TestQuestionnaireState_NilSafety(t *testing.T) {
	var qs *questionnaireState
	qs.moveTab(1, LangEnglish)
	qs.moveChoice(1)
	qs.toggleCurrentChoice()
}

func TestQuestionnaireState_TabNavigation(t *testing.T) {
	qs := newTestQS(
		toolpkg.AskUserQuestion{Prompt: "Q1"},
		toolpkg.AskUserQuestion{Prompt: "Q2"},
	)

	qs.moveTab(1, LangEnglish)
	qs.moveTab(1, LangEnglish)
	if !qs.onSubmitTab() {
		t.Error("expected submit tab")
	}
	qs.moveTab(1, LangEnglish)
	if !qs.onCancelTab() {
		t.Error("expected cancel tab")
	}
	qs.moveTab(1, LangEnglish)
	if qs.tabIndex != 0 {
		t.Errorf("expected tabIndex=0, got %d", qs.tabIndex)
	}
}

// ── chat_bridge.go ──────────────────────────────────────────────────

func TestExtractRecentOutput(t *testing.T) {
	for _, tt := range []struct {
		input string
		want  string
	}{
		{"Some text\nRecent output:\nline1\nline2", "line1\nline2"},
		{"No marker here", ""},
		{"Recent output:\nhello world", "hello world"},
	} {
		got := extractRecentOutput(tt.input)
		if got != tt.want {
			t.Errorf("extractRecentOutput(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestFormatAskUserResult(t *testing.T) {
	input := `{"title":"Q","answers":[{"title":"Name?","selected_choices":[],"freeform_text":"Alice","answered":true}]}`
	got := formatAskUserResult(input)
	if got == "" {
		t.Error("expected non-empty result")
	}

	invalid := "not json"
	got2 := formatAskUserResult(invalid)
	if got2 != invalid {
		t.Errorf("expected raw string back, got %q", got2)
	}
}

// ── ask_user localization helpers ───────────────────────────────────

func TestQuestionnaireLabelsAll(t *testing.T) {
	for _, tt := range []struct {
		fn   func(Language) string
		en   string
		zh   string
		name string
	}{
		{questionnairePanelTitle, "Answer questions", "请补充信息", "panelTitle"},
		{questionnaireSubmitLabel, "Submit", "提交", "submitLabel"},
		{questionnaireCancelLabel, "Cancel", "取消", "cancelLabel"},
		{questionnaireSummaryLabel, "Answered", "已完成", "summaryLabel"},
		{questionnaireFreeformLabel, "Notes", "补充说明", "freeformLabel"},
		{questionnaireFreeformPlaceholder, "Optional notes", "可选补充说明", "freeformPlaceholder"},
	} {
		if tt.fn(LangEnglish) != tt.en {
			t.Errorf("%s en = %q, want %q", tt.name, tt.fn(LangEnglish), tt.en)
		}
		if tt.fn(LangZhCN) != tt.zh {
			t.Errorf("%s zh = %q, want %q", tt.name, tt.fn(LangZhCN), tt.zh)
		}
	}
}

func TestQuestionnaireStateBadge_NonPanics(t *testing.T) {
	_ = questionnaireStateBadge(toolpkg.AskUserCompletionAnswered)
	_ = questionnaireStateBadge(toolpkg.AskUserCompletionPartial)
	_ = questionnaireStateBadge("unknown")
}
