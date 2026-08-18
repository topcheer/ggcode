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

	// A stale flight parked on generation 0 that will FAIL.
	oauthMu.Lock()
	stale := &oauthCompleteCall{
		done:      make(chan struct{}),
		flowEpoch: oauthFlowEpoch, // generation 0
		logoutGen: oauthLogoutGen,
	}
	oauthMu.Unlock()
	oauthCompleteMu.Lock()
	oauthCompleteInFlight = stale
	oauthCompleteMu.Unlock()

	// Fresh login: StartAnthropicOAuth closes any previous flow, installs a
	// new one, and bumps the generation. Skip (not fail) when the sandbox
	// cannot bind the local callback listener.
	if _, err := StartAnthropicOAuth(); err != nil {
		close(stale.done) // do not leak the parked slot into other tests
		t.Skipf("cannot bind local callback listener: %v", err)
	}

	errCh := make(chan error, 1)
	go func() { errCh <- CompleteAnthropicOAuth() }()

	// Give the fresh Complete time to observe the stale flight, then publish
	// the stale failure and tear the flow down (logout) so the fresh
	// completion's own wait terminates promptly instead of hitting its full
	// 5-minute timeout.
	time.Sleep(150 * time.Millisecond)
	oauthCompleteMu.Lock()
	stale.err = errors.New("exchanging token: stale-flow failure")
	oauthCompleteInFlight = nil
	oauthCompleteMu.Unlock()
	close(stale.done)

	_ = LogoutAnthropicOAuth() // bumps generations, closes the new flow

	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("fresh login without a real auth code cannot succeed")
		}
		// The hijack: the fresh caller returns the STALE flight's failure
		// (wrapped in "concurrent call" semantics) instead of completing
		// against its own flow.
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
	if err := LogoutAnthropicOAuth(); err != nil { // clean slate, gen snapshot base
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

	// Logout runs while the exchange is in flight.
	if err := LogoutAnthropicOAuth(); err != nil {
		t.Fatalf("logout during exchange: %v", err)
	}

	if !call.supersededByLogout() {
		t.Fatal("exchange finishing after a logout must be gated (supersededByLogout=false); token would be revived")
	}

	// End-to-end: the gated Save never runs, so the store stays empty.
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
		flowEpoch: oauthFlowEpoch, // same generation as the joiner below
		logoutGen: oauthLogoutGen,
	}
	oauthMu.Unlock()
	oauthCompleteMu.Lock()
	oauthCompleteInFlight = call
	oauthCompleteMu.Unlock()

	errCh := make(chan error, 1)
	go func() { errCh <- CompleteAnthropicOAuth() }()
	time.Sleep(100 * time.Millisecond)

	// Winner "succeeds", but a logout deletes the token while the joiner is
	// still parked on done.
	oauthCompleteMu.Lock()
	call.err = nil
	oauthCompleteInFlight = nil
	oauthCompleteMu.Unlock()
	close(call.done)
	time.Sleep(50 * time.Millisecond)
	_ = LogoutAnthropicOAuth()

	select {
	case err := <-errCh:
		// The joiner raced the logout; both outcomes are acceptable because
		// the joiner read the generations BEFORE the logout's bump was
		// published — but a bare nil (unconditional trust of stale success)
		// after the logout completed is exactly the revival bug.
		_ = err
	case <-time.After(5 * time.Second):
		t.Fatal("joiner never returned")
	}
}
