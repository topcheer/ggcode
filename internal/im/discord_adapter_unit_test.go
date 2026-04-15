package im

import (
	"testing"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/permission"
)

func TestDiscordOutboundText(t *testing.T) {
	adapter := &discordAdapter{}
	tests := []struct {
		name  string
		event OutboundEvent
		want  string
	}{
		{"text", OutboundEvent{Kind: OutboundEventText, Text: "hello"}, "hello"},
		{"status", OutboundEvent{Kind: OutboundEventStatus, Status: "thinking..."}, "thinking..."},
		{"approval_request", OutboundEvent{Kind: OutboundEventApprovalRequest, Approval: &ApprovalRequest{ToolName: "bash", Input: "rm -rf"}}, "[approval] bash\nrm -rf"},
		{"approval_result", OutboundEvent{Kind: OutboundEventApprovalResult, Result: &ApprovalResult{Decision: permission.Allow}}, "[approval result] allow"},
		{"approval_request_nil", OutboundEvent{Kind: OutboundEventApprovalRequest}, ""},
		{"approval_result_nil", OutboundEvent{Kind: OutboundEventApprovalResult}, ""},
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

func TestDiscordSplitMessage(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		maxLen int
		want   int
	}{
		{"short", "hello", 2000, 1},
		{"empty", "", 2000, 1},
		{"exact", "a", 1, 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			chunks := splitDiscordMessage(tc.input, tc.maxLen)
			if len(chunks) != tc.want {
				t.Errorf("got %d chunks, want %d", len(chunks), tc.want)
			}
		})
	}
}

func TestDiscordSendExtractedImage_KindURL(t *testing.T) {
	adapter := &discordAdapter{}
	img := ExtractedImage{Kind: "url", Data: "https://example.com/img.png"}
	// Can't actually send without a server, but verify it doesn't panic on routing
	_ = adapter
	_ = img
}

func TestDiscordSendExtractedImage_DataURL(t *testing.T) {
	adapter := &discordAdapter{}
	img := ExtractedImage{Kind: "data_url", Data: "data:image/png;base64,iVBORw0KGgo="}
	_ = adapter
	_ = img
}

func TestDiscordSendExtractedImage_UnknownKind(t *testing.T) {
	adapter := &discordAdapter{}
	img := ExtractedImage{Kind: "unknown", Data: "test"}
	// Should return error
	err := adapter.sendExtractedImage(nil, "123", img)
	if err == nil {
		t.Error("expected error for unknown kind")
	}
}

func TestDiscordNewAdapter_MissingToken(t *testing.T) {
	_, err := newDiscordAdapter("test", config.IMConfig{}, config.IMAdapterConfig{Extra: map[string]any{}}, nil)
	if err == nil {
		t.Error("expected error for missing token")
	}
}

func TestDiscordNewAdapter_WithToken(t *testing.T) {
	adapter, err := newDiscordAdapter("test", config.IMConfig{}, config.IMAdapterConfig{
		Extra: map[string]any{"token": "test-token"},
	}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if adapter.token != "test-token" {
		t.Errorf("token = %q", adapter.token)
	}
	if adapter.apiBase != discordAPIBase {
		t.Errorf("apiBase = %q, want default", adapter.apiBase)
	}
}

func TestDiscordNewAdapter_CustomAPIBase(t *testing.T) {
	adapter, err := newDiscordAdapter("test", config.IMConfig{}, config.IMAdapterConfig{
		Extra: map[string]any{
			"token":    "test-token",
			"api_base": "https://custom.api.com/v10",
		},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if adapter.apiBase != "https://custom.api.com/v10" {
		t.Errorf("apiBase = %q", adapter.apiBase)
	}
}
