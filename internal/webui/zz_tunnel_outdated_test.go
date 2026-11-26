package webui

import (
	"errors"
	"strings"
	"testing"

	etunnel "github.com/topcheer/etunnel/sdk"
)

// The server's client_outdated message (verbatim, with the etunnel upgrade
// URL) must reach the user, followed by the ggcode-specific remedy; the
// wrapped error must still unwrap to *etunnel.APIError.
func TestDescribeTunnelError_OutdatedKeepsServerTextAndAddsHint(t *testing.T) {
	server := &etunnel.APIError{Code: "client_outdated", Message: "etunnel client did not report a valid version; this server runs 1.7.50 and requires SDK >= 1.7.45. Upgrade: https://github.com/topcheer/etunnel"}
	got := describeTunnelError(server)
	msg := got.Error()
	if !strings.Contains(msg, server.Message) {
		t.Fatalf("server text must stay verbatim, got: %s", msg)
	}
	if !strings.Contains(msg, "upgrade ggcode") || !strings.Contains(msg, etunnel.Version) {
		t.Fatalf("ggcode hint with the embedded SDK version missing, got: %s", msg)
	}
	var apiErr *etunnel.APIError
	if !errors.As(got, &apiErr) || apiErr.Code != "client_outdated" {
		t.Fatalf("wrapped error must still unwrap to *etunnel.APIError, got %v", got)
	}
}

func TestDescribeTunnelError_OtherErrorsUntouched(t *testing.T) {
	other := &etunnel.APIError{Code: "subdomain_taken", Message: "taken"}
	if got := describeTunnelError(other); got != error(other) {
		t.Fatalf("non-outdated APIError must be returned as-is, got %v", got)
	}
	plain := errors.New("dial failed")
	if got := describeTunnelError(plain); got != plain {
		t.Fatalf("plain error must be returned as-is, got %v", got)
	}
}
