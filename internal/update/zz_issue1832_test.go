package update

// #1832 case 1 regression: ApplyBinary (Unix self-update path) must back up
// each target before replacing it and roll back earlier targets when a later
// one fails - mirroring RunHelper's #1402/#1423 contract. Previously it
// wrote every target bare, so a failure on the second of two targets
// (npm/python wrapper installs) left the first already overwritten: a
// half-updated install with no repair entry point.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeManifest1832(t *testing.T, dir, sourceName string, targets []string) PreparedUpdate {
	t.Helper()
	source := filepath.Join(dir, sourceName)
	if err := os.WriteFile(source, []byte("new-binary"), 0o755); err != nil {
		t.Fatalf("write source: %v", err)
	}
	manifest := HelperManifest{SourceBinary: source, TargetPaths: targets}
	data, _ := json.Marshal(manifest)
	mp := filepath.Join(dir, "manifest.json")
	if err := os.WriteFile(mp, data, 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	return PreparedUpdate{ManifestPath: mp}
}

// TestApplyBinaryRollsBackEarlierTargetsOnFailure: target 2's parent is a
// regular file, so its MkdirAll fails AFTER target 1 was already replaced -
// target 1's original bytes must be restored.
func TestApplyBinaryRollsBackEarlierTargetsOnFailure(t *testing.T) {
	dir := t.TempDir()
	target1 := filepath.Join(dir, "bin", "ggcode")
	if err := os.MkdirAll(filepath.Dir(target1), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target1, []byte("old-binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	// A regular FILE where target 2's parent directory should be - MkdirAll
	// fails deterministically.
	blocker := filepath.Join(dir, "blocked")
	if err := os.WriteFile(blocker, []byte("i am a file"), 0o644); err != nil {
		t.Fatal(err)
	}
	target2 := filepath.Join(blocker, "version", "ggcode")

	s := &Service{}
	prepared := writeManifest1832(t, dir, "staged.bin", []string{target1, target2})

	err := s.ApplyBinary(prepared)
	if err == nil {
		t.Fatal("ApplyBinary must fail when a target dir cannot be created")
	}
	if !strings.Contains(err.Error(), "rolled back") && !strings.Contains(err.Error(), "rollback") {
		t.Fatalf("error should mention rollback, got: %v", err)
	}
	restored, rerr := os.ReadFile(target1)
	if rerr != nil {
		t.Fatalf("target1 unreadable after rollback: %v", rerr)
	}
	if string(restored) != "old-binary" {
		t.Fatalf("target1 not rolled back: got %q, want %q", restored, "old-binary")
	}
}

// TestApplyBinaryRollsBackFreshTarget: a target that did not exist before
// the run must be REMOVED on rollback, not left as a half-new file.
func TestApplyBinaryRollsBackFreshTarget(t *testing.T) {
	dir := t.TempDir()
	target1 := filepath.Join(dir, "ggcode-new") // did not exist before
	blocker := filepath.Join(dir, "blocked")
	if err := os.WriteFile(blocker, []byte("file"), 0o644); err != nil {
		t.Fatal(err)
	}
	target2 := filepath.Join(blocker, "sub", "ggcode")

	s := &Service{}
	prepared := writeManifest1832(t, dir, "staged.bin", []string{target1, target2})

	if err := s.ApplyBinary(prepared); err == nil {
		t.Fatal("ApplyBinary must fail")
	}
	if _, err := os.Stat(target1); !os.IsNotExist(err) {
		t.Fatalf("fresh target1 must be removed on rollback, stat err = %v", err)
	}
}

// TestApplyBinaryAllTargetsSucceed: happy path - two targets, both replaced.
func TestApplyBinaryAllTargetsSucceed(t *testing.T) {
	dir := t.TempDir()
	target1 := filepath.Join(dir, "a", "ggcode")
	target2 := filepath.Join(dir, "b", "v2", "ggcode")

	s := &Service{}
	prepared := writeManifest1832(t, dir, "staged.bin", []string{target1, target2})

	if err := s.ApplyBinary(prepared); err != nil {
		t.Fatalf("ApplyBinary() error = %v", err)
	}
	for _, p := range []string{target1, target2} {
		got, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("read %s: %v", p, err)
		}
		if string(got) != "new-binary" {
			t.Fatalf("%s = %q, want %q", p, got, "new-binary")
		}
	}
}
