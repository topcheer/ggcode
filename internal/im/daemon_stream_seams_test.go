package im

import "testing"

// Pin tests for the runAgentStream pure seams (r179). These functions were
// extracted verbatim from the stream callback in daemon_bridge.go; the tests
// pin their routing semantics so future edits to the stream phases cannot
// silently change IM-visible behavior.

func TestPlanModeStartLabel(t *testing.T) {
	cases := []struct {
		name     string
		argsJSON string
		lang     string
		want     string
	}{
		{"empty args en", `{}`, "en", "📝 Planning..."},
		{"empty args default zh", `{}`, "", "📝 正在规划..."},
		{"invalid json en", `not-json`, "en", "📝 Planning..."},
		{"invalid json zh", `not-json`, "zh-CN", "📝 正在规划..."},
		{"empty description falls to default", `{"description":""}`, "en", "📝 Planning..."},
		{"description overrides en", `{"description":"Refactor auth"}`, "en", "📝 Refactor auth"},
		{"description overrides zh", `{"description":"正在重构"}`, "zh-CN", "📝 正在重构"},
	}
	for _, tc := range cases {
		if got := planModeStartLabel(tc.argsJSON, tc.lang); got != tc.want {
			t.Errorf("%s: planModeStartLabel(%q,%q) = %q, want %q", tc.name, tc.argsJSON, tc.lang, got, tc.want)
		}
	}
}

func TestNewSleepToolEvent(t *testing.T) {
	args := `{"duration":"1500ms"}`
	ev := newSleepToolEvent(args)
	if ev.Kind != OutboundEventToolCall {
		t.Fatalf("Kind = %v, want %v", ev.Kind, OutboundEventToolCall)
	}
	if ev.ToolCall == nil {
		t.Fatal("ToolCall is nil")
	}
	if ev.ToolCall.ToolName != "sleep" {
		t.Errorf("ToolName = %q, want sleep", ev.ToolCall.ToolName)
	}
	if ev.ToolCall.Args != args {
		t.Errorf("Args = %q, want %q", ev.ToolCall.Args, args)
	}
	if ev.ToolCall.Detail != formatSleepDuration(args) {
		t.Errorf("Detail = %q, want formatSleepDuration(%q) = %q", ev.ToolCall.Detail, args, formatSleepDuration(args))
	}
}

func TestBufferToolResult(t *testing.T) {
	cases := []struct {
		name    string
		mode    string
		isError bool
		want    bool
	}{
		{"summary buffers success", "summary", false, true},
		{"summary buffers error too", "summary", true, true},
		{"quiet buffers success", "quiet", false, true},
		{"quiet emits error immediately", "quiet", true, false},
		{"verbose emits success", "verbose", false, false},
		{"verbose emits error", "verbose", true, false},
		{"empty mode falls to verbose", "", false, false},
	}
	for _, tc := range cases {
		if got := bufferToolResult(tc.mode, tc.isError); got != tc.want {
			t.Errorf("%s: bufferToolResult(%q,%v) = %v, want %v", tc.name, tc.mode, tc.isError, got, tc.want)
		}
	}
}
