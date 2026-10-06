package commands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const taxonomySkill = `---
name: deploy-taxonomy
description: deploy then smoke test
precondition: working tree clean and on a release branch
postcondition: smoke tests green and tag pushed
state-contract: stage-1 stores the released git sha in .release-state for stage-2 rollback
failure-modes:
  - name: smoke-5xx
    detect: smoke endpoint returns 5xx
    recover: rollback to the sha recorded in .release-state
  - name: tag-collision
    detect: git push origin tag rejected as already exists
    recover: bump patch version and re-run version sync script
---

Deploy the release.
`

func TestParseCommandMarkdownTaxonomy(t *testing.T) {
	template, meta := parseCommandMarkdown(taxonomySkill)
	if strings.TrimSpace(template) != "Deploy the release." {
		t.Fatalf("template = %q", template)
	}
	if meta.Precondition != "working tree clean and on a release branch" {
		t.Errorf("precondition = %q", meta.Precondition)
	}
	if meta.Postcondition != "smoke tests green and tag pushed" {
		t.Errorf("postcondition = %q", meta.Postcondition)
	}
	if !strings.Contains(meta.StateContract, ".release-state") {
		t.Errorf("state-contract = %q", meta.StateContract)
	}
	if len(meta.FailureModes) != 2 {
		t.Fatalf("failure modes = %d, want 2", len(meta.FailureModes))
	}
	if meta.FailureModes[0].Name != "smoke-5xx" || meta.FailureModes[0].Recover == "" {
		t.Errorf("failure mode[0] = %+v", meta.FailureModes[0])
	}
	if meta.FailureModes[1].Detect == "" {
		t.Errorf("failure mode[1] detect empty: %+v", meta.FailureModes[1])
	}
}

func TestLoadCommandFileTaxonomyRoundTrip(t *testing.T) {
	dir := t.TempDir()
	skillDir := filepath.Join(dir, "deploy-taxonomy")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(taxonomySkill), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd, ok := loadCommandFile(filepath.Join(skillDir, "SKILL.md"), "deploy-taxonomy", loadTarget{Source: SourceProject, LoadedFrom: LoadedFromSkills})
	if !ok {
		t.Fatal("loadCommandFile returned false")
	}
	if len(cmd.FailureModes) != 2 || cmd.FailureModes[0].Name != "smoke-5xx" {
		t.Errorf("cmd.FailureModes = %+v", cmd.FailureModes)
	}
	if cmd.Precondition == "" || cmd.Postcondition == "" || cmd.StateContract == "" {
		t.Errorf("contract fields lost: %+v", cmd)
	}
}

func TestParseCommandMarkdownWithoutTaxonomy(t *testing.T) {
	template, meta := parseCommandMarkdown("---\nname: plain\ndescription: p\n---\n\nBody.\n")
	if strings.TrimSpace(template) != "Body." {
		t.Fatalf("template = %q", template)
	}
	if meta.Precondition != "" || meta.FailureModes != nil {
		t.Errorf("legacy skill must parse with zero taxonomy fields: %+v", meta)
	}
}
