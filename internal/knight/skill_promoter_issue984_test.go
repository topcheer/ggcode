package knight

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRollbackDottedSkillNameNoCrosstalk(t *testing.T) {
	home := t.TempDir()
	proj := t.TempDir()
	p := NewPromoter(home, proj)

	snapDir := filepath.Join(proj, ".ggcode", "skills-snapshots")
	if err := os.MkdirAll(snapDir, 0755); err != nil {
		t.Fatal(err)
	}

	writeSnap := func(name, ts, content string) {
		t.Helper()
		path := filepath.Join(snapDir, name+"."+ts+".md")
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}

	writeSnap("a", "20260101-000001", "content-A1")
	writeSnap("a", "20260101-235959", "content-A2")

	writeSnap("a.b", "20250101-000000", "content-B")

	snaps, err := p.listSnapshots("a", "project")
	if err != nil {
		t.Fatal(err)
	}
	if len(snaps) != 2 {
		t.Fatalf("listSnapshots(a) = %d snapshots, want 2: %v", len(snaps), snaps)
	}
	for _, s := range snaps {
		if strings.Contains(filepath.Base(s), "a.b.") {
			t.Fatalf("listSnapshots(a) matched sibling snapshot %s", s)
		}
	}

	skillDir := filepath.Join(proj, ".ggcode", "skills", "a")
	activePath := filepath.Join(skillDir, "SKILL.md")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(activePath, []byte("current-a"), 0644); err != nil {
		t.Fatal(err)
	}

	entry := &SkillEntry{Name: "a", Path: activePath, Scope: "project"}
	if err := p.Rollback(entry); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(activePath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "content-A2") {
		t.Fatalf("Rollback(a) restored wrong content %q, want a's own latest snapshot content-A2", string(data))
	}
	if strings.Contains(string(data), "content-B") {
		t.Fatalf("Rollback(a) restored sibling skill a.b content: %q", string(data))
	}
}

func TestAppendSkillScenarioNoLossOnRepeatedAppend(t *testing.T) {
	proj := t.TempDir()
	k := &Knight{projDir: proj}

	first := SkillScenarioLogEntry{Time: time.Now(), Task: "task-one", SkillRefs: []string{"s1"}, Success: true}
	if err := k.appendSkillScenario(first); err != nil {
		t.Fatal(err)
	}
	second := SkillScenarioLogEntry{Time: time.Now(), Task: "task-two", SkillRefs: []string{"s2"}, Success: false, Error: "boom"}
	if err := k.appendSkillScenario(second); err != nil {
		t.Fatal(err)
	}

	entries, err := readSkillScenarios(k.skillScenarioLogPath())
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("readSkillScenarios returned %d entries, want 2", len(entries))
	}
	var sawOne, sawTwo bool
	for _, e := range entries {
		if e.Task == "task-one" {
			sawOne = true
		}
		if e.Task == "task-two" {
			sawTwo = true
		}
	}
	if !sawOne || !sawTwo {
		t.Fatalf("lost entry after sequential appends: sawOne=%v sawTwo=%v entries=%+v", sawOne, sawTwo, entries)
	}

	recent, err := k.RecentSkillScenarios(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(recent) != 2 || recent[0].Task != "task-two" {
		t.Fatalf("RecentSkillScenarios order/content wrong: %+v", recent)
	}
}

func TestPromoteStagingRemoveIdempotent(t *testing.T) {
	home := t.TempDir()
	proj := t.TempDir()
	p := NewPromoter(home, proj)

	stagingDir := p.stagingDir("project")
	if err := os.MkdirAll(stagingDir, 0755); err != nil {
		t.Fatal(err)
	}
	stagingPath := filepath.Join(stagingDir, "knight-20260101-demo.md")
	if err := os.WriteFile(stagingPath, []byte("---\nname: demo\n---\nbody"), 0644); err != nil {
		t.Fatal(err)
	}

	entry := &SkillEntry{Name: "demo", Path: stagingPath, Scope: "project", Staging: true}
	if err := p.Promote(entry); err != nil {
		t.Fatalf("Promote failed: %v", err)
	}

	activePath := filepath.Join(p.activeDir(entry), "demo", "SKILL.md")
	if _, err := os.Stat(activePath); err != nil {
		t.Fatalf("active skill missing after promote: %v", err)
	}
	if _, err := os.Stat(stagingPath); !os.IsNotExist(err) {
		t.Fatalf("staging skill still present after promote: %v", err)
	}

	if err := removeStagingSkill(stagingPath); err != nil {
		t.Fatalf("removeStagingSkill on missing file should be idempotent, got: %v", err)
	}
}
