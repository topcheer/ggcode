package im

// Issue #955: webhook-mode card.action.trigger callbacks were parsed from the
// wrong envelope layout (top-level action/open_id/message_id instead of the
// real event.action / event.operator.open_id / event.context.open_message_id
// nesting), so 100% of card callbacks were silently dropped after the webhook
// had already replied 200 (unrecoverable). Additionally verification_token was
// dead config — never checked — leaving an unauthenticated callback surface.

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

// The real Card 2.0 callback envelope must reach HandleInteractiveCallback
// with the correct message id, choice, sender and chat.
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

// Legacy tolerance: older envelopes with fields at the payload top level must
// still work (this was the only layout the old parser looked at, but only for
// fields that never appeared there in real Card 2.0 traffic).
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

// verification_token must now be enforced when configured: requests without a
// matching header.token / top-level token are rejected with 401 before any
// processing (previously the config was dead — never checked).
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

	// Correct token passes and the card callback is dispatched.
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

	// Unset token config (verification disabled) must not 401 everything.
	a2 := newFeishu955Adapter(mgr, "")
	body["header"].(map[string]any)["event_id"] = "evt-token-3"
	raw, _ = json.Marshal(body)
	rec = httptest.NewRecorder()
	a2.handleWebhook(rec, httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(raw)))
	if rec.Code != http.StatusOK {
		t.Errorf("no verifyToken configured: status = %d, want 200", rec.Code)
	}
}
