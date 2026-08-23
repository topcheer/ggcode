package knight

// Tests for issue #985:
//  1. hasSection/hasActionableSteps required the heading line to be exactly
//     "## Steps" (case-insensitive), so common LLM-generated variants like
//     "## Steps:" or "## Steps Overview" were rejected as missing the required
//     section (an Error -> Valid=false -> promoteStagingEntry refuses).
//  2. promoteStagingEntry swallowed the ActiveSkills error, making the
//     duplicate guard a no-op when the index scan failed.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
)

// issue985WriteStaging writes a staging skill with a parametrized steps
// heading and returns its indexed entry. All other content is kept valid
// (mirrors the #767 fixture) so the only variable is the heading.
func issue985WriteStaging(t *testing.T, k *Knight, name, heading string) *SkillEntry {
	t.Helper()
	content := "---\nname: " + name + "\ndescription: Enforce read-before-edit discipline across all code changes.\nscope: project\ncreated_by: user\n---\n# Safe Edit Discipline\n\n## When to Use\nUse before any code modification in this repository.\n\n## When Not to Use\nDo not use for documentation-only edits.\n\n" + heading + "\n1. Never edit a file without reading it first.\n2. Confirm the surrounding function context before patching.\n"
	if _, err := k.promoter.WriteStaging(name, "project", content); err != nil {
		t.Fatalf("WriteStaging: %v", err)
	}
	k.index.Invalidate()
	staging, err := k.index.StagingSkills()
	if err != nil || len(staging) != 1 {
		t.Fatalf("expected exactly 1 staging skill, err=%v n=%d", err, len(staging))
	}
	return staging[0]
}

func newIssue985Knight(t *testing.T) (*Knight, string, string) {
	t.Helper()
	homeDir := t.TempDir()
	projDir := t.TempDir()
	k := New(config.KnightConfig{Enabled: true, TrustLevel: "staged"}, homeDir, projDir, nil)
	return k, homeDir, projDir
}

// TestIssue985_StepsHeadingVariantsAccepted verifies that heading variants
// starting with "## steps" (after trim + lowercase) pass validation, while
// headings that do not start with the canonical prefix are still rejected.
func TestIssue985_StepsHeadingVariantsAccepted(t *testing.T) {
	accepted := []string{
		"## Steps",          // canonical
		"## steps",          // lowercase
		"## Steps:",         // trailing colon
		"## Steps Overview", // suffix words
	}
	for _, heading := range accepted {
		k, _, _ := newIssue985Knight(t)
		entry := issue985WriteStaging(t, k, "steps-variant", heading)
		vr := ValidateSkill(entry)
		if !vr.Valid {
			t.Errorf("heading %q should pass validation, errors: %v", heading, vr.Errors)
		}
	}

	rejected := []string{
		"##Steps",               // no space after ##: prefix "## steps" does not match (documented boundary)
		"## Pipeline - 5 steps", // does not start with "## steps" (documented boundary)
		"## Usage",              // no steps section at all
	}
	for _, heading := range rejected {
		k, _, _ := newIssue985Knight(t)
		entry := issue985WriteStaging(t, k, "steps-variant", heading)
		vr := ValidateSkill(entry)
		if vr.Valid {
			t.Errorf("heading %q should fail validation (no recognized steps section)", heading)
		}
		missing := false
		for _, e := range vr.Errors {
			if strings.Contains(e, "missing required '## Steps' section") {
				missing = true
			}
		}
		if !missing {
			t.Errorf("heading %q: expected missing-section error, got: %v", heading, vr.Errors)
		}
	}
}

// TestIssue985_ActionableStepsFollowsVariantHeadings verifies hasActionableSteps
// stays consistent with hasSection: list items under a variant heading like
// "## Steps Overview" must count as actionable steps (before the fix the
// variant passed the presence check but then failed the actionable check).
func TestIssue985_ActionableStepsFollowsVariantHeadings(t *testing.T) {
	lines := []string{
		"# Some Skill",
		"",
		"## Steps Overview",
		"1. Read the target file first.",
	}
	if !hasSection(lines, "## Steps") {
		t.Fatal("hasSection should recognize '## Steps Overview' via prefix match")
	}
	if !hasActionableSteps(lines) {
		t.Fatal("hasActionableSteps must recognize list items under '## Steps Overview'")
	}
}

// TestIssue985_PromotePropagatesActiveSkillsError verifies promoteStagingEntry
// returns an error when the skill index cannot be read, instead of silently
// skipping the duplicate guard with active=nil.
func TestIssue985_PromotePropagatesActiveSkillsError(t *testing.T) {
	k, _, projDir := newIssue985Knight(t)
	entry := issue985WriteStaging(t, k, "guard-error-repro", "## Steps")

	// Break the ACTIVE project skills dir: replace it with a regular file so
	// the next Scan (cache invalidated below) fails. The staging entry was
	// already fetched, and ValidateSkill reads the staging file directly, so
	// the first failure point is ActiveSkills inside promoteStagingEntry.
	activeDir := filepath.Join(projDir, ".ggcode", "skills")
	if err := os.RemoveAll(activeDir); err != nil {
		t.Fatalf("remove active dir: %v", err)
	}
	if err := os.WriteFile(activeDir, []byte("not a directory"), 0o644); err != nil {
		t.Fatalf("plant broken active dir: %v", err)
	}
	k.index.Invalidate()

	err := k.promoteStagingEntry(entry)
	if err == nil {
		t.Fatal("promoteStagingEntry must fail when ActiveSkills errors -- a nil error means the duplicate guard was skipped (active=nil)")
	}
	if !strings.Contains(err.Error(), "read active skills") {
		t.Errorf("error should mention the active-skills read failure, got: %v", err)
	}
}

// TestIssue985_PromoteStillWorksWhenIndexHealthy is the companion sanity
// check: with a healthy index, the same promote path must succeed end to end.
func TestIssue985_PromoteStillWorksWhenIndexHealthy(t *testing.T) {
	k, _, _ := newIssue985Knight(t)
	entry := issue985WriteStaging(t, k, "healthy-promote", "## Steps Overview")
	if err := k.promoteStagingEntry(entry); err != nil {
		t.Fatalf("healthy promote must succeed with variant heading, err: %v", err)
	}
	_ = context.Background() // keep context import if assertions change
}
