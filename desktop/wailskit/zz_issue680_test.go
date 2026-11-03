package wailskit

import (
	"strings"
	"testing"
	"time"
)

// TestIssue680_JoinerRereadsLogoutGenAfterWake verifies defect 2: a joiner
// parked on a same-generation flight must compare the flight's logoutGen
// snapshot against the CURRENT logout generation read AFTER waking — not
// against its own pre-park snapshot. Pre-fix, both snapshots predated the
// parking, so a logout that ran between done-close and the joiner's wakeup
// went unnoticed and the joiner returned nil ("connected") for a token that
// had just been deleted.
//
// Determinism: the test holds oauthMu across close(done) and the generation
// bump, so the joiner — which reads oauthLogoutGen under that same mutex
// after waking — cannot observe anything but the bumped value.
func TestIssue680_JoinerRereadsLogoutGenAfterWake(t *testing.T) {
	resetOAuthState(t)
	isolateAuthStore(t)

	oauthMu.Lock()
	call := &oauthCompleteCall{
		done:      make(chan struct{}),
		flowEpoch: oauthFlowEpoch,
		logoutGen: oauthLogoutGen,
	}
	oauthMu.Unlock()
	oauthCompleteMu.Lock()
	oauthCompleteInFlight = call
	oauthCompleteMu.Unlock()

	errCh := make(chan error, 1)
	go func() { errCh <- CompleteAnthropicOAuth() }()
	time.Sleep(100 * time.Millisecond)

	oauthCompleteMu.Lock()
	call.err = nil
	oauthCompleteInFlight = nil
	oauthCompleteMu.Unlock()

	oauthMu.Lock()
	close(call.done)
	oauthLogoutGen++
	oauthMu.Unlock()

	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("#680: joiner reported success for a token deleted by a logout that ran before its post-wakeup generation read — pre-park snapshot comparison persists")
		}
		if !strings.Contains(err.Error(), "concurrent logout") {
			t.Fatalf("expected concurrent-logout error, got: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("joiner never returned")
	}
}

// TestIssue680_ReloginDuringInflightCompletionKeepsNewFlowUsable verifies the
// user-visible contract of defect 1: when the user re-logins while an older
// completion is still in flight, the old completion's teardown must not take
// down the NEW flow — a fresh Complete against the new flow must reach the
// real auth-code wait (terminated here by logout), never the misleading
// "no OAuth flow in progress" error while its token path is actually alive.
func TestIssue680_ReloginDuringInflightCompletionKeepsNewFlowUsable(t *testing.T) {
	resetOAuthState(t)
	isolateAuthStore(t)

	if _, err := StartAnthropicOAuth(); err != nil {
		t.Skipf("cannot bind local callback listener: %v", err)
	}
	oauthMu.Lock()
	flowA := currentOAuthFlow
	oauthMu.Unlock()

	winnerErr := make(chan error, 1)
	go func() { winnerErr <- CompleteAnthropicOAuth() }()
	time.Sleep(100 * time.Millisecond)

	if _, err := StartAnthropicOAuth(); err != nil {
		t.Skipf("cannot bind local callback listener: %v", err)
	}
	oauthMu.Lock()
	flowB := currentOAuthFlow
	oauthMu.Unlock()

	select {
	case err := <-winnerErr:
		if err == nil {
			t.Fatal("winner without a real auth code cannot succeed")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("winner never returned after its flow was closed")
	}

	oauthMu.Lock()
	cur := currentOAuthFlow
	oauthMu.Unlock()
	if cur == nil {
		t.Fatal("#680: new flow was cleared by the old completion's teardown; a fresh login's Complete would now report \"no OAuth flow in progress\"")
	}
	if cur != flowB || cur == flowA {
		t.Fatal("#680: current flow must still be the re-login's flow B")
	}

	freshErr := make(chan error, 1)
	go func() { freshErr <- CompleteAnthropicOAuth() }()
	time.Sleep(100 * time.Millisecond)
	if err := LogoutAnthropicOAuth(); err != nil {
		t.Fatalf("logout: %v", err)
	}
	select {
	case err := <-freshErr:
		if err == nil {
			t.Fatal("fresh completion without a real auth code cannot succeed")
		}
		if strings.Contains(err.Error(), "no OAuth flow in progress") {
			t.Fatalf("#680: fresh login's Complete hit the misleading no-flow error (stale winner's teardown consumed its flow): %v", err)
		}
		if !strings.Contains(err.Error(), "waiting for auth code") {
			t.Fatalf("fresh completion should fail from the real auth-code wait (closed by logout), got: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("fresh Complete never returned")
	}
}
