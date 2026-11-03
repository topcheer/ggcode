package mcp

import (
	"encoding/json"
	"testing"
	"time"
)

func TestParseElicitationParamsURLMode(t *testing.T) {
	raw := json.RawMessage(`{
		"mode": "url",
		"elicitationId": "550e8400-e29b-41d4-a716-446655440000",
		"url": "https://mcp.example.com/ui/set_api_key",
		"message": "Please provide your API key to continue."
	}`)
	p, err := ParseElicitationParams(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if p.Mode != ElicitationModeURL {
		t.Fatalf("mode = %q, want url", p.Mode)
	}
	if p.URL != "https://mcp.example.com/ui/set_api_key" {
		t.Fatalf("url = %q", p.URL)
	}
	if p.ElicitationID == "" {
		t.Fatal("elicitationId missing")
	}
	if p.EffectiveMode() != ElicitationModeURL {
		t.Fatal("EffectiveMode should be url")
	}

	p2, err := ParseElicitationParams(json.RawMessage(`{
		"message": "hi",
		"requestedSchema": {"type":"object","properties":{"n":{"type":"string"}}}
	}`))
	if err != nil {
		t.Fatalf("parse form: %v", err)
	}
	if p2.EffectiveMode() != ElicitationModeForm {
		t.Fatalf("mode-less request must normalize to form, got %q", p2.EffectiveMode())
	}
}

func TestValidateElicitationURL(t *testing.T) {
	cases := []struct {
		url   string
		valid bool
	}{
		{"https://mcp.example.com/ui/set_api_key", true},
		{"http://localhost:3000/connect", true},
		{"http://127.0.0.1:8080/x", true},
		{"http://mcp.example.com/ui", false},
		{"ftp://mcp.example.com/x", false},
		{"/relative/path", false},
		{"", false},
	}
	for _, tc := range cases {
		err := ValidateElicitationURL(tc.url)
		if tc.valid != (err == nil) {
			t.Errorf("ValidateElicitationURL(%q) err = %v, want valid=%v", tc.url, err, tc.valid)
		}
	}
}

func TestValidateElicitationParamsURLMode(t *testing.T) {
	if err := validateElicitationParams(ElicitationParams{Mode: ElicitationModeURL}); err == nil {
		t.Fatal("url mode without elicitationId must fail")
	}
	if err := validateElicitationParams(ElicitationParams{Mode: ElicitationModeURL, ElicitationID: "x"}); err == nil {
		t.Fatal("url mode without url must fail")
	}
	if err := validateElicitationParams(ElicitationParams{Mode: ElicitationModeURL, ElicitationID: "x", URL: "http://evil.example.com"}); err == nil {
		t.Fatal("url mode with non-https non-local url must fail")
	}
	if err := validateElicitationParams(ElicitationParams{Mode: ElicitationModeURL, ElicitationID: "x", URL: "https://mcp.example.com/ui"}); err != nil {
		t.Fatalf("valid url mode rejected: %v", err)
	}

	if err := validateElicitationParams(ElicitationParams{}); err == nil {
		t.Fatal("form mode without schema must fail")
	}
}

func TestElicitationCapabilityAdvertisesBothModes(t *testing.T) {
	caps := ClientCaps{}
	caps.Roots.ListChanged = true
	caps.Elicitation = &ElicitationCapability{Form: &struct{}{}, URL: &struct{}{}}
	data, err := json.Marshal(caps)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var probe struct {
		Elicitation struct {
			Form map[string]any `json:"form"`
			URL  map[string]any `json:"url"`
		} `json:"elicitation"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if probe.Elicitation.Form == nil || probe.Elicitation.URL == nil {
		t.Fatalf("capability must declare both form and url modes, got %s", data)
	}
}

func TestElicitationCompleteNotificationBookkeeping(t *testing.T) {
	c := NewClient("test", "echo", nil)
	c.trackURLElicitation("id-1")
	if _, known := c.pendingURLElicitations["id-1"]; !known {
		t.Fatal("id-1 should be tracked")
	}
	c.handleElicitationComplete(json.RawMessage(`{"elicitationId":"id-1"}`))
	if _, known := c.pendingURLElicitations["id-1"]; known {
		t.Fatal("id-1 should be retired after completion")
	}

	c.handleElicitationComplete(json.RawMessage(`{"elicitationId":"nope"}`))
	c.handleElicitationComplete(json.RawMessage(`{}`))
	c.handleElicitationComplete(nil)
}

func TestProcessNotificationInterceptsElicitationComplete(t *testing.T) {
	c := NewClient("test", "echo", nil)

	generic := make(chan string, 8)
	c.SetNotificationHandler(func(method string, params json.RawMessage) {
		generic <- method
	})
	c.trackURLElicitation("id-2")
	c.processNotification(&Notification{
		Method: "notifications/elicitation/complete",
		Params: json.RawMessage(`{"elicitationId":"id-2"}`),
	})
	select {
	case method := <-generic:
		t.Fatalf("elicitation/complete must be handled internally, not forwarded (%s)", method)
	case <-time.After(200 * time.Millisecond):
	}
	if _, known := c.pendingURLElicitations["id-2"]; known {
		t.Fatal("id-2 should be retired via processNotification")
	}

	c.processNotification(&Notification{Method: "notifications/tools/list_changed"})
	select {
	case method := <-generic:
		if method != "notifications/tools/list_changed" {
			t.Fatalf("unexpected forwarded method %q", method)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("other notifications must still reach the generic handler")
	}
}
