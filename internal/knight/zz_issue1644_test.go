package knight

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
)

func new1644Knight(trust string) *Knight {
	k := &Knight{cfg: config.KnightConfig{TrustLevel: trust}}
	return k
}

func TestIssue1644CanWriteWhitelist(t *testing.T) {
	cases := map[string]bool{
		"readonly": false,
		"staged":   true,
		"auto":     true,
		"":         true,

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

	k := new1644Knight("read-only")
	k.reviewStagingSkills(context.Background())

	if k.canWrite() != trustCanWrite(strings.ToLower(strings.TrimSpace(k.cfg.TrustLevel))) {
		t.Fatal("scheduler and audit panel disagree on write access")
	}
}

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
