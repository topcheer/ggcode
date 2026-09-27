package im

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// --- pure helper pin tests (r185 pc domain seams) ---

func TestMimeTypeForImageExt(t *testing.T) {
	cases := []struct{ ext, want string }{
		{".jpg", "image/jpeg"},
		{".jpeg", "image/jpeg"},
		{".gif", "image/gif"},
		{".webp", "image/webp"},
		{".png", "image/png"}, // default branch
		{"", "image/png"},     // default branch
		{".bmp", "image/png"}, // unknown ext → default
	}
	for _, c := range cases {
		if got := mimeTypeForImageExt(c.ext); got != c.want {
			t.Errorf("mimeTypeForImageExt(%q) = %q, want %q", c.ext, got, c.want)
		}
	}
}

func TestExtForImageMime(t *testing.T) {
	cases := []struct{ mime, want string }{
		{"image/jpeg", ".jpg"}, // canonical .jpg, not .jpeg
		{"image/gif", ".gif"},
		{"image/webp", ".webp"},
		{"image/png", ".png"}, // default branch
		{"", ".png"},          // default branch
		{"image/bmp", ".png"}, // unknown mime → default
	}
	for _, c := range cases {
		if got := extForImageMime(c.mime); got != c.want {
			t.Errorf("extForImageMime(%q) = %q, want %q", c.mime, got, c.want)
		}
	}
}

func TestMimeTypeFromDataURLHeader(t *testing.T) {
	cases := []struct{ header, want string }{
		{"data:image/jpeg;base64", "image/jpeg"},
		{"data:image/jpg;base64", "image/jpeg"}, // jpg alias → jpeg
		{"data:image/gif;base64", "image/gif"},
		{"data:image/webp;base64", "image/webp"},
		{"data:image/png;base64", "image/png"},
		{"data:text/plain", "image/png"}, // non-image → default
		{"", "image/png"},                // empty → default
	}
	for _, c := range cases {
		if got := mimeTypeFromDataURLHeader(c.header); got != c.want {
			t.Errorf("mimeTypeFromDataURLHeader(%q) = %q, want %q", c.header, got, c.want)
		}
	}
}

// --- handleMessage dispatch pins ---

func newTestRelayClientForDispatch() *pcRelayClient {
	return &pcRelayClient{
		pendingCreates:  make(map[string]chan *pcCreateResult, 1),
		pendingRenewals: make(map[string]chan *pcRenewResult, 1),
	}
}

func TestHandleMessageSessionCreatedDispatch(t *testing.T) {
	c := newTestRelayClientForDispatch()
	ch := make(chan *pcCreateResult, 1)
	c.pendingCreates["r1"] = ch

	msg := pcRelaySessionCreated{Type: pcTypeSessionCreated, RequestID: "r1", SessionID: "s1"}
	data, _ := json.Marshal(msg)
	if err := c.handleMessage(data); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	res, ok := <-ch
	if !ok || res == nil || res.sess == nil || res.sess.SessionID != "s1" {
		t.Fatalf("expected create result with session s1, got %+v", res)
	}
	if _, still := c.pendingCreates["r1"]; still {
		t.Error("expected pendingCreates entry to be deleted after dispatch")
	}
}

func TestHandleMessageSessionCreatedNoWaiter(t *testing.T) {
	c := newTestRelayClientForDispatch()
	msg := pcRelaySessionCreated{Type: pcTypeSessionCreated, RequestID: "missing", SessionID: "s1"}
	data, _ := json.Marshal(msg)
	// No matching waiter: must be a no-op with nil error (pre-refactor behavior).
	if err := c.handleMessage(data); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(c.pendingCreates) != 0 {
		t.Errorf("expected empty pendingCreates, got %d", len(c.pendingCreates))
	}
}

func TestHandleMessageErrorWithRequestID(t *testing.T) {
	c := newTestRelayClientForDispatch()
	chC := make(chan *pcCreateResult, 1)
	chR := make(chan *pcRenewResult, 1)
	c.pendingCreates["rc"] = chC
	c.pendingRenewals["rr"] = chR

	relayErr := pcRelayError{Type: pcTypeError, Code: "quota", Message: "limit hit", RequestID: "rc"}
	data, _ := json.Marshal(relayErr)
	if err := c.handleMessage(data); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	resC := <-chC
	if resC.err == nil || resC.err.Error() != "relay rejected create_session: quota: limit hit" {
		t.Fatalf("expected create error with relay reason, got %v", resC.err)
	}
	if _, still := c.pendingCreates["rc"]; still {
		t.Error("expected pendingCreates entry deleted")
	}
	// Renewal waiter for a DIFFERENT request id must remain untouched.
	if _, still := c.pendingRenewals["rr"]; !still {
		t.Error("expected unrelated pendingRenewals entry to survive")
	}

	// Now match the renewal id: error must carry the renew-specific prefix.
	relayErr.RequestID = "rr"
	data, _ = json.Marshal(relayErr)
	if err := c.handleMessage(data); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	resR := <-chR
	if resR.err == nil || resR.err.Error() != "relay rejected renew_session: quota: limit hit" {
		t.Fatalf("expected renew error with relay reason, got %v", resR.err)
	}
}

func TestHandleMessageErrorNoRequestIDFailsAll(t *testing.T) {
	c := newTestRelayClientForDispatch()
	// Each waiter owns its own buffered channel (mirrors CreateSession's
	// per-request make(chan, 1) - sharing one buffered channel across
	// waiters would block the lock-holding fan-out, which is also true of
	// the pre-refactor code).
	chA := make(chan *pcCreateResult, 1)
	chB := make(chan *pcCreateResult, 1)
	chR := make(chan *pcRenewResult, 1)
	c.pendingCreates["a"] = chA
	c.pendingCreates["b"] = chB
	c.pendingRenewals["x"] = chR

	var errMsg string
	c.onError = func(m string) { errMsg = m }

	relayErr := pcRelayError{Type: pcTypeError, Code: "gone", Message: "relay restarted"}
	data, _ := json.Marshal(relayErr)
	if err := c.handleMessage(data); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Both create waiters AND the renewal waiter failed with the relay error.
	resA := <-chA
	if resA.err == nil || !strings.Contains(resA.err.Error(), "relay restarted") {
		t.Fatalf("create waiter a: expected relay error, got %v", resA.err)
	}
	resB := <-chB
	if resB.err == nil || !strings.Contains(resB.err.Error(), "relay restarted") {
		t.Fatalf("create waiter b: expected relay error, got %v", resB.err)
	}
	resR := <-chR
	if resR.err == nil || !strings.Contains(resR.err.Error(), "relay restarted") {
		t.Fatalf("renew waiter: expected relay error, got %v", resR.err)
	}
	if len(c.pendingCreates) != 0 || len(c.pendingRenewals) != 0 {
		t.Errorf("expected both pending maps emptied, got %d/%d", len(c.pendingCreates), len(c.pendingRenewals))
	}
	if errMsg != "relay restarted" {
		t.Errorf("onError message = %q, want %q", errMsg, "relay restarted")
	}
}

func TestHandleMessageProviderReady(t *testing.T) {
	c := newTestRelayClientForDispatch()
	readyCh := make(chan struct{})
	c.readyCh = readyCh
	fired := false
	c.onReady = func() { fired = true }

	if err := c.handleMessage([]byte(`{"type":"relay:provider_ready"}`)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	select {
	case <-readyCh:
	default:
		t.Error("expected readyCh to be closed")
	}
	if c.readyCh != nil {
		t.Error("expected readyCh field reset to nil")
	}
	if !fired {
		t.Error("expected onReady callback to fire")
	}
}

func TestHandleMessageUnknownTypeAndBadJSON(t *testing.T) {
	c := newTestRelayClientForDispatch()
	// Unknown type: no-op, nil error.
	if err := c.handleMessage([]byte(`{"type":"relay:mystery"}`)); err != nil {
		t.Fatalf("unknown type should be nil-error no-op, got %v", err)
	}
	// Malformed JSON: parse error with preserved prefix.
	err := c.handleMessage([]byte(`{not-json`))
	if err == nil || !strings.Contains(err.Error(), "parse relay message type") {
		t.Fatalf("expected parse relay message type error, got %v", err)
	}
	// Well-formed JSON overall, but requestId typed as number breaks the
	// session_created unmarshal (peek only reads "type" and ignores the
	// unknown-mismatched field): parse error with preserved prefix.
	err = c.handleMessage([]byte(`{"type":"relay:session_created","requestId":123}`))
	if err == nil || !strings.Contains(err.Error(), "parse session_created") {
		t.Fatalf("expected parse session_created error, got %v", err)
	}
}

// --- resolvePCAttachment orchestrator pins ---

func TestResolvePCAttachmentWebPDataURL(t *testing.T) {
	adapter := &pcAdapter{}
	img := ExtractedImage{Kind: "data_url", Data: "data:image/webp;base64,AAAA"}
	att, err := adapter.resolvePCAttachment(context.Background(), img, 2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if att["mimeType"] != "image/webp" {
		t.Errorf("mimeType = %v, want image/webp", att["mimeType"])
	}
	if name, _ := att["name"].(string); name != "image_2.webp" {
		t.Errorf("name = %q, want image_2.webp", name)
	}
	if size, _ := att["sizeBytes"].(int); size != 3 { // "AAAA" decodes to 3 bytes
		t.Errorf("sizeBytes = %v, want 3", att["sizeBytes"])
	}
}

func TestResolvePCAttachmentBadBase64(t *testing.T) {
	adapter := &pcAdapter{}
	img := ExtractedImage{Kind: "data_url", Data: "data:image/png;base64,!!!"}
	_, err := adapter.resolvePCAttachment(context.Background(), img, 0)
	if err == nil || !strings.Contains(err.Error(), "invalid base64") {
		t.Fatalf("expected invalid base64 error, got %v", err)
	}
}
