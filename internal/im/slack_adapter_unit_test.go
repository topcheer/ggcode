package im

import (
	"testing"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/permission"
)

func TestSlackOutboundText(t *testing.T) {
	adapter := &slackAdapter{}
	tests := []struct {
		name  string
		event OutboundEvent
		want  string
	}{
		{"text", OutboundEvent{Kind: OutboundEventText, Text: "hello"}, "hello"},
		{"status", OutboundEvent{Kind: OutboundEventStatus, Status: "thinking..."}, "thinking..."},
		{"approval_request", OutboundEvent{Kind: OutboundEventApprovalRequest, Approval: &ApprovalRequest{ToolName: "bash", Input: "rm -rf"}}, "[approval] bash\nrm -rf"},
		{"approval_result", OutboundEvent{Kind: OutboundEventApprovalResult, Result: &ApprovalResult{Decision: permission.Allow}}, "[approval result] allow"},
		{"unknown", OutboundEvent{Kind: "unknown"}, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := adapter.outboundText(tc.event)
			if got != tc.want {
				t.Errorf("outboundText() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestSlackMarkdownToMrkdwn(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"**bold**", "_bold_"},
		{"*italic*", "_italic_"},
		{"~~strike~~", "~strike~"},
		{"<script>", "&lt;script&gt;"},
		{"a & b", "a &amp; b"},
		{"plain text", "plain text"},
	}
	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			got := markdownToMrkdwn(tc.input)
			if got != tc.want {
				t.Errorf("markdownToMrkdwn(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestSlackSplitMessage(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		maxLen int
		want   int
	}{
		{"short", "hello", 4000, 1},
		{"empty", "", 4000, 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			chunks := splitSlackMessage(tc.input, tc.maxLen)
			if len(chunks) != tc.want {
				t.Errorf("got %d chunks, want %d", len(chunks), tc.want)
			}
		})
	}
}

func TestSlackSendExtractedImage_UnknownKind(t *testing.T) {
	adapter := &slackAdapter{}
	img := ExtractedImage{Kind: "unknown", Data: "test"}
	err := adapter.sendExtractedImage(nil, "C123", img)
	if err == nil {
		t.Error("expected error for unknown kind")
	}
}

func TestSlackNewAdapter_MissingToken(t *testing.T) {
	_, err := newSlackAdapter("test", config.IMConfig{}, config.IMAdapterConfig{Extra: map[string]any{}}, nil)
	if err == nil {
		t.Error("expected error for missing bot_token")
	}
}

func TestSlackNewAdapter_WithToken(t *testing.T) {
	adapter, err := newSlackAdapter("test", config.IMConfig{}, config.IMAdapterConfig{
		Extra: map[string]any{
			"bot_token": "xoxb-test",
			"app_token": "xapp-test",
		},
	}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if adapter.botToken != "xoxb-test" {
		t.Errorf("botToken = %q", adapter.botToken)
	}
	if adapter.appToken != "xapp-test" {
		t.Errorf("appToken = %q", adapter.appToken)
	}
}

func TestReplaceDelimiters(t *testing.T) {
	tests := []struct {
		text string
		from string
		to   string
		want string
	}{
		{"no match", "**", "*", "no match"},
		{"**bold**", "**", "*", "*bold*"},
		{"a**b**c", "**", "*", "a*b*c"},
	}
	for _, tc := range tests {
		got := replaceDelimiters(tc.text, tc.from, tc.to)
		if got != tc.want {
			t.Errorf("replaceDelimiters(%q, %q, %q) = %q, want %q", tc.text, tc.from, tc.to, got, tc.want)
		}
	}
}
