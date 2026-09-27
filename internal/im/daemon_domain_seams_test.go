package im

import (
	"errors"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/provider"
	toolpkg "github.com/topcheer/ggcode/internal/tool"
)

// Pin tests for the r180 daemon_bridge domain seams (handleInteractiveCallback,
// beginVisionTurn, HandleAskUser, handleApproval, formatToolSummary).
// Behavior-preserving extraction: these lock the pure helpers' semantics.

func TestInteractiveStaleDetail(t *testing.T) {
	cases := []struct {
		name         string
		expectedID   string
		lastEmitted  string
		cbMessageID  string
		want         string
		wantContains string
	}{
		{"match", "m1", "m1", "m1", "", ""},
		{"stale vs expected", "m1", "m1", "m2", `expected="m1"`, ""},
		{"text-only successor", "", "m0", "m2", "- current question is text-only, no button was emitted for it", ""},
		{"nothing on record", "", "", "m9", "", ""},
		{"no expected but no last either", "", "", "", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := interactiveStaleDetail(tc.expectedID, tc.lastEmitted, tc.cbMessageID)
			if tc.wantContains != "" && !strings.Contains(got, tc.wantContains) {
				t.Fatalf("interactiveStaleDetail(%q,%q,%q) = %q, want contains %q", tc.expectedID, tc.lastEmitted, tc.cbMessageID, got, tc.wantContains)
			}
			if tc.wantContains == "" && got != tc.want {
				t.Fatalf("interactiveStaleDetail(%q,%q,%q) = %q, want %q", tc.expectedID, tc.lastEmitted, tc.cbMessageID, got, tc.want)
			}
		})
	}
	// Detail formatting pin: the stale-vs-expected detail embeds the expected
	// ID with %q quoting, matching the original log line suffix exactly.
	if got := interactiveStaleDetail("abc", "", "xyz"); got != `expected="abc"` {
		t.Fatalf("stale detail = %q, want quoted expected id", got)
	}
}

func TestFirstCallbackValue(t *testing.T) {
	if got := firstCallbackValue(nil); got != "" {
		t.Fatalf("firstCallbackValue(nil) = %q", got)
	}
	if got := firstCallbackValue([]string{}); got != "" {
		t.Fatalf("firstCallbackValue(empty) = %q", got)
	}
	if got := firstCallbackValue([]string{"a", "b"}); got != "a" {
		t.Fatalf("firstCallbackValue = %q, want a", got)
	}
}

func TestContentHasImage(t *testing.T) {
	if contentHasImage(nil) {
		t.Fatal("nil content should not have image")
	}
	if contentHasImage([]provider.ContentBlock{{Type: "text", Text: "hi"}}) {
		t.Fatal("text-only content should not have image")
	}
	if !contentHasImage([]provider.ContentBlock{
		{Type: "text", Text: "hi"},
		{Type: "image", ImageMIME: "image/png"},
	}) {
		t.Fatal("image block should be detected")
	}
}

func TestVisionModelForTurn(t *testing.T) {
	called := false
	empty := func() string { called = true; return "" }
	if got := visionModelForTurn(empty, "user-model"); got != "" {
		t.Fatalf("empty selection should resolve to empty, got %q", got)
	}
	if !called {
		t.Fatal("selector must be invoked exactly once")
	}
	same := func() string { return "user-model" }
	if got := visionModelForTurn(same, "user-model"); got != "" {
		t.Fatalf("vision model equal to user model should resolve to empty, got %q", got)
	}
	pick := func() string { return "vision-model" }
	if got := visionModelForTurn(pick, "user-model"); got != "vision-model" {
		t.Fatalf("vision model should pass through, got %q", got)
	}
}

func TestVisionTurnRestore(t *testing.T) {
	t.Run("switches back exactly once", func(t *testing.T) {
		switches := 0
		restore := visionTurnRestore("user-model", func(string) error {
			switches++
			return nil
		})
		restore()
		restore()
		restore()
		if switches != 1 {
			t.Fatalf("restore switched %d times, want exactly 1 (once-guard pin)", switches)
		}
	})
	t.Run("empty user model never switches", func(t *testing.T) {
		switches := 0
		restore := visionTurnRestore("", func(string) error {
			switches++
			return nil
		})
		restore()
		restore()
		if switches != 0 {
			t.Fatalf("empty userModel must skip restore, got %d switches", switches)
		}
	})
	t.Run("switch error does not panic", func(t *testing.T) {
		restore := visionTurnRestore("user-model", func(string) error {
			return errTestVisionSwitch
		})
		restore() // error path must be swallowed (logged), not panicked
	})
}

var errTestVisionSwitch = errors.New("vision switch boom")

func TestNewUnansweredAskUserAnswer(t *testing.T) {
	q := toolpkg.AskUserQuestion{ID: "q1", Title: "Pick", Kind: toolpkg.AskUserKindSingle}
	a := newUnansweredAskUserAnswer(q)
	if a.ID != "q1" || a.Title != "Pick" || a.Kind != toolpkg.AskUserKindSingle {
		t.Fatalf("unanswered answer lost question identity: %+v", a)
	}
	if a.Answered {
		t.Fatal("unanswered answer must not be marked Answered")
	}
	if a.CompletionStatus != toolpkg.AskUserCompletionUnanswered {
		t.Fatalf("CompletionStatus = %v, want Unanswered", a.CompletionStatus)
	}
	if a.AnswerMode != toolpkg.AskUserAnswerModeNone {
		t.Fatalf("AnswerMode = %v, want None", a.AnswerMode)
	}
}

func TestApprovalDecisionLabel(t *testing.T) {
	cases := []struct {
		decision permission.Decision
		always   bool
		want     string
	}{
		{permission.Deny, false, "deny"},
		{permission.Deny, true, "deny"},
		{permission.Allow, false, "allow"},
		{permission.Allow, true, "always"},
	}
	for _, tc := range cases {
		if got := approvalDecisionLabel(tc.decision, tc.always); got != tc.want {
			t.Fatalf("approvalDecisionLabel(%v,%v) = %q, want %q", tc.decision, tc.always, got, tc.want)
		}
	}
}

func TestApprovalPromptAndResultText(t *testing.T) {
	zhPrompt := approvalPromptText("zh-CN", "bash", "ls")
	enPrompt := approvalPromptText("en", "bash", "ls")
	if zhPrompt == "" || enPrompt == "" {
		t.Fatal("approval prompts must be non-empty")
	}
	if zhPrompt == enPrompt {
		t.Fatal("zh and en approval prompts must differ")
	}
	zhDeny := approvalResultText("zh", "bash", permission.Deny, false)
	enDeny := approvalResultText("en", "bash", permission.Deny, false)
	if zhDeny == "" || enDeny == "" || zhDeny == enDeny {
		t.Fatal("approval results must be non-empty and language-differentiated")
	}
	// Decision label pin: allow-always wording differs from plain allow.
	if approvalResultText("en", "bash", permission.Allow, true) == approvalResultText("en", "bash", permission.Allow, false) {
		t.Fatal("always-allow confirmation must differ from one-shot allow")
	}
}

func TestToolSummarySymbol(t *testing.T) {
	cases := []struct {
		failures, count int
		want            string
	}{
		{0, 1, "✓"},
		{0, 5, "✓"},
		{2, 2, "✗"},
		{1, 3, "⚠"},
	}
	for _, tc := range cases {
		if got := toolSummarySymbol(tc.failures, tc.count); got != tc.want {
			t.Fatalf("toolSummarySymbol(%d,%d) = %q, want %q", tc.failures, tc.count, got, tc.want)
		}
	}
}

func TestToolSummaryHeader(t *testing.T) {
	if got := toolSummaryHeader("en", 3, 2, 1); got != "⚙ 3 tool calls (2 ok, 1 failed)\n" {
		t.Fatalf("en header with failures = %q", got)
	}
	if got := toolSummaryHeader("en", 3, 3, 0); got != "⚙ 3 tool calls\n" {
		t.Fatalf("en header clean = %q", got)
	}
	if got := toolSummaryHeader("zh-CN", 3, 2, 1); got != "⚙ 执行了 3 个工具调用（2 成功，1 失败）\n" {
		t.Fatalf("zh header with failures = %q", got)
	}
	if got := toolSummaryHeader("zh-CN", 3, 3, 0); got != "⚙ 执行了 3 个工具调用\n" {
		t.Fatalf("zh header clean = %q", got)
	}
}

func TestAggregateToolUsageStats(t *testing.T) {
	tools := []ToolResultInfo{
		{ToolName: "read", IsError: false},
		{ToolName: "read", IsError: true},
		{ToolName: "bash", IsError: false},
		{ToolName: "bash", IsError: false},
		{ToolName: "bash", IsError: true},
	}
	got := aggregateToolUsageStats(tools)
	if len(got) != 2 {
		t.Fatalf("aggregated %d stats, want 2", len(got))
	}
	// Sort pin: descending by count; names use formatIMToolDisplayName.
	wantFirstName := formatIMToolDisplayName("bash")
	wantSecondName := formatIMToolDisplayName("read")
	if got[0].name != wantFirstName || got[0].count != 3 || got[0].failures != 1 {
		t.Fatalf("first stat = %+v, want %s x3 with 1 failure", got[0], wantFirstName)
	}
	if got[1].name != wantSecondName || got[1].count != 2 || got[1].failures != 1 {
		t.Fatalf("second stat = %+v, want %s x2 with 1 failure", got[1], wantSecondName)
	}
	if len(aggregateToolUsageStats(nil)) != 0 {
		t.Fatal("nil tools must aggregate to empty")
	}
}

func TestFormatToolUsageLine(t *testing.T) {
	if got := formatToolUsageLine(toolUsageStat{name: "read", count: 1}); got != "  ✓ read\n" {
		t.Fatalf("single line = %q", got)
	}
	if got := formatToolUsageLine(toolUsageStat{name: "read", count: 3}); got != "  ✓ read ×3\n" {
		t.Fatalf("multi line = %q", got)
	}
	if got := formatToolUsageLine(toolUsageStat{name: "bash", count: 2, failures: 2}); got != "  ✗ bash ×2\n" {
		t.Fatalf("failed line = %q", got)
	}
}
