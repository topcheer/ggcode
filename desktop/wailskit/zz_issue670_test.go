package wailskit

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/auth"
)

// resetOAuthState clears both OAuth globals after each test so a leaked
// in-flight flow or completion slot cannot poison later tests.
func resetOAuthState(t *testing.T) {
	t.Cleanup(func() {
		oauthMu.Lock()
		if currentOAuthFlow != nil {
			currentOAuthFlow.Close()
			currentOAuthFlow = nil
		}
		oauthMu.Unlock()
		oauthCompleteMu.Lock()
		if oauthCompleteInFlight != nil {
			<-oauthCompleteInFlight.done // should already be closed
			oauthCompleteInFlight = nil
		}
		oauthCompleteMu.Unlock()
	})
}

// isolateAuthStore points the auth store at a temp HOME so Save/Delete
// never touch the developer's real provider_auth.json.
func isolateAuthStore(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
}

// TestIssue670_CompleteSingleFlight_LoserInheritsSuccess verifies the core
// of defect 2: when a concurrent completion already saved the token
// successfully, a second caller (double-click / event replay) must return
// nil — NOT a misleading "waiting for auth code" failure that would send
// the user through a pointless re-auth even though login worked.
func TestIssue670_CompleteSingleFlight_LoserInheritsSuccess(t *testing.T) {
	resetOAuthState(t)
	isolateAuthStore(t)

	// Simulate an in-flight winner without any real flow: the loser path
	// must not touch currentOAuthFlow at all (leave it nil — a loser that
	// read the flow here would be racy by construction).
	call := &oauthCompleteCall{done: make(chan struct{})}
	oauthCompleteMu.Lock()
	oauthCompleteInFlight = call
	oauthCompleteMu.Unlock()

	errCh := make(chan error, 1)
	go func() { errCh <- CompleteAnthropicOAuth() }()

	// Let the loser block on done, then publish the winner's success.
	time.Sleep(100 * time.Millisecond)
	oauthCompleteMu.Lock()
	call.err = nil
	oauthCompleteInFlight = nil
	oauthCompleteMu.Unlock()
	close(call.done)

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("loser must inherit success (nil), got misleading error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("loser did not return; single-flight join is leaking")
	}
}

// TestIssue670_CompleteSingleFlight_LoserInheritsFailure: when the in-flight
// winner failed, the loser receives the shared outcome wrapped with the
// "concurrent call" semantics instead of inventing its own error.
func TestIssue670_CompleteSingleFlight_LoserInheritsFailure(t *testing.T) {
	resetOAuthState(t)
	isolateAuthStore(t)

	call := &oauthCompleteCall{done: make(chan struct{})}
	oauthCompleteMu.Lock()
	oauthCompleteInFlight = call
	oauthCompleteMu.Unlock()

	errCh := make(chan error, 1)
	go func() { errCh <- CompleteAnthropicOAuth() }()

	time.Sleep(100 * time.Millisecond)
	oauthCompleteMu.Lock()
	call.err = errors.New("exchanging token: boom")
	oauthCompleteInFlight = nil
	oauthCompleteMu.Unlock()
	close(call.done)

	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("expected inherited failure, got nil")
		}
		if !strings.Contains(err.Error(), "concurrent call") {
			t.Fatalf("expected concurrent-call semantics in error, got: %v", err)
		}
		if !strings.Contains(err.Error(), "boom") {
			t.Fatalf("expected underlying winner error propagated, got: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("loser did not return; single-flight join is leaking")
	}
}

// TestIssue670_CompleteSingleFlight_BothCallersUnblockPromptly reproduces the
// pre-fix hang: two waiters raced the flow's capacity-1 callback channel, so
// after the flow closed only ONE received the cancellation — the other
// blocked for its full 5-minute context timeout. With single-flight exactly
// one caller waits on the flow and the other joins it, so both must return
// promptly once the flow is torn down (via Logout, which also covers #670's
// in-flight-flow cleanup).
func TestIssue670_CompleteSingleFlight_BothCallersUnblockPromptly(t *testing.T) {
	resetOAuthState(t)
	isolateAuthStore(t)

	if _, err := StartAnthropicOAuth(); err != nil {
		t.Skipf("cannot bind local callback listener in this environment: %v", err)
	}

	const callers = 2
	errCh := make(chan error, callers)
	var wg sync.WaitGroup
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errCh <- CompleteAnthropicOAuth()
		}()
		// Give the first caller time to take the winner slot so the second
		// deterministically exercises the joiner path.
		time.Sleep(100 * time.Millisecond)
	}

	// Small settle window, then tear the flow down (logout path).
	time.Sleep(100 * time.Millisecond)
	if err := LogoutAnthropicOAuth(); err != nil {
		t.Fatalf("logout failed: %v", err)
	}

	results := make([]error, 0, callers)
	for i := 0; i < callers; i++ {
		select {
		case err := <-errCh:
			// Both callers fail (no real auth code was ever exchanged) —
			// the assertion is that they RETURN at all, and quickly.
			if err == nil {
				t.Fatal("unexpected success without a real auth code")
			}
			results = append(results, err)
		case <-time.After(5 * time.Second):
			t.Fatalf("caller %d still blocked ~5s after flow teardown; pre-#670 loser hang persists", i)
		}
	}
	wg.Wait()

	// Exactly one caller observed the raw flow error ("oauth flow closed"
	// wrapped in "waiting for auth code"); the other joined and saw the
	// concurrent-call wrap of the same outcome. A third state — an
	// independent unrelated error invented by a racy loser — is forbidden.
	joined, raw := 0, 0
	for _, err := range results {
		switch {
		case strings.Contains(err.Error(), "concurrent call"):
			joined++
		case strings.Contains(err.Error(), "waiting for auth code"):
			raw++
		default:
			t.Fatalf("unexpected error shape not derived from the shared outcome: %v", err)
		}
	}
	if joined != 1 || raw != 1 {
		t.Fatalf("expected exactly one winner (raw flow error) and one joiner (concurrent-call wrap), got joined=%d raw=%d: %v", joined, raw, results)
	}
}

// TestIssue670_LogoutClearsTokenFlowAndStatus verifies defect 1's end state:
// after logout the disk token is gone, no in-flight flow remains, and the
// status probe reports disconnected — the UI/memory split (UI shows logged
// out while chats keep using the deleted token) required the symmetric
// provider refresh added in LogoutAnthropicOAuth.
func TestIssue670_LogoutClearsTokenFlowAndStatus(t *testing.T) {
	resetOAuthState(t)
	isolateAuthStore(t)

	store := auth.DefaultStore()
	if err := store.Save(&auth.Info{
		ProviderID:   auth.ProviderAnthropic,
		Type:         "oauth",
		AccessToken:  "stale-access-token",
		RefreshToken: "stale-refresh-token",
		ExpiresAt:    time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatalf("seeding store: %v", err)
	}
	if !AnthropicOAuthStatus() {
		t.Fatal("precondition: seeded token should report connected")
	}

	if _, err := StartAnthropicOAuth(); err != nil {
		t.Skipf("cannot bind local callback listener in this environment: %v", err)
	}
	oauthMu.Lock()
	flowBefore := currentOAuthFlow
	oauthMu.Unlock()
	if flowBefore == nil {
		t.Fatal("precondition: StartAnthropicOAuth should set currentOAuthFlow")
	}

	if err := LogoutAnthropicOAuth(); err != nil {
		t.Fatalf("logout: %v", err)
	}

	if info, err := store.Load(auth.ProviderAnthropic); err == nil && info != nil {
		t.Fatalf("disk token must be deleted, still present: %+v", info)
	}
	oauthMu.Lock()
	flowAfter := currentOAuthFlow
	oauthMu.Unlock()
	if flowAfter != nil {
		t.Fatal("in-flight OAuth flow must be cleared by logout (#670)")
	}
	if AnthropicOAuthStatus() {
		t.Fatal("status must report disconnected after logout")
	}

	// Second logout is idempotent (no flow, no token).
	if err := LogoutAnthropicOAuth(); err != nil {
		t.Fatalf("second logout should be clean, got: %v", err)
	}
}
