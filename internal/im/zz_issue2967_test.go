package im

import (
	"sync"
	"testing"
)

// zz_issue2967_test.go - regression probes for #2967: pcAdapter.Send looked
// up ONLY binding.TargetID in the sessions map. Pairing-built bindings carry
// TargetID = appId (ReplyBinding prefers SenderID) while the map is keyed by
// the invite SessionID (= ChannelID), so every post-pairing reply
// deterministically failed with "session not found" - the pairing feature
// was effectively dead for PC. The nostr adapter already implements the
// ChannelID fallback this pins.

func TestIssue2967ChannelIDFallbackResolves(t *testing.T) {
	var sessions sync.Map
	sessions.Store("sess-1", &pcSession{})                             // keyed by invite SessionID
	binding := ChannelBinding{TargetID: "app-77", ChannelID: "sess-1"} // pairing shape
	if got := resolvePCSessionID(&sessions, binding); got != "sess-1" {
		t.Fatalf("#2967: fallback failed, got %q want sess-1 (app reply routing stays broken)", got)
	}
}

func TestIssue2967DirectTargetIDHitUnchanged(t *testing.T) {
	var sessions sync.Map
	sessions.Store("sess-1", &pcSession{})
	binding := ChannelBinding{TargetID: "sess-1", ChannelID: "sess-9"}
	if got := resolvePCSessionID(&sessions, binding); got != "sess-1" {
		t.Fatalf("#2967: direct TargetID hit must stay preferred, got %q", got)
	}
}

func TestIssue2967BothMissReturnsOriginal(t *testing.T) {
	var sessions sync.Map
	sessions.Store("other", &pcSession{})
	binding := ChannelBinding{TargetID: "app-77", ChannelID: "sess-gone"}
	// No fabricated success: the original TargetID is returned so Send
	// reports the accurate "session app-77 not found" error.
	if got := resolvePCSessionID(&sessions, binding); got != "app-77" {
		t.Fatalf("#2967: both-miss must return original for accurate error, got %q", got)
	}
}

func TestIssue2967EmptyBindingReturnsEmpty(t *testing.T) {
	var sessions sync.Map
	if got := resolvePCSessionID(&sessions, ChannelBinding{}); got != "" {
		t.Fatalf("#2967: empty binding must resolve empty, got %q", got)
	}
}
