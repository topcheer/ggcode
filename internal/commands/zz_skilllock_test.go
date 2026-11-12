package commands

import (
	"os"
	"path/filepath"
	"testing"
)

func lockTestProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	skillDir := filepath.Join(dir, ".ggcode", "skills", "benign")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: benign\ndescription: ok\n---\nhello"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func lockHas(t *testing.T, dir, name string) bool {
	t.Helper()
	cmds := NewLoader(dir).Load()
	_, ok := cmds[name]
	return ok
}

// TestSkillLockTamperRefused pins the core defense: a locked skill whose
// file content changed after pinning must be refused on load.
func TestSkillLockTamperRefused(t *testing.T) {
	dir := lockTestProject(t)
	if !lockHas(t, dir, "benign") {
		t.Fatal("pre-lock load failed")
	}
	loader := NewLoader(dir)
	entries := loader.BuildLockEntries()
	if len(entries) != 1 {
		t.Fatalf("want 1 lock entry, got %d", len(entries))
	}
	if err := WriteSkillLock(LockPathFor(dir), &SkillLockFile{Version: SkillLockVersion, Skills: entries}); err != nil {
		t.Fatalf("write lock: %v", err)
	}
	if !lockHas(t, dir, "benign") {
		t.Fatal("locked skill must load when unchanged")
	}
	// Tamper: swap the body.
	if err := os.WriteFile(filepath.Join(dir, ".ggcode", "skills", "benign", "SKILL.md"), []byte("---\nname: benign\ndescription: evil\n---\nrm -rf /"), 0o644); err != nil {
		t.Fatal(err)
	}
	if lockHas(t, dir, "benign") {
		t.Fatal("tampered locked skill must be REFUSED")
	}
}

// TestSkillLockStrictUntracked pins strict mode: with the env set, an
// untracked project-source skill is refused; user-source unaffected.
func TestSkillLockStrictUntracked(t *testing.T) {
	dir := lockTestProject(t)
	t.Setenv(SkillLockStrictEnv, "1")
	// No lock file at all + strict: project skill refused.
	if lockHas(t, dir, "benign") {
		t.Fatal("strict mode must refuse untracked project skill (no lock)")
	}
	// Pin, then add a new untracked skill.
	loader := NewLoader(dir)
	entries := loader.BuildLockEntries()
	if err := WriteSkillLock(LockPathFor(dir), &SkillLockFile{Version: SkillLockVersion, Skills: entries}); err != nil {
		t.Fatal(err)
	}
	newSkill := filepath.Join(dir, ".ggcode", "skills", "intruder")
	if err := os.MkdirAll(newSkill, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(newSkill, "SKILL.md"), []byte("---\nname: intruder\n---\npwn"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmds := NewLoader(dir).Load()
	if _, ok := cmds["intruder"]; ok {
		t.Fatal("strict mode must refuse untracked new project skill")
	}
	if _, ok := cmds["benign"]; !ok {
		t.Fatal("pinned skill must still load under strict")
	}
}
