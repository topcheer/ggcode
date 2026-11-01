package agent

import (
	"os"
	"regexp"
	"testing"
)

// #4524 bug 2: all user-facing attempt surfaces must agree on the
// denominator (maxSyncVerifyRetries+1 - the number of attempts that actually
// execute). Source-pin guard against one surface drifting back to 3.
// (Regexes use \u escapes for the ellipsis/em-dash the progress strings
// contain, keeping this file itself ASCII-clean.)
func TestIssue4524_AttemptDenominatorsConsistent(t *testing.T) {
	b, err := os.ReadFile("verify.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)

	// The old shape: "...(attempt %d/%d)...", retryCount+1, maxSyncVerifyRetries)
	// (bare 3 as denominator) must be gone.
	bad := regexp.MustCompile("attempt %d/%d\\)\u2026\", retryCount\\+1, maxSyncVerifyRetries\\)")
	if bad.MatchString(src) {
		t.Fatal("#4524: attempt progress uses maxSyncVerifyRetries (3) as denominator - must be maxSyncVerifyRetries+1")
	}

	// Both progress surfaces (re-run + auto-repair) carry the +1 denominator.
	rerun := regexp.MustCompile("\\(attempt %d/%d\\)\u2026\", cmd, retryCount\\+1, maxSyncVerifyRetries\\+1")
	if !rerun.MatchString(src) {
		t.Fatal("re-run progress line must use the +1 denominator")
	}
	repair := regexp.MustCompile("\\(attempt %d/%d\\)\u2026\", retryCount\\+1, maxSyncVerifyRetries\\+1")
	if !repair.MatchString(src) {
		t.Fatal("auto-repair progress line must use the +1 denominator")
	}

	// The exhaustion count stays +1 as well.
	exhaust := regexp.MustCompile("after %d attempts \u2014 please review manually\\.\", maxSyncVerifyRetries\\+1")
	if !exhaust.MatchString(src) {
		t.Fatal("exhaustion count must stay maxSyncVerifyRetries+1")
	}
}
