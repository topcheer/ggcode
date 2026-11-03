package knight

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
)

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

func TestIssue985_StepsHeadingVariantsAccepted(t *testing.T) {
	accepted := []string{
		"## Steps",
		"## steps",
		"## Steps:",
		"## Steps Overview",
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
		"##Steps",
		"## Pipeline - 5 steps",
		"## Usage",
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

func TestIssue985_PromotePropagatesActiveSkillsError(t *testing.T) {
	k, _, projDir := newIssue985Knight(t)
	entry := issue985WriteStaging(t, k, "guard-error-repro", "## Steps")

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

func TestIssue985_PromoteStillWorksWhenIndexHealthy(t *testing.T) {
	k, _, _ := newIssue985Knight(t)
	entry := issue985WriteStaging(t, k, "healthy-promote", "## Steps Overview")
	if err := k.promoteStagingEntry(entry); err != nil {
		t.Fatalf("healthy promote must succeed with variant heading, err: %v", err)
	}
	_ = context.Background()
}
