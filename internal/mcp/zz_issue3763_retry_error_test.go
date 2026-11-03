package mcp

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

				return nil, &scriptError{
					code: ErrorCodeUnsupportedProtocolVersion,
					msg:  "Requested protocol version does not match supported versions",
					data: map[string]any{
						"requested": ProtocolVersion20260728,
						"supported": []string{latestMCPProtocolVersion},
					},
				}
			}

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

	if !strings.Contains(err.Error(), "crashed mid-retry") {
		t.Errorf("error lost the real retry cause: %v", err)
	}
	if !strings.Contains(err.Error(), "retry after -32022") {
		t.Errorf("error lacks the negotiation-context prefix: %v", err)
	}

	if strings.Contains(err.Error(), "method not found: initialize") {
		t.Errorf("Initialize fell back to legacy handshake against a modern server: %v", err)
	}

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
