package wailskit

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/auth"
)

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
			<-oauthCompleteInFlight.done
			oauthCompleteInFlight = nil
		}
		oauthCompleteMu.Unlock()
	})
}

func isolateAuthStore(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
}

func TestIssue670_CompleteSingleFlight_LoserInheritsSuccess(t *testing.T) {
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

		time.Sleep(100 * time.Millisecond)
	}

	time.Sleep(100 * time.Millisecond)
	if err := LogoutAnthropicOAuth(); err != nil {
		t.Fatalf("logout failed: %v", err)
	}

	results := make([]error, 0, callers)
	for i := 0; i < callers; i++ {
		select {
		case err := <-errCh:

			if err == nil {
				t.Fatal("unexpected success without a real auth code")
			}
			results = append(results, err)
		case <-time.After(5 * time.Second):
			t.Fatalf("caller %d still blocked ~5s after flow teardown; pre-#670 loser hang persists", i)
		}
	}
	wg.Wait()

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

	if err := LogoutAnthropicOAuth(); err != nil {
		t.Fatalf("second logout should be clean, got: %v", err)
	}
}
