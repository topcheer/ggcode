package im

import (
	"testing"
)

func TestFormatSleepDuration(t *testing.T) {
	tests := []struct {
		args string
		want string
	}{
		{`{"seconds":5}`, "5s"},
		{`{"seconds":0}`, "0s"},
		{`{"seconds":30}`, "30s"},
		{`{"seconds":5,"milliseconds":500}`, "5.5s"},
		{`{"seconds":1,"milliseconds":100}`, "1.1s"},
		{`invalid`, ""},
	}
	for _, tc := range tests {
		got := formatSleepDuration(tc.args)
		if got != tc.want {
			t.Errorf("formatSleepDuration(%q) = %q, want %q", tc.args, got, tc.want)
		}
	}
}

func TestFormatIMSleepResult(t *testing.T) {
	// Success — should be suppressed
	tr := &ToolResultInfo{ToolName: "sleep", Result: "Sleep for 5s ... Done"}
	got := formatIMSleepResult(tr)
	if got != "" {
		t.Errorf("success sleep result should be suppressed, got %q", got)
	}

	// Error — should show
	trErr := &ToolResultInfo{ToolName: "sleep", Result: "sleep interrupted", IsError: true}
	got = formatIMSleepResult(trErr)
	if got == "" {
		t.Error("error sleep result should not be suppressed")
	}
}

func TestFormatToolCallText_Sleep(t *testing.T) {
	tc := &ToolCallInfo{ToolName: "sleep", Args: `{"seconds":10}`}
	got := formatToolCallText(tc)
	if got != "⏳ Sleep for 10s" {
		t.Errorf("formatToolCallText(sleep) = %q, want %q", got, "⏳ Sleep for 10s")
	}
}

func TestFormatToolResultText_Sleep_Suppressed(t *testing.T) {
	// Sleep success result should return empty (suppressed)
	tr := &ToolResultInfo{ToolName: "sleep", Result: "Sleep for 5s ... Done"}
	got := formatToolResultText(tr)
	if got != "" {
		t.Errorf("sleep success result should be suppressed, got %q", got)
	}
}
