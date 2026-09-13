package knight

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
)

// #1644 case 2: the scheduler's five raw !EqualFold(TrustLevel, "readonly")
// checks were fail-open - a typo'd trust level made the audit panel report
// every write policy disabled while the scheduler kept writing. All five
// must route through the trustCanWrite whitelist via Knight.canWrite.

func new1644Knight(trust string) *Knight {
	k := &Knight{cfg: config.KnightConfig{TrustLevel: trust}}
	return k
}

func TestIssue1644CanWriteWhitelist(t *testing.T) {
	cases := map[string]bool{
		"readonly": false,
		"staged":   true,
		"auto":     true,
		"":         true, // #2213 review: empty defaults to staged, matching the auto_policy call site's audit-panel rendering
		// the exact fail-open inputs from the issue: typos read as writable before
		"read-only": false,
		"HIGH":      false,
		"ReadOnly":  false,
	}
	for trust, want := range cases {
		if got := new1644Knight(trust).canWrite(); got != want {
			t.Errorf("canWrite(%q) = %v, want %v", trust, got, want)
		}
	}
}

func TestIssue1644SchedulerGatesUseWhitelist(t *testing.T) {
	// Source-level regression pin: no raw EqualFold(TrustLevel, "readonly")
	// gate may return to scheduler.go (the L881 exact "auto" promotion
	// comparison is allowed - it was always exact).
	src, err := os.ReadFile("scheduler.go")
	if err != nil {
		t.Skipf("source layout changed: %v", err)
	}
	for i, line := range strings.Split(string(src), "\n") {
		if strings.Contains(line, `EqualFold(k.cfg.TrustLevel, "readonly")`) {
			t.Fatalf("scheduler.go:%d still contains the raw fail-open trust gate: %s", i+1, line)
		}
	}
}

func TestIssue1644ReviewStagingSkillsTypoTrustNoWrite(t *testing.T) {
	// a typo'd trust level must not run the staging review (it writes)
	k := new1644Knight("read-only")
	k.reviewStagingSkills(context.Background()) // must be a no-op, not a write
	// structural: the same typo'd knight agrees with the audit panel
	if k.canWrite() != trustCanWrite(strings.ToLower(strings.TrimSpace(k.cfg.TrustLevel))) {
		t.Fatal("scheduler and audit panel disagree on write access")
	}
}

// #1644 family / #2213 follow-up (sa-169): the staging-promotion branch
// compared the RAW TrustLevel against "auto" while canWrite and the audit
// panel normalize - "AUTO"/" auto " showed auto-promotion ENABLED but never
// promoted. Source-level pin: no raw cfg.TrustLevel comparison may return.
func TestIssue1644PromotionBranchNormalizes(t *testing.T) {
	src, err := os.ReadFile("scheduler.go")
	if err != nil {
		t.Skipf("source layout changed: %v", err)
	}
	for i, line := range strings.Split(string(src), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.Contains(trimmed, `k.cfg.TrustLevel == "auto"`) {
			t.Fatalf("scheduler.go:%d still compares the raw trust value: %s", i+1, line)
		}
	}
}
