package knight

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
)

func verifyDirs(t *testing.T) (homeDir, projDir string) {
	t.Helper()
	dir := t.TempDir()
	return filepath.Join(dir, "home"), filepath.Join(dir, "project")
}

func verifyWriteActive(t *testing.T, homeDir, projDir, name, scope, description string) {
	t.Helper()
	var base string
	if scope == "global" {
		base = filepath.Join(homeDir, ".ggcode", "skills", name)
	} else {
		base = filepath.Join(projDir, ".ggcode", "skills", name)
	}
	if err := os.MkdirAll(base, 0755); err != nil {
		t.Fatalf("MkdirAll active dir: %v", err)
	}
	content := "---\nname: " + name + "\ndescription: " + description + "\nscope: " + scope + "\ncreated_by: user\n---\n# " + name + "\n\n## When to Use\nUse when deploying the service to production.\n\n## Steps\n1. Run the deploy pipeline for the target environment.\n"
	if err := os.WriteFile(filepath.Join(base, "SKILL.md"), []byte(content), 0644); err != nil {
		t.Fatalf("WriteFile active skill: %v", err)
	}
}

const verifyValidStagingBody = `
# Deploy Helper

## When to Use
Use when the release candidate must ship to staging infrastructure.

## When Not to Use
Do not use for local development builds.

## Steps
1. Build the artifact with the release profile.
2. Push the artifact to the staging registry endpoint.
`

func verifyStagingPath(homeDir, projDir, scope, name string) string {
	if scope == "global" {
		return filepath.Join(homeDir, ".ggcode", "skills-staging", name+".md")
	}
	return filepath.Join(projDir, ".ggcode", "skills-staging", name+".md")
}

func TestSuspect1_StagingFailCountConcurrentWithUserPromoteReject(t *testing.T) {
	homeDir, projDir := verifyDirs(t)
	k := New(config.KnightConfig{Enabled: true, TrustLevel: "staged"}, homeDir, projDir, nil)

	badBody := "---\nname: race-invalid\ndescription: Body deliberately missing the required steps section.\nscope: project\ncreated_by: knight\n---\n# Race Invalid\n\n## When to Use\nTrigger validation failure path.\n"
	if _, err := k.promoter.WriteStaging("race-invalid", "project", badBody); err != nil {
		t.Fatalf("WriteStaging(bad): %v", err)
	}

	goodBody := "---\nname: race-good\ndescription: Valid candidate exercising the map delete path.\nscope: project\ncreated_by: knight\n---\n" + verifyValidStagingBody
	if _, err := k.promoter.WriteStaging("race-good", "project", goodBody); err != nil {
		t.Fatalf("WriteStaging(good): %v", err)
	}

	ctx := context.Background()
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			k.reviewStagingSkills(ctx)
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			_ = k.RejectStaging("race-invalid")
			_ = k.RejectStaging("race-good")
			_, _ = k.promoter.WriteStaging("race-invalid", "project", badBody)
			_, _ = k.promoter.WriteStaging("race-good", "project", goodBody)
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			_ = k.PromoteStaging("race-invalid")
			_ = k.PromoteStaging("race-good")
		}
	}()

	wg.Wait()

	t.Log("completed 100 concurrent review cycles against user promote/reject")
}

func TestSuspect2_CheckDuplicateNilActive(t *testing.T) {
	entry := &SkillEntry{
		Name:  "deploy",
		Scope: "project",
		Meta: SkillMeta{
			Name:        "deploy",
			Description: "Deploy the current project to production infrastructure.",
			Scope:       "project",
		},
	}
	if CheckDuplicate(entry, nil) {
		t.Fatal("CheckDuplicate(entry, nil) = true, want false: nil active list silently disables the duplicate guard")
	}

	existing := []*SkillEntry{{
		Name:  "deploy",
		Scope: "project",
		Meta:  SkillMeta{Name: "deploy", Description: "Existing deploy workflow for this repository.", Scope: "project"},
	}}
	if !CheckDuplicate(entry, existing) {
		t.Fatal("CheckDuplicate(entry, existing) = false, want true")
	}
}

func TestSuspect2_ActiveSkillsErrorIsCurrentlyUnreachable(t *testing.T) {
	homeDir, projDir := verifyDirs(t)

	k := New(config.KnightConfig{Enabled: true}, homeDir, projDir, nil)
	if _, err := k.index.ActiveSkills(); err != nil {
		t.Fatalf("ActiveSkills on missing dirs returned error: %v (expected nil — Scan swallows scanDir errors)", err)
	}

	verifyWriteActive(t, homeDir, projDir, "deploy", "project", "Existing deploy workflow.")
	if err := os.Chmod(filepath.Join(projDir, ".ggcode", "skills", "deploy"), 0000); err == nil {
		_, err2 := k.index.ActiveSkills()
		if err2 != nil {
			t.Fatalf("ActiveSkills surfaced scan error: %v — claim premise (error swallow) would hold only if this were non-nil", err2)
		}
		_ = os.Chmod(filepath.Join(projDir, ".ggcode", "skills", "deploy"), 0755)
	}
}

func TestSuspect3a_CrossScopeSameNameAutoRejected(t *testing.T) {
	homeDir, projDir := verifyDirs(t)

	verifyWriteActive(t, homeDir, projDir, "deploy", "project", "Existing project deploy workflow.")

	k := New(config.KnightConfig{Enabled: true, TrustLevel: "staged"}, homeDir, projDir, nil)

	stagingContent := "---\nname: deploy\ndescription: Global deploy conventions for all repositories.\nscope: global\ncreated_by: user\n---\n# Deploy (global)\n\n## When to Use\nUse when shipping any project to shared infrastructure.\n\n## When Not to Use\nDo not use for repo-local scripts.\n\n## Steps\n1. Verify the release checklist for shared infra.\n"
	stagingPath, err := k.promoter.WriteStaging("deploy", "global", stagingContent)
	if err != nil {
		t.Fatalf("WriteStaging: %v", err)
	}
	if _, err := os.Stat(stagingPath); err != nil {
		t.Fatalf("staging file must exist before review: %v", err)
	}

	staging, err := k.index.StagingSkills()
	if err != nil || len(staging) != 1 {
		t.Fatalf("expected exactly 1 staging skill, err=%v n=%d", err, len(staging))
	}
	if vr := ValidateSkill(staging[0]); !vr.Valid {
		t.Fatalf("staging skill should pass ValidateSkill in isolation, got errors: %v", vr.Errors)
	}

	k.reviewStagingSkills(context.Background())

	if _, err := os.Stat(stagingPath); err != nil {
		t.Fatalf("global staging 'deploy' was deleted despite disjoint scope from project active 'deploy': %v", err)
	}

	activePath := filepath.Join(projDir, ".ggcode", "skills", "deploy", "SKILL.md")
	if _, err := os.Stat(activePath); err != nil {
		t.Fatalf("project active skill should still exist: %v", err)
	}

	revPath, err := k.promoter.WriteStaging("deploy", "project", stagingContent)
	if err != nil {
		t.Fatalf("WriteStaging revision: %v", err)
	}
	for i := 1; i <= knightStagingMaxValidationFails+1; i++ {
		k.reviewStagingSkills(context.Background())
	}
	if _, err := os.Stat(revPath); err != nil {
		t.Fatalf("same-scope revision staging 'deploy' auto-deleted — revisions must await explicit review, not auto-reject: %v", err)
	}
}

func TestSuspect3b_TrivialPatternSubstringKillsLegitSkill(t *testing.T) {
	homeDir, projDir := verifyDirs(t)
	k := New(config.KnightConfig{Enabled: true, TrustLevel: "staged"}, homeDir, projDir, nil)

	stagingContent := "---\nname: safe-edit-discipline\ndescription: Enforce read-before-edit discipline across all code changes.\nscope: project\ncreated_by: user\n---\n# Safe Edit Discipline\n\n## When to Use\nUse before any code modification in this repository.\n\n## When Not to Use\nDo not use for documentation-only edits.\n\n## Steps\n1. Never edit a file without reading it first.\n2. Confirm the surrounding function context before patching.\n"
	stagingPath, err := k.promoter.WriteStaging("safe-edit-discipline", "project", stagingContent)
	if err != nil {
		t.Fatalf("WriteStaging: %v", err)
	}

	staging, err := k.index.StagingSkills()
	if err != nil || len(staging) != 1 {
		t.Fatalf("expected exactly 1 staging skill, err=%v n=%d", err, len(staging))
	}
	vr := ValidateSkill(staging[0])

	if !vr.Valid {
		t.Fatalf("legitimate skill must stay valid despite trivial-substring hit, errors: %v", vr.Errors)
	}
	foundTrivial := false
	for _, w := range vr.Warnings {
		if strings.Contains(w, "basic tool usage") {
			foundTrivial = true
		}
	}
	if !foundTrivial {
		t.Fatalf("expected the trivial-pattern warning, got warnings: %v", vr.Warnings)
	}

	for i := 1; i <= knightStagingMaxValidationFails; i++ {
		k.reviewStagingSkills(context.Background())
	}
	if _, err := os.Stat(stagingPath); err != nil {
		t.Fatalf("staging file deleted after %d cycles of warnings-only validation — auto-reject must not fire on warnings: %v", knightStagingMaxValidationFails, err)
	}
}
