package tunnel

import (
	"encoding/json"
	"strings"
	"testing"
)

// #1822 case 3: omitempty expiries must not silently pass validation
// (zero time = ambiguous IsZero semantics). Pins the parse-level shape:
// an expiry-less descriptor decodes to zero times with NO parse error,
// which is exactly the state the new validation guard rejects.
func Test1822OmittedExpiryParsesToZero(t *testing.T) {
	payload := `{"protocol_version":4,"room_id":"r","server_auth_ticket":"s","client_auth_ticket":"c"}`
	var issued relayIssuedShareSessionResponse
	if err := json.NewDecoder(strings.NewReader(payload)).Decode(&issued); err != nil {
		t.Fatal(err)
	}
	auth, err1 := parseShareTimestamp(issued.AuthExpiresAt)
	renew, err2 := parseShareTimestamp(issued.RenewExpiresAt)
	if err1 != nil || err2 != nil {
		t.Fatalf("parse errors: %v %v", err1, err2)
	}
	if !auth.IsZero() || !renew.IsZero() {
		t.Fatal("omitted expiries must decode to zero times (the state validation now rejects)")
	}
	// Well-formed timestamps still parse.
	var good relayIssuedShareSessionResponse
	payload2 := `{"protocol_version":4,"auth_expires_at":"2026-01-02T15:04:05Z","renew_expires_at":"2026-01-09T15:04:05Z"}`
	if err := json.NewDecoder(strings.NewReader(payload2)).Decode(&good); err != nil {
		t.Fatal(err)
	}
	ga, err := parseShareTimestamp(good.AuthExpiresAt)
	gr, err2 := parseShareTimestamp(good.RenewExpiresAt)
	if err != nil || err2 != nil || ga.IsZero() || gr.IsZero() {
		t.Fatalf("valid expiries must parse non-zero: %v %v %v %v", ga, gr, err, err2)
	}
}
