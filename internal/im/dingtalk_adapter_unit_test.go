package im

import (
	"encoding/json"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
)

func TestNewDingtalkAdapter_RequiresCredentials(t *testing.T) {
	_, err := newDingtalkAdapter("test", nil, config.IMAdapterConfig{
		Extra: map[string]interface{}{},
	})
	if err == nil {
		t.Fatal("expected error for missing app_key/app_secret")
	}
}

func TestNewDingtalkAdapter_ValidConfig(t *testing.T) {
	a, err := newDingtalkAdapter("test", nil, config.IMAdapterConfig{
		Extra: map[string]interface{}{
			"app_key":    "dingtest123",
			"app_secret": "secret456",
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if a.Name() != "test" {
		t.Errorf("expected name 'test', got %q", a.Name())
	}
	if a.appKey != "dingtest123" {
		t.Errorf("expected appKey 'dingtest123', got %q", a.appKey)
	}
}

func TestDingtalkAdapter_Close(t *testing.T) {
	a, err := newDingtalkAdapter("test", nil, config.IMAdapterConfig{
		Extra: map[string]interface{}{
			"app_key":    "dingtest123",
			"app_secret": "secret456",
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := a.Close(); err != nil {
		t.Errorf("Close() error: %v", err)
	}
}

func TestDingtalkEscapeJSONString(t *testing.T) {
	tests := []struct {
		input  string
		expect string
	}{
		{`hello`, `hello`},
		{`hello "world"`, `hello \"world\"`},
		{"line1\nline2", `line1\nline2`},
		{`back\slash`, `back\\slash`},
	}
	for _, tt := range tests {
		got := escapeJSONString(tt.input)
		if got != tt.expect {
			t.Errorf("escapeJSONString(%q) = %q, want %q", tt.input, got, tt.expect)
		}
	}
}

func TestDingtalkDataFrameResponse(t *testing.T) {
	frame := dingtalkDataFrame{
		SpecVersion: "1.0",
		Type:        dingtalkSubCallback,
		Headers: map[string]string{
			dfHeaderTopic:       dingtalkBotCallbackTopic,
			dfHeaderMessageID:   "msg-123",
			dfHeaderContentType: dfContentTypeJSON,
		},
		Data: `{"text":"hello"}`,
	}

	if frame.Headers[dfHeaderTopic] != dingtalkBotCallbackTopic {
		t.Errorf("expected topic %q, got %q", dingtalkBotCallbackTopic, frame.Headers[dfHeaderTopic])
	}
	if frame.Headers[dfHeaderMessageID] != "msg-123" {
		t.Errorf("expected messageID 'msg-123', got %q", frame.Headers[dfHeaderMessageID])
	}
}

func TestDingtalkBotCallbackDataParsing(t *testing.T) {
	data := `{
		"conversationId": "cid123",
		"senderStaffId": "staff456",
		"senderNick": "Test User",
		"msgId": "msg789",
		"text": {"content": "hello bot"},
		"sessionWebhook": "https://oapi.dingtalk.com/robot/sendByToken?token=xxx",
		"robotCode": "dingtest123",
		"conversationType": "1",
		"msgtype": "text"
	}`

	var callback dingtalkBotCallbackData
	if err := json.Unmarshal([]byte(data), &callback); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if callback.ConversationID != "cid123" {
		t.Errorf("expected conversationId 'cid123', got %q", callback.ConversationID)
	}
	if callback.Text.Content != "hello bot" {
		t.Errorf("expected text.content 'hello bot', got %q", callback.Text.Content)
	}
	if callback.SessionWebhook == "" {
		t.Error("expected non-empty sessionWebhook")
	}
}
