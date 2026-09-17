package a2a

// A2A v1.0 compatibility tests (sa-71): ProtoJSON enum decoding (1.0.0
// changelog #1384), legacy well-known URI fallback (0.3.0 rename), and
// application/a2a+json media-type preference (1.0.1 #1753).

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTaskStateV1Unmarshal(t *testing.T) {
	cases := []struct {
		wire     string
		want     TaskState
		terminal bool
	}{
		{`"TASK_STATE_COMPLETED"`, TaskStateCompleted, true},
		{`"TASK_STATE_FAILED"`, TaskStateFailed, true},
		{`"TASK_STATE_CANCELED"`, TaskStateCanceled, true},
		{`"TASK_STATE_REJECTED"`, TaskStateRejected, true},
		{`"TASK_STATE_WORKING"`, TaskStateWorking, false},
		{`"TASK_STATE_SUBMITTED"`, TaskStateSubmitted, false},
		{`"TASK_STATE_INPUT_REQUIRED"`, TaskStateInputRequired, false},
		{`"TASK_STATE_AUTH_REQUIRED"`, TaskStateAuthRequired, false},
		// legacy lowercase (0.2.x/0.3.x JSON-RPC peers)
		{`"completed"`, TaskStateCompleted, true},
		{`"working"`, TaskStateWorking, false},
		// historical spellings tolerated for interop
		{`"cancelled"`, TaskStateCanceled, true},
		{`"input_required"`, TaskStateInputRequired, false},
		{`"auth_required"`, TaskStateAuthRequired, false},
		// unknown states preserved verbatim (forward compat)
		{`"brand-new-state"`, TaskState("brand-new-state"), false},
	}
	for _, tc := range cases {
		var s TaskState
		if err := json.Unmarshal([]byte(tc.wire), &s); err != nil {
			t.Fatalf("unmarshal %s: %v", tc.wire, err)
		}
		if s != tc.want {
			t.Errorf("wire %s: got %q, want %q", tc.wire, s, tc.want)
		}
		if got := s.IsTerminal(); got != tc.terminal {
			t.Errorf("wire %s: IsTerminal()=%v, want %v", tc.wire, got, tc.terminal)
		}
	}
}

// The motivating bug: a v1.0 remote agent reporting TASK_STATE_COMPLETED
// decoded as an unknown state whose IsTerminal() is false, so the caller
// waited forever on a finished task.
func TestTaskStatusV1DecodeTerminal(t *testing.T) {
	var st TaskStatus
	wire := []byte(`{"state":"TASK_STATE_COMPLETED","timestamp":"2026-01-01T00:00:00Z"}`)
	if err := json.Unmarshal(wire, &st); err != nil {
		t.Fatalf("unmarshal status: %v", err)
	}
	if !st.IsTerminal() {
		t.Fatalf("v1 completed state must be terminal, got state=%q", st.State)
	}
}

// MarshalJSON keeps emitting the legacy lowercase name so older ggcode
// peers round-trip unchanged (1.0.0 changelog #1401 compat allowance).
func TestTaskStateMarshalLegacy(t *testing.T) {
	b, err := json.Marshal(TaskStateCompleted)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(b) != `"completed"` {
		t.Fatalf("marshal: got %s, want \"completed\"", b)
	}
}

func TestTaskStateV1Name(t *testing.T) {
	cases := []struct {
		in   TaskState
		want string
	}{
		{TaskStateWorking, "TASK_STATE_WORKING"},
		{TaskStateCompleted, "TASK_STATE_COMPLETED"},
		{TaskStateInputRequired, "TASK_STATE_INPUT_REQUIRED"},
		{TaskState("brand-new-state"), "brand-new-state"},
	}
	for _, tc := range cases {
		if got := tc.in.V1Name(); got != tc.want {
			t.Errorf("V1Name(%q)=%q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestIsJSONMedia(t *testing.T) {
	cases := []struct {
		ct   string
		want bool
	}{
		{"application/json", true},
		{"application/json; charset=utf-8", true},
		{"application/a2a+json", true},
		{"application/a2a+json; charset=utf-8", true},
		{"APPLICATION/A2A+JSON", true},
		{"text/event-stream", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := isJSONMedia(tc.ct); got != tc.want {
			t.Errorf("isJSONMedia(%q)=%v, want %v", tc.ct, got, tc.want)
		}
	}
}

func TestRequestContentType(t *testing.T) {
	// No card discovered yet: legacy default.
	c := NewClient("http://127.0.0.1:1", "k")
	if got := c.requestContentType(); got != "application/json" {
		t.Fatalf("no card: got %q", got)
	}
	// v1.0 card: a2a+json.
	c.card.Store(&AgentCard{ProtocolReversion: "1.0"})
	if got := c.requestContentType(); got != "application/a2a+json" {
		t.Fatalf("v1 card: got %q", got)
	}
	// Legacy card (0.2.x, no protocolVersion): application/json.
	c.card.Store(&AgentCard{})
	if got := c.requestContentType(); got != "application/json" {
		t.Fatalf("legacy card: got %q", got)
	}
}

// Discover must fall back to the legacy agent.json well-known URI when a
// v1 server serves only agent-card.json (0.3.0 rename) — and vice versa.
func TestDiscoverWellKnownFallback(t *testing.T) {
	v1Card := `{"name":"v1","protocolVersion":"1.0","url":"http://x"}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/.well-known/agent-card.json" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/a2a+json")
		w.Write([]byte(v1Card))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "k")
	card, err := c.Discover(context.Background())
	if err != nil {
		t.Fatalf("discover (card.json only): %v", err)
	}
	if card.ProtocolReversion != "1.0" {
		t.Fatalf("card protocolVersion=%q", card.ProtocolReversion)
	}
	if got := c.requestContentType(); got != "application/a2a+json" {
		t.Fatalf("after v1 discover, request CT=%q", got)
	}

	legacyCard := `{"name":"legacy","url":"http://x"}`
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/.well-known/agent.json" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(legacyCard))
	}))
	defer srv2.Close()

	c2 := NewClient(srv2.URL, "k")
	if _, err := c2.Discover(context.Background()); err != nil {
		t.Fatalf("discover (agent.json only): %v", err)
	}
	if got := c2.requestContentType(); got != "application/json" {
		t.Fatalf("after legacy discover, request CT=%q", got)
	}
}

func TestPrefersA2AJSON(t *testing.T) {
	cases := []struct {
		ct, accept string
		want       bool
	}{
		{"application/a2a+json", "", true},
		{"application/json", "application/a2a+json, application/json", true},
		{"application/json", "application/json", false},
		{"", "", false},
	}
	for i, tc := range cases {
		r := httptest.NewRequest(http.MethodPost, "/", nil)
		if tc.ct != "" {
			r.Header.Set("Content-Type", tc.ct)
		}
		if tc.accept != "" {
			r.Header.Set("Accept", tc.accept)
		}
		if got := prefersA2AJSON(r); got != tc.want {
			t.Errorf("case %d: prefersA2AJSON=%v, want %v", i, got, tc.want)
		}
	}
}

func TestWriteRPCContentTypeNegotiation(t *testing.T) {
	// Legacy request path: plain recorder keeps application/json.
	rec := httptest.NewRecorder()
	writeRPCResult(rec, json.RawMessage(`1`), map[string]string{"ok": "true"})
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("legacy result CT=%q", ct)
	}

	// Negotiated request path: rpcWriter emits a2a+json.
	rec2 := httptest.NewRecorder()
	nw := &rpcWriter{ResponseWriter: rec2, respCT: "application/a2a+json"}
	writeRPCResult(nw, json.RawMessage(`1`), map[string]string{"ok": "true"})
	if ct := rec2.Header().Get("Content-Type"); ct != "application/a2a+json" {
		t.Fatalf("negotiated result CT=%q", ct)
	}
	if !strings.Contains(rec2.Body.String(), `"jsonrpc":"2.0"`) {
		t.Fatalf("negotiated body=%q", rec2.Body.String())
	}

	rec3 := httptest.NewRecorder()
	nw3 := &rpcWriter{ResponseWriter: rec3, respCT: "application/a2a+json"}
	writeRPCError(nw3, json.RawMessage(`1`), ErrTaskNotFound)
	if ct := rec3.Header().Get("Content-Type"); ct != "application/a2a+json" {
		t.Fatalf("negotiated error CT=%q", ct)
	}
}
