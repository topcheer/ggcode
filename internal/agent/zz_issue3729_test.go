package agent

// #3729 probe: the global-tier fill-gaps top-up counted local categories by
// file existence, so a locally-retired zombie entry (EffectiveConfidence
// eroded below the 0.3 floor, or r461 effectiveness-gated) still suppressed
// same-category global entries - a double supply cut: the category was
// injected neither locally nor globally.

import (
	"strings"
	"testing"
	"time"
)

func TestIssue3729_ZombieCategoryDoesNotSuppressGlobal(t *testing.T) {
	trajHoldoutEnabled = false
	t.Cleanup(func() { trajHoldoutEnabled = true })
	home := t.TempDir()
	t.Setenv("HOME", home) // TrajGlobalPath reads HOME/.ggcode/...
	dir := t.TempDir()
	now := time.Now()

	// legacy conf 0.5, 90 days unreinforced: EffectiveConfidence
	// = 0.25 + 0.25*0.5^3 ~= 0.281 < 0.3 -> retired (#3266(I)).
	zombie := mkConfLearning(now.Add(-90*24*time.Hour), "strategy", "test_hygiene",
		"zombie local insight", 0.5, 0, now.Add(-90*24*time.Hour))
	if zombie.EffectiveConfidence() >= trajPromptMinConfidence {
		t.Fatalf("fixture assumption broken: zombie conf %.3f must be below floor %.2f",
			zombie.EffectiveConfidence(), trajPromptMinConfidence)
	}
	// Vigorous global entry of the SAME category: multi-project reinforced,
	// comfortably above the floor.
	global := mkConfLearning(now.Add(-30*24*time.Hour), "strategy", "test_hygiene",
		"global fill-in insight", 0.9, 40, now.Add(-24*time.Hour))
	if !trajInjectEligible(global) || trajInjectEligible(zombie) {
		t.Fatalf("eligibility mismatch: global=%v zombie=%v",
			trajInjectEligible(global), trajInjectEligible(zombie))
	}

	writeLearnings(t, dir, []trajectoryLearning{zombie})
	writeLearnings(t, home, []trajectoryLearning{global})
	s := newTrajIntelState()
	got := s.RenderPromptSection(dir)
	if !strings.Contains(got, "global fill-in insight") {
		t.Fatalf("global entry for zombie category must inject instead of being suppressed, got:\n%s", got)
	}
	if strings.Contains(got, "zombie local insight") {
		t.Fatalf("zombie must still not inject itself, got:\n%s", got)
	}
}

func TestIssue3729_EligibleLocalStillSuppressesGlobal(t *testing.T) {
	trajHoldoutEnabled = false
	t.Cleanup(func() { trajHoldoutEnabled = true })
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := t.TempDir()
	now := time.Now()

	local := mkConfLearning(now.Add(-time.Hour), "strategy", "test_hygiene",
		"local variant wins own category", 0.9, 20, now)
	global := mkConfLearning(now.Add(-30*24*time.Hour), "strategy", "test_hygiene",
		"global fill-in insight", 0.9, 40, now.Add(-24*time.Hour))
	if !trajInjectEligible(local) {
		t.Fatalf("local entry must be eligible")
	}

	writeLearnings(t, dir, []trajectoryLearning{local})
	writeLearnings(t, home, []trajectoryLearning{global})
	s := newTrajIntelState()
	got := s.RenderPromptSection(dir)
	if strings.Contains(got, "general, other projects") {
		t.Fatalf("eligible local entry must keep suppressing same-category global, got:\n%s", got)
	}
	if !strings.Contains(got, "local variant wins") {
		t.Fatalf("local entry must inject, got:\n%s", got)
	}
}
