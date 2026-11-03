package im

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func newFeishu955Adapter(mgr *Manager, token string) *feishuAdapter {
	return &feishuAdapter{
		name:        "feishu",
		manager:     mgr,
		verifyToken: token,
		seenEvents:  make(map[string]time.Time),
		seenNonces:  make(map[string]time.Time),
	}
}

func TestFeishuCardActionParsesNestedEventFields(t *testing.T) {
	mgr := NewManager()
	got := make(chan InteractiveCallback, 1)
	mgr.SetInteractiveCallback(func(cb InteractiveCallback) {
		select {
		case got <- cb:
		default:
		}
	})
	a := newFeishu955Adapter(mgr, "")

	a.handleCardAction(t.Context(), map[string]any{
		"event": map[string]any{
			"operator": map[string]any{"open_id": "ou_955"},
			"action":   map[string]any{"value": map[string]any{"choice": "2"}, "tag": "button"},
			"context": map[string]any{
				"open_message_id": "om_955",
				"open_chat_id":    "oc_955",
			},
		},
	})

	select {
	case cb := <-got:
		if cb.Envelope.MessageID != "om_955" {
			t.Errorf("MessageID = %q, want om_955", cb.Envelope.MessageID)
		}
		if len(cb.Values) != 1 || cb.Values[0] != "2" {
			t.Errorf("Values = %v, want [2]", cb.Values)
		}
		if cb.Envelope.SenderID != "ou_955" {
			t.Errorf("SenderID = %q, want ou_955", cb.Envelope.SenderID)
		}
		if cb.Envelope.ChannelID != "oc_955" {
			t.Errorf("ChannelID = %q, want oc_955", cb.Envelope.ChannelID)
		}
		if cb.Envelope.Platform != PlatformFeishu {
			t.Errorf("Platform = %q, want %q", cb.Envelope.Platform, PlatformFeishu)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("card action callback not delivered — parsing dropped it (#955)")
	}
}

func TestFeishuCardActionToleratesTopLevelFallback(t *testing.T) {
	mgr := NewManager()
	got := make(chan InteractiveCallback, 1)
	mgr.SetInteractiveCallback(func(cb InteractiveCallback) {
		select {
		case got <- cb:
		default:
		}
	})
	a := newFeishu955Adapter(mgr, "")

	a.handleCardAction(t.Context(), map[string]any{
		"action":   map[string]any{"value": map[string]any{"choice": "1"}},
		"operator": map[string]any{"open_id": "ou_top"},
		"context":  map[string]any{"open_message_id": "om_top"},
	})

	select {
	case cb := <-got:
		if cb.Envelope.MessageID != "om_top" || cb.Envelope.SenderID != "ou_top" {
			t.Errorf("fallback parse: MessageID=%q SenderID=%q", cb.Envelope.MessageID, cb.Envelope.SenderID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("top-level fallback card action not delivered")
	}
}

func TestFeishuWebhookVerificationTokenEnforced(t *testing.T) {
	mgr := NewManager()
	calls := make(chan InteractiveCallback, 4)
	mgr.SetInteractiveCallback(func(cb InteractiveCallback) {
		select {
		case calls <- cb:
		default:
		}
	})
	a := newFeishu955Adapter(mgr, "tok-955")

	body := map[string]any{
		"header": map[string]any{
			"event_type": "card.action.trigger",
			"event_id":   "evt-token-1",
			"token":      "WRONG-TOKEN",
		},
		"event": map[string]any{
			"operator": map[string]any{"open_id": "ou_1"},
			"action":   map[string]any{"value": map[string]any{"choice": "1"}},
			"context":  map[string]any{"open_message_id": "om_1"},
		},
	}
	raw, _ := json.Marshal(body)

	rec := httptest.NewRecorder()
	a.handleWebhook(rec, httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(raw)))
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("wrong token: status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}

	body["header"].(map[string]any)["token"] = "tok-955"
	body["header"].(map[string]any)["event_id"] = "evt-token-2"
	raw, _ = json.Marshal(body)
	rec = httptest.NewRecorder()
	a.handleWebhook(rec, httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(raw)))
	if rec.Code != http.StatusOK {
		t.Fatalf("correct token: status = %d, want 200", rec.Code)
	}
	select {
	case cb := <-calls:
		if cb.Envelope.MessageID != "om_1" {
			t.Errorf("MessageID = %q, want om_1", cb.Envelope.MessageID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("card callback not dispatched after token verification")
	}

	a2 := newFeishu955Adapter(mgr, "")
	body["header"].(map[string]any)["event_id"] = "evt-token-3"
	raw, _ = json.Marshal(body)
	rec = httptest.NewRecorder()
	a2.handleWebhook(rec, httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(raw)))
	if rec.Code != http.StatusOK {
		t.Errorf("no verifyToken configured: status = %d, want 200", rec.Code)
	}
}
