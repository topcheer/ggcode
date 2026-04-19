package im

import (
	"testing"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/permission"
)

func TestFeishuOutboundText(t *testing.T) {
	adapter := &feishuAdapter{}
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

func TestFeishuSplitMessage(t *testing.T) {
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
			chunks := splitFeishuMessage(tc.input, tc.maxLen)
			if len(chunks) != tc.want {
				t.Errorf("got %d chunks, want %d", len(chunks), tc.want)
			}
		})
	}
}

func TestFeishuNewAdapter_MissingAppID(t *testing.T) {
	_, err := newFeishuAdapter("test", config.IMConfig{}, config.IMAdapterConfig{Extra: map[string]any{}}, nil)
	if err == nil {
		t.Error("expected error for missing app_id")
	}
}

func TestFeishuNewAdapter_WithAppID(t *testing.T) {
	adapter, err := newFeishuAdapter("test", config.IMConfig{}, config.IMAdapterConfig{
		Extra: map[string]any{
			"app_id":     "cli_test",
			"app_secret": "secret_test",
		},
	}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if adapter.appID != "cli_test" {
		t.Errorf("appID = %q", adapter.appID)
	}
	if adapter.domain != "feishu" {
		t.Errorf("domain = %q, want feishu", adapter.domain)
	}
}

func TestFeishuNewAdapter_LarkDomain(t *testing.T) {
	adapter, err := newFeishuAdapter("test", config.IMConfig{}, config.IMAdapterConfig{
		Extra: map[string]any{
			"app_id":     "cli_test",
			"app_secret": "secret_test",
			"domain":     "lark",
		},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if adapter.domain != "lark" {
		t.Errorf("domain = %q, want lark", adapter.domain)
	}
}

func TestFeishuResolveAPIBase(t *testing.T) {
	tests := []struct {
		domain string
		want   string
	}{
		{"feishu", "https://open.feishu.cn/open-apis"},
		{"lark", "https://open.larksuite.com/open-apis"},
		{"FEISHU", "https://open.feishu.cn/open-apis"},
		{"", "https://open.feishu.cn/open-apis"},
	}
	for _, tc := range tests {
		adapter := &feishuAdapter{domain: tc.domain}
		got := adapter.resolveAPIBase()
		if got != tc.want {
			t.Errorf("resolveAPIBase(%q) = %q, want %q", tc.domain, got, tc.want)
		}
	}
}

func TestFeishuParseMessageContent_PlainText(t *testing.T) {
	adapter := &feishuAdapter{}
	content := `{"text":"hello world"}`
	got := adapter.parseMessageContent(content)
	if got != "hello world" {
		t.Errorf("got %q, want %q", got, "hello world")
	}
}

func TestFeishuParseMessageContent_RichText(t *testing.T) {
	adapter := &feishuAdapter{}
	content := `{"zh_cn":{"title":"","content":[[{"tag":"text","text":"hello "},{"tag":"text","text":"world"}]]}}`
	got := adapter.parseMessageContent(content)
	if got != "hello world" {
		t.Errorf("got %q, want %q", got, "hello world")
	}
}

func TestFeishuSendExtractedImage_UnknownKind(t *testing.T) {
	adapter := &feishuAdapter{}
	img := ExtractedImage{Kind: "unknown", Data: "test"}
	err := adapter.sendExtractedImage(nil, "oc123", img)
	if err == nil {
		t.Error("expected error for unknown kind")
	}
}

func TestFeishuSendExtractedImage_InvalidDataURL(t *testing.T) {
	adapter := &feishuAdapter{}
	img := ExtractedImage{Kind: "data_url", Data: "invalid"}
	err := adapter.sendExtractedImage(nil, "oc123", img)
	if err == nil {
		t.Error("expected error for invalid data URL")
	}
}

func TestIntValueStr(t *testing.T) {
	tests := []struct {
		input string
		want  int
		ok    bool
	}{
		{"123", 123, true},
		{"", 0, false},
		{"12a", 0, false},
		{"0", 0, true},
	}
	for _, tc := range tests {
		got, ok := intValueStr(tc.input)
		if ok != tc.ok || got != tc.want {
			t.Errorf("intValueStr(%q) = (%d, %v), want (%d, %v)", tc.input, got, ok, tc.want, tc.ok)
		}
	}
}
