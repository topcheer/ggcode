package agent

import (
	"strings"
	"testing"
)

// #3046 B1: /vN cross-segment major bumps (v2->v3, v3->v4) must be detected,
// not just v1->v2 (bare-base upgrades).
func TestIssue3046_B1_CrossSegmentMajorBump(t *testing.T) {
	cases := []struct {
		name    string
		oldReq  string
		newReq  string
		wantSub string // "" = expect no warning
	}{
		{
			name:    "v2 to v3",
			oldReq:  "require github.com/foo/bar/v2 v2.5.0",
			newReq:  "require github.com/foo/bar/v3 v3.0.0",
			wantSub: "upgraded v2 -> v3",
		},
		{
			name:    "v3 to v4",
			oldReq:  "require github.com/foo/bar/v3 v3.2.1",
			newReq:  "require github.com/foo/bar/v4 v4.0.0",
			wantSub: "upgraded v3 -> v4",
		},
		{
			name:    "v1 to v2 still detected",
			oldReq:  "require github.com/foo/bar v1.4.0",
			newReq:  "require github.com/foo/bar/v2 v2.0.1",
			wantSub: "upgraded v1 -> v2",
		},
		{
			name:   "v3 downgrade to v2 not warned",
			oldReq: "require github.com/foo/bar/v3 v3.0.0",
			newReq: "require github.com/foo/bar/v2 v2.9.9",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			warnings := checkBreakingChangeDep("go.mod", tc.oldReq, tc.newReq)
			if tc.wantSub == "" {
				for _, w := range warnings {
					if strings.Contains(w, "Major Version Bump") {
						t.Fatalf("unexpected major-bump warning: %q", w)
					}
				}
				return
			}
			found := false
			for _, w := range warnings {
				if strings.Contains(w, "[Major Version Bump]") && strings.Contains(w, tc.wantSub) {
					found = true
				}
			}
			if !found {
				t.Fatalf("expected major-bump warning containing %q, got %v", tc.wantSub, warnings)
			}
		})
	}
}

// #3046 B2: upgrading a vulnerable package to a safe version must not be
// followed by the generic "consider running govulncheck" reminder.
func TestIssue3046_B2_SafeUpgradeNoScanReminder(t *testing.T) {
	oldManifest := `{
  "dependencies": {
    "lodash": "4.16.0"
  }
}`
	newManifest := `{
  "dependencies": {
    "lodash": "4.17.21"
  }
}`
	warnings := checkDependencyVulns("package.json", oldManifest, newManifest)
	for _, w := range warnings {
		if strings.Contains(w, "Dependency Vulnerability") {
			t.Fatalf("safe version must not be flagged vulnerable: %q", w)
		}
		if strings.Contains(w, "consider running") {
			t.Fatalf("scan reminder right after a successful security upgrade is noise: %q", w)
		}
	}
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings for a safe upgrade, got %v", warnings)
	}
}

// #3046 B2 (control): a genuinely unrelated dependency change still gets the
// generic scan reminder - the suppression must not swallow the fallback.
func TestIssue3046_B2_UnrelatedChangeStillReminds(t *testing.T) {
	oldManifest := `{
  "dependencies": {
    "left-pad": "1.0.0"
  }
}`
	newManifest := `{
  "dependencies": {
    "left-pad": "1.3.0"
  }
}`
	warnings := checkDependencyVulns("package.json", oldManifest, newManifest)
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "consider running") {
			found = true
		}
	}
	if !found {
		t.Fatalf("unrelated dependency change should still get the scan reminder, got %v", warnings)
	}
}
