package knight

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// #3656 defect 1: WriteStaging must trim the skill name before building the
// staging filename — a leading-space name previously validated (validation
// ran on the trimmed form) but wrote "knight-20261009- deploy-web.md".
func Test3656WriteStagingTrimsNameInFilename(t *testing.T) {
	home, proj := t.TempDir(), t.TempDir()
	p := NewPromoter(home, proj)

	path, err := p.WriteStaging(" deploy-web ", "project", "---\nname: deploy-web\ndescription: d\n")
	if err != nil {
		t.Fatalf("WriteStaging with padded name: %v", err)
	}
	base := filepath.Base(path)
	if strings.TrimSpace(base) != base {
		t.Fatalf("staging filename carries untrimmed whitespace: %q", base)
	}
	if !strings.Contains(base, "deploy-web") || strings.Contains(base, " deploy") {
		t.Fatalf("unexpected staging filename: %q", base)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("staging file not written at trimmed path: %v", err)
	}
}

// #3656 defect 2: Promote must refuse a staging entry whose Path lives in a
// DIFFERENT scope's staging dir (or anywhere outside the declared scope's
// staging root) — previously it copied the file and deleted the foreign
// original, losing data.
func Test3656PromoteRefusesCrossScopePath(t *testing.T) {
	home, proj := t.TempDir(), t.TempDir()
	p := NewPromoter(home, proj)

	// Seed a file in the GLOBAL staging dir.
	globalStaging := filepath.Join(home, ".ggcode", "skills-staging")
	if err := os.MkdirAll(globalStaging, 0755); err != nil {
		t.Fatal(err)
	}
	foreign := filepath.Join(globalStaging, "knight-20261009-victim.md")
	if err := os.WriteFile(foreign, []byte("---\nname: victim\n---\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// Cross-wired entry: Scope says project, Path is under global staging.
	entry := &SkillEntry{Name: "victim", Scope: "project", Staging: true, Path: foreign}
	err := p.Promote(entry)
	if err == nil {
		t.Fatal("Promote accepted a cross-scope staging path")
	}
	if !strings.Contains(err.Error(), "outside") {
		t.Fatalf("unexpected error: %v", err)
	}
	// The foreign original must still exist — no data loss.
	if _, statErr := os.Stat(foreign); statErr != nil {
		t.Fatalf("foreign staging original was deleted despite refusal: %v", statErr)
	}
	// And no half-promoted copy may exist in the project active tree.
	if _, statErr := os.Stat(filepath.Join(proj, ".ggcode", "skills", "victim", "SKILL.md")); statErr == nil {
		t.Fatal("cross-scope promote left a copy in the project active tree")
	}
}

// #3656 defect 3: ValidateSkill(nil) previously panicked in
// checkDependencies (entry.Meta.Requires deref) while checkFormat guarded.
func Test3656ValidateSkillNilNoPanic(t *testing.T) {
	res := ValidateSkill(nil)
	if res.Valid {
		t.Fatal("ValidateSkill(nil) reported valid")
	}
	if len(res.Errors) == 0 {
		t.Fatal("ValidateSkill(nil) reported no errors")
	}
}

// Promote(nil) previously panicked at entry.Staging; sibling guards (#1580-D)
// already cover Reject, Promote now matches.
func Test3656PromoteNilNoPanic(t *testing.T) {
	p := NewPromoter(t.TempDir(), t.TempDir())
	if err := p.Promote(nil); err == nil {
		t.Fatal("Promote(nil) returned nil error")
	}
}
