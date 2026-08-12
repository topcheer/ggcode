package agentruntime

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/commands"
)

func TestNewSkillHotReload(t *testing.T) {
	mgr := commands.NewManager(t.TempDir())
	w := NewSkillHotReload(mgr, []string{"/tmp/skills"})
	if w == nil {
		t.Fatal("expected non-nil watcher")
	}
	if w.interval != 5*time.Second {
		t.Fatalf("expected default interval 5s, got %v", w.interval)
	}
}

func TestSkillHotReloadComputeSignature(t *testing.T) {
	dir := t.TempDir()
	skillDir := filepath.Join(dir, "my-skill")
	_ = os.MkdirAll(skillDir, 0o755)
	skillFile := filepath.Join(skillDir, "SKILL.md")
	_ = os.WriteFile(skillFile, []byte("---\nname: my-skill\n---\nbody"), 0o644)

	mgr := commands.NewManager(dir)
	w := NewSkillHotReload(mgr, []string{dir})

	sig1 := w.computeSignature()
	if sig1 == "" {
		t.Fatal("expected non-empty signature")
	}

	// Same files → same signature.
	sig2 := w.computeSignature()
	if sig1 != sig2 {
		t.Fatal("expected identical signatures for unchanged files")
	}

	// Modify file content (must change mtime).
	time.Sleep(10 * time.Millisecond)
	_ = os.WriteFile(skillFile, []byte("---\nname: my-skill\n---\nmodified body"), 0o644)

	sig3 := w.computeSignature()
	if sig3 == sig1 {
		t.Fatal("expected different signature after file modification")
	}
}

func TestSkillHotReloadReloadsOnAdd(t *testing.T) {
	dir := t.TempDir()
	mgr := commands.NewManager(dir)
	w := NewSkillHotReload(mgr, []string{dir})

	sigBefore := w.computeSignature()

	// Add a new skill directory + SKILL.md.
	newSkill := filepath.Join(dir, "added-skill")
	_ = os.MkdirAll(newSkill, 0o755)
	_ = os.WriteFile(filepath.Join(newSkill, "SKILL.md"),
		[]byte("---\nname: added-skill\ndescription: test\n---\nhello"), 0o644)

	sigAfter := w.computeSignature()
	if sigAfter == sigBefore {
		t.Fatal("expected different signature after adding a skill")
	}
}

func TestSkillHotReloadReloadsOnDelete(t *testing.T) {
	dir := t.TempDir()
	skillDir := filepath.Join(dir, "doomed-skill")
	_ = os.MkdirAll(skillDir, 0o755)
	skillFile := filepath.Join(skillDir, "SKILL.md")
	_ = os.WriteFile(skillFile, []byte("---\nname: doomed-skill\n---\nbody"), 0o644)

	mgr := commands.NewManager(dir)
	w := NewSkillHotReload(mgr, []string{dir})

	sigBefore := w.computeSignature()
	_ = os.RemoveAll(skillDir)

	sigAfter := w.computeSignature()
	if sigAfter == sigBefore {
		t.Fatal("expected different signature after deleting a skill")
	}
}

func TestSkillHotReloadDetectsAndReloads(t *testing.T) {
	projectDir := t.TempDir()
	mgr := commands.NewManager(projectDir)
	// Override interval for fast test.
	w := &SkillHotReload{
		manager:  mgr,
		dirs:     mgr.WatchedDirs(),
		interval: 100 * time.Millisecond,
	}
	if len(w.dirs) == 0 {
		t.Fatal("expected non-empty watched dirs")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	w.Start(ctx)

	// Find the project-local skills dir (e.g. projectDir/.ggcode/skills).
	skillsDir := filepath.Join(projectDir, ".ggcode", "skills")
	if err := os.MkdirAll(skillsDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Add a new skill after Start — should be picked up after reload.
	newDir := filepath.Join(skillsDir, "added-later")
	if err := os.MkdirAll(newDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(newDir, "SKILL.md"),
		[]byte("---\nname: added-later\ndescription: added\n---\nbody"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Poll for up to 3 seconds for the skill to appear.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, ok := mgr.Get("added-later"); ok {
			return // success
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("expected 'added-later' skill to be visible after hot-reload within 3s")
}

func TestSkillHotReloadStartNilManager(t *testing.T) {
	// Should not panic with nil manager.
	w := NewSkillHotReload(nil, []string{"/tmp"})
	w.Start(context.Background())
}

func TestSkillHotReloadStartEmptyDirs(t *testing.T) {
	mgr := commands.NewManager(t.TempDir())
	w := NewSkillHotReload(mgr, nil)
	w.Start(context.Background()) // should be a no-op
}
