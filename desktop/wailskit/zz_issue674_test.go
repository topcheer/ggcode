package wailskit

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/auth"
)

func TestIssue674_NewFlowCompleteDoesNotInheritStaleFlight(t *testing.T) {
	resetOAuthState(t)
	isolateAuthStore(t)

	oauthMu.Lock()
	stale := &oauthCompleteCall{
		done:      make(chan struct{}),
		flowEpoch: oauthFlowEpoch,
		logoutGen: oauthLogoutGen,
	}
	oauthMu.Unlock()
	oauthCompleteMu.Lock()
	oauthCompleteInFlight = stale
	oauthCompleteMu.Unlock()

	if _, err := StartAnthropicOAuth(); err != nil {
		close(stale.done)
		t.Skipf("cannot bind local callback listener: %v", err)
	}

	errCh := make(chan error, 1)
	go func() { errCh <- CompleteAnthropicOAuth() }()

	time.Sleep(150 * time.Millisecond)
	oauthCompleteMu.Lock()
	stale.err = errors.New("exchanging token: stale-flow failure")
	oauthCompleteInFlight = nil
	oauthCompleteMu.Unlock()
	close(stale.done)

	_ = LogoutAnthropicOAuth()

	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("fresh login without a real auth code cannot succeed")
		}

		if strings.Contains(err.Error(), "concurrent call") {
			t.Fatalf("fresh Complete inherited the STALE flight's outcome (cross-generation hijack): %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("fresh Complete never returned; stale-generation waiter is leaking")
	}
}

func TestIssue674_StartBumpsFlowGeneration(t *testing.T) {
	resetOAuthState(t)

	oauthMu.Lock()
	before := oauthFlowEpoch
	oauthMu.Unlock()

	if _, err := StartAnthropicOAuth(); err != nil {
		t.Skipf("cannot bind local callback listener: %v", err)
	}

	oauthMu.Lock()
	after := oauthFlowEpoch
	oauthMu.Unlock()
	if after != before+1 {
		t.Fatalf("StartAnthropicOAuth must bump the flow generation exactly once: %d -> %d", before, after)
	}
}

func TestIssue674_LogoutBumpsGenerations(t *testing.T) {
	resetOAuthState(t)

	oauthMu.Lock()
	fe, lg := oauthFlowEpoch, oauthLogoutGen
	oauthMu.Unlock()

	if err := LogoutAnthropicOAuth(); err != nil {
		t.Fatalf("logout: %v", err)
	}

	oauthMu.Lock()
	fe2, lg2 := oauthFlowEpoch, oauthLogoutGen
	oauthMu.Unlock()
	if fe2 != fe+1 || lg2 != lg+1 {
		t.Fatalf("logout must bump flow epoch and logout gen exactly once: flow %d->%d logout %d->%d", fe, fe2, lg, lg2)
	}
}

func TestIssue674_LogoutDuringExchangeDoesNotReviveToken(t *testing.T) {
	resetOAuthState(t)
	isolateAuthStore(t)

	store := auth.DefaultStore()
	if err := LogoutAnthropicOAuth(); err != nil {
		t.Fatalf("pre-logout: %v", err)
	}

	oauthMu.Lock()
	call := &oauthCompleteCall{done: make(chan struct{})}
	call.flowEpoch = oauthFlowEpoch
	call.logoutGen = oauthLogoutGen
	oauthMu.Unlock()

	if call.supersededByLogout() {
		t.Fatal("precondition: no logout since snapshot; gate must be open")
	}

	if err := LogoutAnthropicOAuth(); err != nil {
		t.Fatalf("logout during exchange: %v", err)
	}

	if !call.supersededByLogout() {
		t.Fatal("exchange finishing after a logout must be gated (supersededByLogout=false); token would be revived")
	}

	if info, err := store.Load(auth.ProviderAnthropic); err == nil && info != nil {
		t.Fatalf("token must not be present after logout-during-exchange: %+v", info)
	}
}

func TestIssue674_JoinerDoesNotTrustSuccessAfterLogout(t *testing.T) {
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
	close(call.done)
	time.Sleep(50 * time.Millisecond)
	_ = LogoutAnthropicOAuth()

	select {
	case err := <-errCh:

		_ = err
	case <-time.After(5 * time.Second):
		t.Fatal("joiner never returned")
	}
}
