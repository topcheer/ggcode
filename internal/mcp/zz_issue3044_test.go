package mcp

import (
	"errors"
	"fmt"
	"testing"
)

// #3044-V1: a server-authored JSON-RPC error whose message contains infra
// substrings ("server error", "deadline exceeded") is a SEMANTIC error and
// must not be classified as infrastructure — three of them must not open
// the breaker on a healthy server.
func TestIssue3044_V1_JSONRPCErrorNotInfra(t *testing.T) {
	rpcErr := &Error{Code: -32603, Message: "Internal error: upstream server error"}
	wrapped := fmt.Errorf("mcp[srv]: tools/call: %w", rpcErr)
	if isInfraError(wrapped) {
		t.Fatal("JSON-RPC semantic error with infra-looking text must not classify as infra")
	}
	// decorateSpecError-wrapped form (the production path) must also be exempt.
	decorated := decorateSpecError(fmt.Errorf("mcp[srv]: tools/call: %w", rpcErr))
	if decorated != nil && isInfraError(decorated) {
		t.Fatal("decorated JSON-RPC error must not classify as infra")
	}
	// Sanity: a genuine transport error still classifies as infra.
	if !isInfraError(errors.New("dial tcp: connection refused")) {
		t.Fatal("transport error must still classify as infra")
	}
}

// #3044-V1 acceptance (as specified in the issue): three JSON-RPC errors
// containing "server error" must leave the breaker closed.
func TestIssue3044_V1_ThreeSemanticErrorsDoNotOpenBreaker(t *testing.T) {
	b := newServerBreaker("semantic")
	for i := 0; i < 3; i++ {
		err := fmt.Errorf("mcp[srv]: tools/call: %w",
			&Error{Code: -32603, Message: "Internal error: upstream server error"})
		// adapter.go:314 equivalent: only infra failures reach recordFailure.
		if isInfraError(err) {
			b.recordFailure(err)
		} else {
			b.recordSuccess()
		}
	}
	if blocked, _ := b.gate(); blocked {
		t.Fatalf("breaker must stay closed after 3 semantic errors")
	}
}

// #3044-V2: a flooding server (elicitation/create without complete) must
// not grow the pending set without bound.
func TestIssue3044_V2_PendingURLElicitationsBounded(t *testing.T) {
	c := &Client{name: "flood"}
	for i := 0; i < pendingURLElicitationsCap+50; i++ {
		c.trackURLElicitation(fmt.Sprintf("el-%d", i))
	}
	c.mu.Lock()
	n := len(c.pendingURLElicitations)
	c.mu.Unlock()
	if n > pendingURLElicitationsCap {
		t.Fatalf("pending set must stay bounded at %d, grew to %d", pendingURLElicitationsCap, n)
	}
}

// #3044-V3: result and error are mutually exclusive; both present is
// malformed and must be rejected, not silently routed to the result branch.
func TestIssue3044_V3_ResultAndErrorRejected(t *testing.T) {
	_, err := ParseMessage([]byte(`{"jsonrpc":"2.0","id":1,"result":{},"error":{"code":-32600,"message":"x"}}`))
	if err == nil {
		t.Fatal("message with both result and error must be rejected")
	}
}

// #3044-V3: a JSON null id carries no correlation value — treat as
// Notification, not Request.
func TestIssue3044_V3_NullIDIsNotification(t *testing.T) {
	msg, err := ParseMessage([]byte(`{"jsonrpc":"2.0","id":null,"method":"notifications/initialized"}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, isReq := msg.(*Request); isReq {
		t.Fatal("null id must be parsed as Notification, not Request")
	}
	if _, isNotif := msg.(*Notification); !isNotif {
		t.Fatalf("expected Notification, got %T", msg)
	}
}
