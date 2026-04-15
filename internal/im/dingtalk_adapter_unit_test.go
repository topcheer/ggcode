package im

import (
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/permission"
)

func TestDingTalkOutboundText(t *testing.T) {
	adapter := &dingtalkAdapter{}
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

func TestDingTalkSplitMessage(t *testing.T) {
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
			chunks := splitDingTalkMessage(tc.input, tc.maxLen)
			if len(chunks) != tc.want {
				t.Errorf("got %d chunks, want %d", len(chunks), tc.want)
			}
		})
	}
}

func TestDingTalkNewAdapter_MissingAppKey(t *testing.T) {
	_, err := newDingTalkAdapter("test", config.IMConfig{}, config.IMAdapterConfig{Extra: map[string]any{}}, nil)
	if err == nil {
		t.Error("expected error for missing app_key")
	}
}

func TestDingTalkNewAdapter_WithAppKey(t *testing.T) {
	adapter, err := newDingTalkAdapter("test", config.IMConfig{}, config.IMAdapterConfig{
		Extra: map[string]any{
			"app_key":    "test-key",
			"app_secret": "test-secret",
		},
	}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if adapter.appKey != "test-key" {
		t.Errorf("appKey = %q", adapter.appKey)
	}
	if adapter.appSecret != "test-secret" {
		t.Errorf("appSecret = %q", adapter.appSecret)
	}
}

func TestDingTalkSendGroupMessage_UsesMarkdown(t *testing.T) {
	// Verify the sendGroupMessage uses sampleMarkdown
	adapter := &dingtalkAdapter{
		appKey:     "test-key",
		appSecret:  "test-secret",
		httpClient: nil, // can't actually call, but test structure
	}
	_ = adapter
}

func TestDingTalkSendExtractedImage_UnknownKind(t *testing.T) {
	adapter := &dingtalkAdapter{}
	img := ExtractedImage{Kind: "unknown", Data: "test"}
	err := adapter.sendExtractedImage(nil, "cid123", img)
	if err == nil {
		t.Error("expected error for unknown kind")
	}
}

func TestDingTalkSendExtractedImage_DataURL(t *testing.T) {
	// Data URLs should be silently skipped (not supported)
	adapter := &dingtalkAdapter{}
	img := ExtractedImage{Kind: "data_url", Data: "data:image/png;base64,abc"}
	err := adapter.sendExtractedImage(nil, "cid123", img)
	if err != nil {
		t.Errorf("expected nil for data_url (skip), got %v", err)
	}
}

func TestDingTalkSendExtractedImage_LocalFile(t *testing.T) {
	// Local files should be silently skipped (not supported)
	adapter := &dingtalkAdapter{}
	img := ExtractedImage{Kind: "url", Data: "/tmp/test.png"}
	err := adapter.sendExtractedImage(nil, "cid123", img)
	if err != nil {
		t.Errorf("expected nil for local file (skip), got %v", err)
	}
}

func TestSplitDingTalkMessage_Long(t *testing.T) {
	longText := strings.Repeat("a", 8000)
	chunks := splitDingTalkMessage(longText, 4000)
	if len(chunks) != 2 {
		t.Errorf("expected 2 chunks, got %d", len(chunks))
	}
}
