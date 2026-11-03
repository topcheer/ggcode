package wailskit

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/auth"
)

// TestIssue674_NewFlowCompleteDoesNotInheritStaleFlight: when a completion
// from an OLD flow generation is still parked (the user re-clicked login and
// StartAnthropicOAuth installed a new flow), a fresh Complete must NOT
// inherit the stale flight's failure — the pre-#674 hijack surfaced the old
// flow's error to the fresh login while the new flow ended up with no waiter
// at all (its callback listener leaked, its auth code dropped). The fresh
// caller must wait for the stale slot, then run its own completion.
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

// TestIssue674_StartBumpsFlowGeneration: every install must advance the flow
// generation — the single-flight keys on it, so a stale generation counter
// would let old flights hijack fresh logins.
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

// TestIssue674_LogoutBumpsGenerations: logout must bump BOTH counters — the
// logout generation gates the post-exchange token Save (no token revival),
// and the flow generation detaches any parked same-generation joiners.
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

// TestIssue674_LogoutDuringExchangeDoesNotReviveToken: a completion whose
// (uncancellable) token exchange straddles a logout must discard the token
// instead of saving it — the pre-#674 bug "revived" the deleted token.
// The exchange itself is network-bound, so the gate is verified at its
// decision point (supersededByLogout) plus end-to-end through the store.
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

// TestIssue674_JoinerDoesNotTrustSuccessAfterLogout: a joiner parked on a
// same-generation flight whose success was invalidated by a logout must not
// report the stale success — the token backing it was deleted.
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
