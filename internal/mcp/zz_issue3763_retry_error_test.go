package mcp

// #3763: the -32022 retry path in probeModern (discover.go) must NOT
// swallow the retry error. Negotiation succeeded (the server spoke -32022
// and a mutual version was picked from its payload) - a retry failure is
// a transport/lifecycle problem and must surface as such, not as the
// stale first UnsupportedProtocolVersionError that sends users chasing
// version phantoms while the server is simply dead.

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestRetryFailureAfterNegotiationSurfacesRealError(t *testing.T) {
	discoverCalls := 0
	srv := &scriptedServer{t: t, handler: func(method string, _ json.RawMessage) (any, *scriptError) {
		switch method {
		case "server/discover":
			discoverCalls++
			if discoverCalls == 1 {
				// A modern server rejecting the sent version but naming a
				// mutually supported one: negotiation succeeds and
				// probeModern schedules exactly one retry.
				return nil, &scriptError{
					code: ErrorCodeUnsupportedProtocolVersion,
					msg:  "Requested protocol version does not match supported versions",
					data: map[string]any{
						"requested": ProtocolVersion20260728,
						"supported": []string{latestMCPProtocolVersion},
					},
				}
			}
			// Retry: the server died mid-retry - a transport-class
			// internal error, nothing to do with version compatibility.
			return nil, &scriptError{code: -32603, msg: "server process crashed mid-retry"}
		default:
			return nil, &scriptError{code: ErrorCodeMethodNotFound, msg: "method not found: " + method}
		}
	}}
	client, cleanup := srv.start()
	client.EnableStateless()
	defer cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := client.Initialize(ctx)
	if err == nil {
		t.Fatal("Initialize succeeded; want the retry failure to surface")
	}
	// The real cause must be in the chain, not swallowed.
	if !strings.Contains(err.Error(), "crashed mid-retry") {
		t.Errorf("error lost the real retry cause: %v", err)
	}
	if !strings.Contains(err.Error(), "retry after -32022") {
		t.Errorf("error lacks the negotiation-context prefix: %v", err)
	}
	// Regression pins (pre-fix behavior):
	// 1) the stale first-attempt typed error was the WHOLE story; now the
	//    retry context and real cause lead it.
	// 2) Initialize must NOT fall back to the legacy handshake - the
	//    server already proved itself modern by speaking -32022; the
	//    fallback produced the misleading "initialize: method not found"
	//    chain the pre-fix test run exposed.
	if strings.Contains(err.Error(), "method not found: initialize") {
		t.Errorf("Initialize fell back to legacy handshake against a modern server: %v", err)
	}
	// The typed error stays in the chain (dual-era gating needs it) but
	// must not lead the diagnosis.
	var uerr *UnsupportedProtocolVersionError
	if !errors.As(err, &uerr) {
		t.Errorf("typed negotiation error dropped from chain (dual-era gating needs it): %v", err)
	}
	if strings.HasPrefix(err.Error(), "mcp[scripted]: tools/list") || strings.HasPrefix(err.Error(), "mcp: server does not support") {
		t.Errorf("stale version diagnostic still leads the error: %v", err)
	}
	if got := srv.callsOf("server/discover"); got != 2 {
		t.Errorf("server/discover attempts = %d, want exactly 2 (initial + one retry)", got)
	}
}
