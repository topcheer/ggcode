package knight

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/provider"
)

// --- Sanitization ----------------------------------------------------------

func TestSanitizeScenarioTextRedactsSecrets(t *testing.T) {
	cases := []string{
		"contact alice@example.com",
		"key sk-AAAAAAAAAAAAAAAAAA",
		"github token ghp_AAAAAAAAAAAAAAAAAAAAA",
		"aws AKIAIOSFODNN7EXAMPLE",
		"server 192.168.1.42",
		"Bearer abcdefghijklmnopqrstuvwx",
	}
	for _, in := range cases {
		out := sanitizeScenarioText(in)
		if !containsRedactedSensitive(out) {
			t.Fatalf("expected sanitization for %q, got %q", in, out)
		}
	}
}

func TestTruncateSanitizedRespectsRuneLimit(t *testing.T) {
	in := strings.Repeat("a", 2000)
	out := truncateSanitized(in, 100)
	if len([]rune(out)) > 110 {
		t.Fatalf("got %d runes, want ≤110 (max+ellipsis)", len([]rune(out)))
	}
	if !strings.HasSuffix(out, "...") {
		t.Fatalf("expected ellipsis suffix, got %q", out)
	}
}

// --- Emit throttle ---------------------------------------------------------

func TestEmitThrottleSuppressesRepeatsWithinWindow(t *testing.T) {
	g := newEmitThrottle(time.Hour)
	now := time.Now()
	if !g.allow("k", EmitSeverityNotice, now) {
		t.Fatal("first allow should succeed")
	}
	if g.allow("k", EmitSeverityNotice, now.Add(time.Minute)) {
		t.Fatal("second allow inside window should be suppressed")
	}
	if !g.allow("k", EmitSeverityNotice, now.Add(2*time.Hour)) {
		t.Fatal("after window allow should succeed")
	}
}

func TestEmitThrottleResetClearsKey(t *testing.T) {
	g := newEmitThrottle(time.Hour)
	now := time.Now()
	g.allow("k", EmitSeverityNotice, now)
	g.reset("k")
	if !g.allow("k", EmitSeverityNotice, now.Add(time.Second)) {
		t.Fatal("after reset, key should allow again")
	}
}

func TestEmitThrottleEmptyKeyAlwaysAllows(t *testing.T) {
	g := newEmitThrottle(time.Hour)
	now := time.Now()
	for i := 0; i < 5; i++ {
		if !g.allow("", EmitSeverityInfo, now) {
			t.Fatal("empty key must always allow")
		}
	}
}

// --- Jaccard / fingerprint -------------------------------------------------

func TestJaccardIdenticalIsOne(t *testing.T) {
	a := tokenizeForSimilarity("build verify test")
	if got := jaccardSimilarity(a, a); got < 0.999 {
		t.Fatalf("identical jaccard = %v", got)
	}
}

func TestJaccardDisjointIsZero(t *testing.T) {
	a := tokenizeForSimilarity("foo bar baz")
	b := tokenizeForSimilarity("alpha beta gamma")
	if got := jaccardSimilarity(a, b); got != 0 {
		t.Fatalf("disjoint jaccard = %v", got)
	}
}

// --- Rule-based overlap ----------------------------------------------------

func TestComputeRuleBasedOverlapDetectsHighSimilarity(t *testing.T) {
	body := "When to use\nUse before pushing changes to verify build passes.\nSteps\n- run go build\n- run tests\n- never push without verification\n"
	candidate := &SkillEntry{
		Name:  "build-then-verify-no-push",
		Scope: "project",
		Meta:  SkillMeta{Description: "Build, verify, do not push without confirmation"},
	}
	active := []*SkillEntry{
		{
			Name:  "build-verify-no-push",
			Scope: "project",
			Meta:  SkillMeta{Description: "Build, verify, do not push without confirmation"},
		},
	}
	dec := computeRuleBasedOverlap(candidate, body, active, func(*SkillEntry) string { return body })
	if !dec.HasOverlap {
		t.Fatalf("expected overlap, got %#v", dec)
	}
	if dec.WorstActiveRef == "" {
		t.Fatalf("expected ref set, got %#v", dec)
	}
}

func TestComputeRuleBasedOverlapAllowsUnrelated(t *testing.T) {
	candBody := "When to use\nApply when handling QQ passive replies\nSteps\n- track msg id\n"
	activeBody := "When to use\nbefore pushing\nSteps\n- run build\n"
	candidate := &SkillEntry{
		Name:  "im-qq-passive-window",
		Scope: "project",
		Meta:  SkillMeta{Description: "Track QQ passive reply window"},
	}
	active := []*SkillEntry{
		{Name: "build-verify", Scope: "project", Meta: SkillMeta{Description: "Build before push"}},
	}
	dec := computeRuleBasedOverlap(candidate, candBody, active, func(*SkillEntry) string { return activeBody })
	if dec.HasOverlap {
		t.Fatalf("expected no overlap, got %#v", dec)
	}
}

// --- Usage decay -----------------------------------------------------------

func TestUsageDecayFadesOverTime(t *testing.T) {
	dir := t.TempDir()
	ut := NewUsageTracker(filepath.Join(dir, "u.json"))
	ut.RecordPromptOutcome("project:foo", false)
	ut.mu.Lock()
	entry := ut.data["project:foo"]
	entry.LastPromptDecay = time.Now().Add(-90 * 24 * time.Hour)
	ut.mu.Unlock()
	successes, failures := ut.GetPromptOutcome("project:foo")
	if successes != 0 {
		t.Fatalf("expected 0 successes, got %d", successes)
	}
	if failures > 0 {
		t.Fatalf("expected decayed failure to round to 0 after 90 days, got %d", failures)
	}
}

// --- Reject feedback cooldown ---------------------------------------------

func TestRejectFeedbackCooldownActiveWithinWindow(t *testing.T) {
	dir := t.TempDir()
	store := newRejectFeedbackStore(filepath.Join(dir, "rej.jsonl"))
	if err := store.Append(rejectFeedbackEntry{Name: "foo", Scope: "project", Action: "reject", Reason: "noise"}); err != nil {
		t.Fatalf("append: %v", err)
	}
	if _, ok := store.coolDownActive("project", "foo", time.Now()); !ok {
		t.Fatal("expected cooldown active immediately after rejection")
	}
	if _, ok := store.coolDownActive("project", "bar", time.Now()); ok {
		t.Fatal("unrelated skill should not be in cooldown")
	}
	if _, ok := store.coolDownActive("project", "foo", time.Now().Add(8*24*time.Hour)); ok {
		t.Fatal("expected cooldown to expire after 7+ days")
	}
}

// --- Bucket budget --------------------------------------------------------

func TestBucketBudgetCapsSpendAndRollsOver(t *testing.T) {
	bb := newBucketBudget(1_000_000)
	now := time.Now()
	bucket := BudgetBucketEval
	for i := 0; i < 100; i++ {
		if !bb.canSpend(bucket, now) {
			break
		}
		bb.record(bucket, 50_000, now)
	}
	if bb.canSpend(bucket, now) {
		t.Fatal("bucket should be exhausted")
	}
	if !bb.canSpend(bucket, now.Add(48*time.Hour)) {
		t.Fatal("bucket should reset after rollover")
	}
}

func TestBucketBudgetUnlimitedAlwaysAllows(t *testing.T) {
	bb := newBucketBudget(0)
	if !bb.canSpend(BudgetBucketEval, time.Now()) {
		t.Fatal("zero daily total must allow everything")
	}
}

func TestClassifyBucketKnownAndUnknown(t *testing.T) {
	if got := classifyBucket("skill-auto-promote-eval"); got != BudgetBucketEval {
		t.Fatalf("eval bucket = %v", got)
	}
	if got := classifyBucket("nope"); got != BudgetBucketAdhoc {
		t.Fatalf("unknown defaults to adhoc, got %v", got)
	}
}

// --- Auto policy runtime derivation ---------------------------------------

func TestAutoPoliciesEffectiveWhenAutoTrustAndCaps(t *testing.T) {
	k := New(config.KnightConfig{
		Enabled:      true,
		TrustLevel:   "auto",
		Capabilities: []string{"skill_creation", "skill_validation"},
	}, t.TempDir(), t.TempDir(), nil)
	var promote, codeWrites *AutoPolicy
	for _, p := range k.AutoPolicies() {
		pp := p
		if strings.Contains(p.Name, "auto-promotion") {
			promote = &pp
		}
		if strings.Contains(p.Name, "project code writes") {
			codeWrites = &pp
		}
	}
	if promote == nil || !promote.Effective {
		t.Fatalf("auto-promotion should be effective, got %#v", promote)
	}
	if codeWrites == nil || codeWrites.Effective {
		t.Fatal("project code writes must never be effective")
	}
}

func TestAutoPoliciesReadOnlyDisablesEverything(t *testing.T) {
	k := New(config.KnightConfig{Enabled: true, TrustLevel: "readonly"}, t.TempDir(), t.TempDir(), nil)
	for _, p := range k.AutoPolicies() {
		if p.Effective {
			t.Fatalf("readonly should disable %q, got effective=true", p.Name)
		}
		if p.Reason == "" {
			t.Fatalf("inactive policy should have reason, got %#v", p)
		}
	}
}

// --- Project proposal lifecycle -------------------------------------------

func TestProjectProposalLifecycleApproveReject(t *testing.T) {
	dir := t.TempDir()
	k := New(config.KnightConfig{Enabled: true}, t.TempDir(), dir, nil)
	prop, err := k.writeProjectImprovementProposal("speed up build", "# Speed up build\n## Summary\nUse cache\n")
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if prop.Status != "proposed" {
		t.Fatalf("initial status = %q", prop.Status)
	}
	approved, err := k.ApproveProposal(prop.ID, "looks good")
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	if approved.Status != "approved" || approved.StatusNote != "looks good" {
		t.Fatalf("approved entry = %#v", approved)
	}
	listed, err := k.RecentProjectImprovementProposals(5)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(listed) != 1 || listed[0].Status != "approved" {
		t.Fatalf("expected single approved proposal, got %#v", listed)
	}

	prop2, err := k.writeProjectImprovementProposal("another", "# Another\n## Summary\ndo y\n")
	if err != nil {
		t.Fatalf("write2: %v", err)
	}
	if _, err := k.RejectProposal(prop2.ID, "not now"); err != nil {
		t.Fatalf("reject: %v", err)
	}
	listed, _ = k.RecentProjectImprovementProposals(5)
	statuses := map[string]string{}
	for _, p := range listed {
		statuses[p.ID] = p.Status
	}
	if statuses[prop.ID] != "approved" || statuses[prop2.ID] != "rejected" {
		t.Fatalf("statuses = %#v", statuses)
	}
}

func TestRejectProposalUnknownIDFails(t *testing.T) {
	k := New(config.KnightConfig{Enabled: true}, t.TempDir(), t.TempDir(), nil)
	if _, err := k.RejectProposal("does-not-exist", ""); err == nil {
		t.Fatal("expected error for unknown id")
	}
}

// --- ClearSkillScenarios ---------------------------------------------------

func TestClearSkillScenariosRemovesLog(t *testing.T) {
	dir := t.TempDir()
	k := New(config.KnightConfig{Enabled: true}, t.TempDir(), dir, nil)
	if err := k.RecordPromptSkillScenario(
		[]string{"project:foo"},
		[]provider.ContentBlock{provider.TextBlock("do something useful")},
		true,
		nil,
	); err != nil {
		t.Fatalf("record: %v", err)
	}
	scenarios, _ := k.RecentSkillScenarios(10)
	if len(scenarios) == 0 {
		t.Fatal("expected scenario recorded")
	}
	if err := k.ClearSkillScenarios(); err != nil {
		t.Fatalf("clear: %v", err)
	}
	scenarios, _ = k.RecentSkillScenarios(10)
	if len(scenarios) != 0 {
		t.Fatalf("expected empty after clear, got %d", len(scenarios))
	}
}
