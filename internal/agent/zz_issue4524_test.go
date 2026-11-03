package agent

import (
	"os"
	"regexp"
	"testing"
)

func TestIssue4524_AttemptDenominatorsConsistent(t *testing.T) {
	b, err := os.ReadFile("verify.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)

	bad := regexp.MustCompile("attempt %d/%d\\)\u2026\", retryCount\\+1, maxSyncVerifyRetries\\)")
	if bad.MatchString(src) {
		t.Fatal("#4524: attempt progress uses maxSyncVerifyRetries (3) as denominator - must be maxSyncVerifyRetries+1")
	}

	rerun := regexp.MustCompile("\\(attempt %d/%d\\)\u2026\", cmd, retryCount\\+1, maxSyncVerifyRetries\\+1")
	if !rerun.MatchString(src) {
		t.Fatal("re-run progress line must use the +1 denominator")
	}
	repair := regexp.MustCompile("\\(attempt %d/%d\\)\u2026\", retryCount\\+1, maxSyncVerifyRetries\\+1")
	if !repair.MatchString(src) {
		t.Fatal("auto-repair progress line must use the +1 denominator")
	}

	exhaust := regexp.MustCompile("after %d attempts \u2014 please review manually\\.\", maxSyncVerifyRetries\\+1")
	if !exhaust.MatchString(src) {
		t.Fatal("exhaustion count must stay maxSyncVerifyRetries+1")
	}
}
