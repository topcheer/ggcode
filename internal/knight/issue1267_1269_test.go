package knight

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestPromoteAbortsWhenSnapshotFails(t *testing.T) {
	proj := t.TempDir()
	p := NewPromoter(proj, proj)

	oldSkill := filepath.Join(proj, ".ggcode", "skills", "my-skill", "SKILL.md")
	mustWrite(t, oldSkill, "old active content")

	stagingPath := filepath.Join(proj, ".ggcode", "skills-staging", "my-skill", "SKILL.md")
	mustWrite(t, stagingPath, "new staging content")
	entry := &SkillEntry{Name: "my-skill", Scope: "project", Staging: true, Path: stagingPath}

	mustWrite(t, filepath.Join(proj, ".ggcode", "skills-snapshots"), "blocker")

	err := p.Promote(entry)
	if err == nil {
		t.Fatal("#1267: Promote must abort when the rollback snapshot fails")
	}
	if !strings.Contains(err.Error(), "refusing to overwrite") {
		t.Fatalf("expected explicit abort reason, got: %v", err)
	}

	data, rerr := os.ReadFile(oldSkill)
	if rerr != nil {
		t.Fatal(rerr)
	}
	if string(data) != "old active content" {
		t.Fatalf("old active version was overwritten despite snapshot failure: %q", data)
	}
}

func TestRollbackAbortsWhenSnapshotFails(t *testing.T) {
	proj := t.TempDir()
	p := NewPromoter(proj, proj)

	active := filepath.Join(proj, ".ggcode", "skills", "my-skill", "SKILL.md")
	mustWrite(t, active, "current version")

	snapDir := filepath.Join(proj, ".ggcode", "skills-snapshots")
	mustWrite(t, filepath.Join(snapDir, "my-skill.20260101-000000000000.md"), "historic version")
	if err := os.Chmod(snapDir, 0500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(snapDir, 0755) })

	entry := &SkillEntry{Name: "my-skill", Scope: "project", Staging: false, Path: active}
	err := p.Rollback(entry)
	if err == nil {
		t.Fatal("#1267: Rollback must abort when the pre-rollback snapshot fails")
	}
	if !strings.Contains(err.Error(), "refusing to overwrite") {
		t.Fatalf("expected explicit abort reason, got: %v", err)
	}
	data, rerr := os.ReadFile(active)
	if rerr != nil {
		t.Fatal(rerr)
	}
	if string(data) != "current version" {
		t.Fatalf("current version was lost despite snapshot failure: %q", data)
	}
}

func TestSnapshotSameSecondNotOverwritten(t *testing.T) {
	proj := t.TempDir()
	p := NewPromoter(proj, proj)

	active := filepath.Join(proj, ".ggcode", "skills", "s", "SKILL.md")
	mustWrite(t, active, "v1")
	if err := p.createSnapshot("s", active); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, active, "v2")
	if err := p.createSnapshot("s", active); err != nil {
		t.Fatal(err)
	}

	snaps, err := p.listSnapshots("s")
	if err != nil {
		t.Fatal(err)
	}
	if len(snaps) != 2 {
		t.Fatalf("#1268: same-second snapshots must both survive, listed %d: %v", len(snaps), snaps)
	}

	seen := map[string]bool{}
	for _, s := range snaps {
		data, err := os.ReadFile(s)
		if err != nil {
			t.Fatal(err)
		}
		seen[string(data)] = true
	}
	if !seen["v1"] || !seen["v2"] {
		t.Fatalf("snapshot history lost a version: %v", seen)
	}
}

func TestScanConcurrentWithInvalidate(t *testing.T) {
	home := t.TempDir()
	proj := t.TempDir()
	mustWrite(t, filepath.Join(proj, ".ggcode", "skills", "alpha", "SKILL.md"),
		"---\nname: alpha\ndescription: alpha skill\n---\n\nbody\n")

	si := NewSkillIndex(home, proj)
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				if _, err := si.Scan(); err != nil {
					t.Error(err)
					return
				}
				si.Invalidate()
			}
		}()
	}
	wg.Wait()

	si.Invalidate()
	entries, err := si.Scan()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range entries {
		if e.Name == "alpha" && !e.Staging {
			found = true
		}
	}
	if !found {
		t.Fatalf("#1269: post-invalidation scan lost the on-disk skill: %+v", entries)
	}
}

func TestScanStaleWriteBackDiscarded(t *testing.T) {
	home := t.TempDir()
	proj := t.TempDir()
	si := NewSkillIndex(home, proj)

	si.mu.RLock()
	staleEpoch := si.epoch
	si.mu.RUnlock()

	si.Invalidate()

	si.mu.Lock()
	if si.epoch == staleEpoch {
		si.cache = []*SkillEntry{{Name: "stale-ghost"}}
		si.cacheTime = time.Now()
	}
	si.mu.Unlock()
	if si.cache != nil {
		t.Fatal("guard failed: stale scan result was published after Invalidate")
	}
}
