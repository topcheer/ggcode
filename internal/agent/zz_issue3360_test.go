package agent

// #3360 probes: pseudo-version pins must not be flagged (commit content is
// not comparable by semver), prereleases sort below their release.

import "testing"

func TestIssue3360_PseudoVersionPinsNotFlagged(t *testing.T) {
	vuln := vulnEntry{maxSafeV: "0.31.0"}
	cases := []struct{ name, ver string }{
		{"no-base pseudo", "v0.0.0-20250101120000-abcdef123456"},
		{"base-tag pseudo", "v0.30.0-0.20241220120000-123456abcdef"},
		{"recent commit pseudo", "v0.0.0-20260101120000-fedcba654321"},
	}
	for _, c := range cases {
		if isVulnerableVersion(c.ver, vuln) {
			t.Fatalf("%s (%s): pseudo-version pin must not be flagged as a known Critical CVE", c.name, c.ver)
		}
	}
}

func TestIssue3360_PrereleaseSortsBelowRelease(t *testing.T) {
	vuln := vulnEntry{maxSafeV: "0.31.0"}
	// v0.31.0-rc1 still contains the vulnerability (rc < release) - the old
	// truncation read it as 0.31.0 and called it patched.
	if !isVulnerableVersion("v0.31.0-rc1", vuln) {
		t.Fatal("prerelease of the unpatched line must be vulnerable (rc1 < 0.31.0)")
	}
	// Sanity: the release itself is safe, anything below stays vulnerable.
	if isVulnerableVersion("v0.31.0", vuln) {
		t.Fatal("the patched release must be safe")
	}
	if !isVulnerableVersion("v0.30.2", vuln) {
		t.Fatal("below the patched release must stay vulnerable")
	}
}

func TestIssue3360_CompareVersionsSemverOrdering(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"0.31.0", "0.31.0-rc1", 1},
		{"0.31.0-rc1", "0.31.0", -1},
		{"0.31.0-rc1", "0.31.0-rc2", -1},
		{"0.31.0", "0.31.0", 0},
		{"0.30.9", "0.31.0", -1},
	}
	for _, c := range cases {
		if got := compareVersions(c.a, c.b); got != c.want {
			t.Fatalf("compareVersions(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}
