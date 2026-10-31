package knight

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// #3656 probes: skill_promoter trim/ownership/nil-guard defects.

// d1: WriteStaging must canonicalize (trim) the name BEFORE building the
// on-disk filename - the old code validated TrimSpace(name) but formatted
// paths from the raw value, producing "knight-20261009- deploy-web.md".
func Test3656WriteStagingTrimsName(t *testing.T) {
	proj := t.TempDir()
	p := NewPromoter(proj, proj)

	path, err := p.WriteStaging(" deploy-web ", "project", "# skill")
	if err != nil {
		t.Fatalf("WriteStaging: %v", err)
	}
	base := filepath.Base(path)
	if strings.TrimSpace(base) != base || strings.Contains(base, " ") {
		t.Errorf("staging filename %q contains whitespace (uncanonicalized name leaked to disk)", base)
	}
	if !strings.Contains(base, "deploy-web") {
		t.Errorf("staging filename %q lost the skill name", base)
	}
}

// d1 companion: an untrimmable name (whitespace inside) must still be rejected.
func Test3656WriteStagingRejectsInnerSpace(t *testing.T) {
	proj := t.TempDir()
	p := NewPromoter(proj, proj)
	if _, err := p.WriteStaging("deploy web", "project", "# x"); err == nil {
		t.Error("expected rejection for name with inner space")
	}
}

// d2: Promote must verify entry.Path actually lives under the staging
// directory of entry.Scope. A crossed index entry (Path in GLOBAL staging,
// Scope="project") used to copy the content into project-active and then
// DELETE the global source file - leaving the global index dangling.
func Test3656PromoteRejectsCrossScopeStagingPath(t *testing.T) {
	home := t.TempDir() // global root
	proj := t.TempDir() // project root
	p := NewPromoter(home, proj)

	globalStaging := filepath.Join(home, ".ggcode", "skills-staging")
	mustWrite(t, filepath.Join(globalStaging, "knight-x.md"), "# global content")

	entry := &SkillEntry{
		Name:    "crossed",
		Scope:   "project", // claims project...
		Staging: true,
		Path:    filepath.Join(globalStaging, "knight-x.md"), // ...but lives in global
	}
	err := p.Promote(entry)
	if err == nil {
		t.Fatal("expected Promote to reject a staging path outside the declared scope's staging dir")
	}
	if data, rerr := os.ReadFile(filepath.Join(globalStaging, "knight-x.md")); rerr != nil || string(data) != "# global content" {
		t.Errorf("global staging source was deleted or mutated by rejected Promote: read=%v", rerr)
	}
	// And nothing should have been created in project active either.
	if _, serr := os.Stat(filepath.Join(proj, ".ggcode", "skills", "crossed", "SKILL.md")); serr == nil {
		t.Error("rejected promote must not create an active skill")
	}
}

// d2 companion: a legitimate same-scope promote still succeeds.
func Test3656PromoteSameScopeSucceeds(t *testing.T) {
	proj := t.TempDir()
	p := NewPromoter(proj, proj)
	stagingPath := filepath.Join(proj, ".ggcode", "skills-staging", "knight-ok.md")
	mustWrite(t, stagingPath, "# ok content")

	entry := &SkillEntry{Name: "ok-skill", Scope: "project", Staging: true, Path: stagingPath}
	if err := p.Promote(entry); err != nil {
		t.Fatalf("legitimate promote failed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(proj, ".ggcode", "skills", "ok-skill", "SKILL.md")); err != nil {
		t.Errorf("active skill missing after promote: %v", err)
	}
}

// d3: ValidateSkill(nil) must return an invalid result instead of panicking
// in checkDependencies (which dereferences entry.Meta.Requires).
func Test3656ValidateSkillNilNoPanic(t *testing.T) {
	res := ValidateSkill(nil)
	if res.Valid {
		t.Error("nil entry must not validate")
	}
	if len(res.Errors) == 0 {
		t.Error("expected at least one error for nil entry")
	}
}
