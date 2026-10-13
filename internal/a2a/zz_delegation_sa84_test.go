package a2a

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// sa-84 (A-JWT-lite): delegation declarations in message metadata.
// These tests pin the client-side injection contract and the server-side
// policy gate. Default behavior (no SetDelegation / zero policy) MUST be
// byte-identical to before the feature existed.

func newDelegationCaptureServer(t *testing.T, body *atomic.Value) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		body.Store(string(b))
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      json.RawMessage(`1`),
			Result:  json.RawMessage(`{"id":"t1","status":{"state":"completed"}}`),
		})
	}))
}

func TestClientInjectsDelegationMetadata(t *testing.T) {
	var body atomic.Value
	srv := newDelegationCaptureServer(t, &body)
	defer srv.Close()

	c := NewClient(srv.URL, "")
	c.SetDelegation("alice", "fix the login bug")
	if _, err := c.SendMessage(context.Background(), "code-edit", "do it"); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}

	raw, _ := body.Load().(string)
	var req struct {
		Params SendMessageParams `json:"params"`
	}
	if err := json.Unmarshal([]byte(raw), &req); err != nil {
		t.Fatalf("unmarshal request: %v\nbody: %s", err, raw)
	}
	var meta map[string]json.RawMessage
	if err := json.Unmarshal(req.Params.Message.Metadata, &meta); err != nil {
		t.Fatalf("message metadata is not a JSON object: %v\nmetadata: %s", err, req.Params.Message.Metadata)
	}
	var principal string
	if err := json.Unmarshal(meta[MetadataKeyOnBehalfOf], &principal); err != nil || principal != "alice" {
		t.Fatalf("on-behalf-of = %q (err %v), want %q", principal, err, "alice")
	}
	var d delegationPayload
	if err := json.Unmarshal(meta[MetadataKeyDelegation], &d); err != nil {
		t.Fatalf("delegation payload: %v", err)
	}
	if d.Intent != "fix the login bug" {
		t.Fatalf("intent = %q, want %q", d.Intent, "fix the login bug")
	}
	if d.DelegationDepth != 1 {
		t.Fatalf("delegation-depth = %d, want 1", d.DelegationDepth)
	}
	if d.IssuedAt == "" {
		t.Fatal("issued-at must be set")
	}
}

func TestClientNoDelegationNoMetadata(t *testing.T) {
	var body atomic.Value
	srv := newDelegationCaptureServer(t, &body)
	defer srv.Close()

	c := NewClient(srv.URL, "")
	if _, err := c.SendMessage(context.Background(), "code-edit", "hello"); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	raw, _ := body.Load().(string)
	if strings.Contains(raw, "ggcode.dev") {
		t.Fatalf("default client must not inject delegation metadata:\n%s", raw)
	}
	if strings.Contains(raw, "\"metadata\"") {
		t.Fatalf("default client must not emit a metadata field:\n%s", raw)
	}
}

func TestClientMergeExistingMetadata(t *testing.T) {
	var body atomic.Value
	srv := newDelegationCaptureServer(t, &body)
	defer srv.Close()

	c := NewClient(srv.URL, "")
	c.SetDelegation("bob", "")
	_, err := c.SendMessageWithConfig(ctxBG(), SendMessageParams{
		Message: Message{
			Role:      "user",
			MessageID: "m1",
			Parts:     []Part{{Kind: "text", Text: "hi"}},
			Metadata:  json.RawMessage(`{"custom":"keep-me"}`),
		},
		Skill: "code-edit",
	})
	if err != nil {
		t.Fatalf("SendMessageWithConfig: %v", err)
	}

	raw, _ := body.Load().(string)
	var req struct {
		Params SendMessageParams `json:"params"`
	}
	if err := json.Unmarshal([]byte(raw), &req); err != nil {
		t.Fatalf("unmarshal request: %v", err)
	}
	var meta map[string]json.RawMessage
	if err := json.Unmarshal(req.Params.Message.Metadata, &meta); err != nil {
		t.Fatalf("merged metadata is not a JSON object: %v", err)
	}
	if string(meta["custom"]) != `"keep-me"` {
		t.Fatalf("pre-existing metadata key clobbered: %s", req.Params.Message.Metadata)
	}
	var principal string
	if err := json.Unmarshal(meta[MetadataKeyOnBehalfOf], &principal); err != nil || principal != "bob" {
		t.Fatalf("on-behalf-of = %q (err %v), want bob", principal, err)
	}
	if _, ok := meta[MetadataKeyDelegation]; !ok {
		t.Fatalf("delegation payload missing after merge: %s", req.Params.Message.Metadata)
	}
}

func TestServerRejectsDepthExceeded(t *testing.T) {
	srv := &Server{delegation: DelegationPolicy{MaxDepth: 2}}
	msg := &Message{Metadata: json.RawMessage(`{"ggcode.dev/delegation":{"delegation-depth":5}}`)}
	rr := httptest.NewRecorder()
	if srv.checkDelegation(rr, json.RawMessage(`1`), msg) {
		t.Fatal("depth 5 with max 2 must be rejected")
	}
	if !strings.Contains(rr.Body.String(), "delegation-depth 5 exceeds max 2") {
		t.Fatalf("unexpected error body: %s", rr.Body.String())
	}

	// Within bound passes.
	rr2 := httptest.NewRecorder()
	if !srv.checkDelegation(rr2, json.RawMessage(`1`), &Message{Metadata: json.RawMessage(`{"ggcode.dev/delegation":{"delegation-depth":2}}`)}) {
		t.Fatalf("depth 2 with max 2 must pass, body: %s", rr2.Body.String())
	}
	// No declaration at all passes (depth check only constrains declared chains).
	rr3 := httptest.NewRecorder()
	if !srv.checkDelegation(rr3, json.RawMessage(`1`), &Message{}) {
		t.Fatalf("bare message must pass a depth-only policy, body: %s", rr3.Body.String())
	}
}

func TestServerDelegationPolicyDisabledByDefault(t *testing.T) {
	srv := &Server{}

	// Declared message passes.
	rr := httptest.NewRecorder()
	if !srv.checkDelegation(rr, json.RawMessage(`1`), &Message{Metadata: json.RawMessage(`{"ggcode.dev/on-behalf-of":"eve","ggcode.dev/delegation":{"delegation-depth":99}}`)}) {
		t.Fatalf("default policy must accept declared messages, body: %s", rr.Body.String())
	}
	// Bare message passes — old peers keep working.
	rr2 := httptest.NewRecorder()
	if !srv.checkDelegation(rr2, json.RawMessage(`1`), &Message{}) {
		t.Fatalf("default policy must accept bare messages, body: %s", rr2.Body.String())
	}

	// RequireOnBehalfOf rejects messages without the declaration...
	srv2 := &Server{delegation: DelegationPolicy{RequireOnBehalfOf: true}}
	rr3 := httptest.NewRecorder()
	if srv2.checkDelegation(rr3, json.RawMessage(`1`), &Message{}) {
		t.Fatal("RequireOnBehalfOf must reject bare messages")
	}
	// ...including an empty-string declaration.
	rr4 := httptest.NewRecorder()
	if srv2.checkDelegation(rr4, json.RawMessage(`1`), &Message{Metadata: json.RawMessage(`{"ggcode.dev/on-behalf-of":""}`)}) {
		t.Fatal("RequireOnBehalfOf must reject empty on-behalf-of")
	}
	// ...and accepts a real declaration.
	rr5 := httptest.NewRecorder()
	if !srv2.checkDelegation(rr5, json.RawMessage(`1`), &Message{Metadata: json.RawMessage(`{"ggcode.dev/on-behalf-of":"alice"}`)}) {
		t.Fatalf("RequireOnBehalfOf must accept declared messages, body: %s", rr5.Body.String())
	}
}

func ctxBG() context.Context { return context.Background() }
