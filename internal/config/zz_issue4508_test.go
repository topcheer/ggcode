//go:build goolm

package config

import (
	"os"
	"strings"
	"testing"
)

// #4508 pin: the opencode refresh-failure path must clear a permanently
// rejected token, mirroring the Claude path's #1805 case-1 block - the old
// path just returned the error and the dead token kept the status stuck on
// "expired" instead of flipping to re-onboarding.
func TestIssue4508_OpenCodePermanentFailureClearsToken(t *testing.T) {
	b, err := os.ReadFile("config_vendor.go")
	if err != nil {
		t.Skip("source not readable in this environment")
	}
	s := string(b)
	// Scope to the opencode refresh function.
	idx := strings.Index(s, "func refreshOpenCodeOAuthToken(")
	if idx < 0 {
		t.Fatal("refreshOpenCodeOAuthToken not found")
	}
	end := strings.Index(s[idx+1:], "\nfunc ")
	if end < 0 {
		end = len(s) - idx - 1
	}
	fn := s[idx : idx+1+end]
	if !strings.Contains(fn, "isPermanentRefreshFailure(refreshErr)") {
		t.Fatal("#4508 regression: no permanent-failure branch in the opencode path")
	}
	if !strings.Contains(fn, `store.Delete("opencode")`) {
		t.Fatal("#4508 regression: dead token not deleted on permanent rejection")
	}
}
