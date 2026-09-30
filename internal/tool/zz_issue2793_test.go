package tool

// #2793 regression probes (source-level invariants): the SSRF scenario
// (public DNS bouncing to a rebinding A record, SERVFAIL on the pre-check)
// cannot be reproduced deterministically in a unit test without a
// controllable resolver. Following the repo's #2781/#2775 precedent, these
// probes pin the guard structure at source level: if any of the three
// guards is removed or weakened, the corresponding assertion fails.

import (
	"os"
	"strings"
	"testing"
)

const issue2793Source = "skill_portable.go"

func issue2793ReadSource(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(issue2793Source)
	if err != nil {
		t.Skipf("source layout changed, cannot read %s: %v", issue2793Source, err)
	}
	return string(b)
}

// Guard 1: the dial-level guard must exist - a custom DialContext that
// resolves once via resolvePublicDialAddress (fail-closed: lookup errors
// reject, and every redirect hop dials through the same check).
func TestIssue2793_DialContextGuardPinned(t *testing.T) {
	src := issue2793ReadSource(t)
	if !strings.Contains(src, "resolvePublicDialAddress(dialCtx, host, port, net.DefaultResolver.LookupIPAddr)") {
		t.Fatal("#2793: openSkillSource no longer routes dials through resolvePublicDialAddress - the rebinding TOCTOU and redirect-dial gap would reopen")
	}
	if !strings.Contains(src, "base.DialContext = func(") {
		t.Fatal("#2793: the custom DialContext install is gone")
	}
}

// Guard 2: the pre-check must fail CLOSED - a lookup error refuses the
// import instead of silently falling through to the client's own
// resolution.
func TestIssue2793_PrecheckFailsClosed(t *testing.T) {
	src := issue2793ReadSource(t)
	if strings.Contains(src, "lerr == nil") {
		t.Fatal("#2793: fail-open pre-check pattern (`lerr == nil` guard) is back - SERVFAIL would bypass the guard")
	}
	if !strings.Contains(src, "cannot resolve skill import host") {
		t.Fatal("#2793: the fail-closed lookup-error rejection branch is missing")
	}
}

// Guard 3: the redirect hook must re-resolve the target host, not just
// match literal private names.
func TestIssue2793_RedirectGuardResolvesDNS(t *testing.T) {
	src := issue2793ReadSource(t)
	if !strings.Contains(src, "rejectIfHostResolvesPrivate(req.Context(), req.URL.Hostname(), net.DefaultResolver.LookupIPAddr)") {
		t.Fatal("#2793: CheckRedirect no longer re-resolves redirect targets - a domain pointing at private IP space would pass the literal check")
	}
}
