package wailskit

import (
	"strings"
	"testing"
	"time"
)

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
