//go:build goolm

package agentruntime

// #4412 probes, source-pinned (the graceful-stop path needs a live relay
// websocket + mobile client; a behavioral harness is disproportionate -
// same disclosure as the #3945/#4409 probes):
//  1. Close must call StopSharingGracefully BEFORE session.Stop() - the
//     sharing_stopped event is DELIVERED over the relay websocket, so
//     tearing the session down first made the event unsendable (full 2s
//     block, mobile never notified).
//  2. rollover must claim needsFinalize ATOMICALLY with the read
//     (claim-and-clear in one lock window) - the old check-act spanned
//     two windows and two concurrent publishers double-fired PushTextDone.

import (
	"os"
	"strings"
	"testing"
)

func TestIssue4412_CloseStopsShareBeforeSession(t *testing.T) {
	raw, err := os.ReadFile("tunnel_host.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(raw)
	// Restrict to the Close body: ref.session.Stop() also appears in an
	// unrelated earlier path.
	closeAt := strings.Index(src, "func (h *TunnelHost) Close()")
	if closeAt < 0 {
		t.Fatal("Close missing")
	}
	window := src[closeAt:]
	if len(window) > 4000 {
		window = window[:4000]
	}
	grace := strings.Index(window, "online.StopSharingGracefully(2 * time.Second)")
	sess := strings.Index(window, "ref.session.Stop()")
	if grace < 0 || sess < 0 {
		t.Fatalf("both calls must exist in Close, grace=%d sess=%d (#4412)", grace, sess)
	}
	if grace > sess {
		t.Fatalf("StopSharingGracefully must come BEFORE session.Stop() so sharing_stopped can be delivered over the relay websocket (#4412)")
	}
}

func TestIssue4412_RolloverClaimsFinalizeAtomically(t *testing.T) {
	raw, err := os.ReadFile("tunnel_host.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(raw)
	i := strings.Index(src, "func (h *TunnelHost) rollover(")
	if i < 0 {
		t.Fatal("rollover missing")
	}
	body := src[i : i+1400]
	head := body[:strings.Index(body, "if finalize {")]
	// The claim window: read of needsFinalize and the clear must sit in
	// the SAME lock window (before the first Unlock).
	if !strings.Contains(head, "finalize := h.needsFinalize") ||
		!strings.Contains(head, "h.needsFinalize = false") {
		t.Fatalf("needsFinalize must be claimed-and-cleared in one lock window, head:\n%s (#4412)", head)
	}
	if strings.Count(body, "h.needsFinalize = false") != 1 {
		t.Fatalf("the clear must live only in the claim window (no second clear after the push), body:\n%s (#4412)", body)
	}
}
