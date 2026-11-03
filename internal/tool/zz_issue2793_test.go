package tool

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

func TestIssue2793_DialContextGuardPinned(t *testing.T) {
	src := issue2793ReadSource(t)
	if !strings.Contains(src, "resolvePublicDialAddress(dialCtx, host, port, net.DefaultResolver.LookupIPAddr)") {
		t.Fatal("#2793: openSkillSource no longer routes dials through resolvePublicDialAddress - the rebinding TOCTOU and redirect-dial gap would reopen")
	}
	if !strings.Contains(src, "base.DialContext = func(") {
		t.Fatal("#2793: the custom DialContext install is gone")
	}
}

func TestIssue2793_PrecheckFailsClosed(t *testing.T) {
	src := issue2793ReadSource(t)
	if strings.Contains(src, "lerr == nil") {
		t.Fatal("#2793: fail-open pre-check pattern (`lerr == nil` guard) is back - SERVFAIL would bypass the guard")
	}
	if !strings.Contains(src, "cannot resolve skill import host") {
		t.Fatal("#2793: the fail-closed lookup-error rejection branch is missing")
	}
}

func TestIssue2793_RedirectGuardResolvesDNS(t *testing.T) {
	src := issue2793ReadSource(t)
	if !strings.Contains(src, "rejectIfHostResolvesPrivate(req.Context(), req.URL.Hostname(), net.DefaultResolver.LookupIPAddr)") {
		t.Fatal("#2793: CheckRedirect no longer re-resolves redirect targets - a domain pointing at private IP space would pass the literal check")
	}
}
