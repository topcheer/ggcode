package agent

// #3363 probes: the pseudo-version regex's 3-digit N-segment cap let
// base-tag pins with N>=1000 (commits since the base tag) fall through to
// semver comparison and resurrect the #3360 false-Critical. N has no spec
// upper bound; the timestamp segment remains the distinguishing mark.

import "testing"

func TestIssue3363_LargeNPseudoPinsNotFlagged(t *testing.T) {
	vuln := vulnEntry{maxSafeV: "0.31.0"}
	cases := []struct{ name, ver string }{
		{"N=999 boundary (was last to match)", "v0.30.0-999.20241220123456-abcdef123456"},
		{"N=1000 first escape", "v0.30.0-1000.20241220123456-abcdef123456"},
		{"N=3750 real-world sparse-tag", "v0.30.0-3750.20241220123456-abcdef123456"},
		{"no-base pseudo still skipped", "v0.0.0-20250101120000-abcdef123456"},
		{"+incompatible pseudo", "v2.0.0-20200302210943-78000ba7a073+incompatible"},
	}
	for _, c := range cases {
		if isVulnerableVersion(c.ver, vuln) {
			t.Fatalf("%s (%s): pseudo-version pin must not be flagged as a known Critical CVE", c.name, c.ver)
		}
	}
}

func TestIssue3363_NonPseudoNotExempted(t *testing.T) {
	vuln := vulnEntry{maxSafeV: "0.31.0"}
	// Prereleases of the vulnerable line stay vulnerable - the exemption
	// must not swallow anything lacking the 14-digit timestamp mark.
	if isGoPseudoVersion("v0.30.0-rc1") {
		t.Fatal("plain prerelease must not match the pseudo-version regex")
	}
	if !isVulnerableVersion("v0.30.0-rc1", vuln) {
		t.Fatal("prerelease below maxSafeV must stay vulnerable")
	}
	// Sanity: normal vulnerable release still flagged, patched release not.
	if !isVulnerableVersion("v0.30.2", vuln) {
		t.Fatal("version below maxSafeV must stay vulnerable")
	}
	if isVulnerableVersion("v0.31.0", vuln) {
		t.Fatal("patched release must be safe")
	}
}
