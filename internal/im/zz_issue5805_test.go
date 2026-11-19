package im

import (
	"os"
	"strings"
	"testing"
)

// #5805: completing a rebind (workspace switched to a different channel)
// must reset the QQ adapter's gateway session/seq state so the next
// reconnect identifies from scratch instead of resuming with a stale seq.
// The stale-seq resume made the server-side cursor skip the gap between
// bindings: messages in the window satisfied no push condition and sat
// undelivered. This anchor pins the reset to the rebind completion path.
func TestIssue5805QQRebindResetsGatewaySession(t *testing.T) {
	b, err := os.ReadFile("qq_adapter.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	// The reset must live inside the pairing-consumed branch and gate on
	// PairingKindRebind (plain binds to the same context must keep session).
	if !strings.Contains(src, "pairingResult.Kind == PairingKindRebind") {
		t.Fatal("#5805: rebind completion must gate the gateway session reset on PairingKindRebind")
	}
	if !strings.Contains(src, "binding rebound; resetting gateway session for identify") {
		t.Fatal("#5805: rebind completion must reset sessionID/lastSeq and force identify (closeWS)")
	}
}

// #5805 (audit half): the resume decision at handleHello must remain
// "session present -> resume", with the ONLY reset paths being
// handleInvalidSession(!resumable) and the rebind reset pinned above.
// If session reset is added anywhere else, revisit this anchor.
func TestIssue5805QQSessionResetPaths(t *testing.T) {
	b, err := os.ReadFile("qq_adapter.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	resets := strings.Count(src, "a.sessionID = \"\"")
	if resets < 2 {
		t.Fatalf("#5805: expected >=2 sessionID reset sites (invalid-session + rebind), got %d", resets)
	}
	// Resume must fall back to identify when the session was cleared, so the
	// rebind reset actually converges on a fresh identify.
	if !strings.Contains(src, "if sid == \"\" {\n\t\treturn a.sendIdentify()") {
		t.Fatal("#5805: sendResume must fall back to sendIdentify when sessionID is empty")
	}
}
