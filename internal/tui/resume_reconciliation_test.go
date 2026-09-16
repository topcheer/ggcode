package tui

// Tests for resume reconciliation: environment fingerprint parsing, drift
// diffing, note composition, and the model-facing note store.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParsePorcelainPaths(t *testing.T) {
	out := "M  a.go\x00M  b.go\x00?? new.txt\x00R  renamed.go\x00old.go\x00\x00"
	got := parsePorcelainPaths(out)
	want := []string{"a.go", "b.go", "new.txt", "renamed.go", "old.go"}
	if len(got) != len(want) {
		t.Fatalf("got %v (%d entries), want %v", got, len(got), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("entry %d: got %q, want %q", i, got[i], want[i])
		}
	}
}

func TestDecodeEnvFingerprint(t *testing.T) {
	if fp := decodeEnvFingerprint(nil); fp != nil {
		t.Errorf("nil data should decode to nil, got %+v", fp)
	}
	if fp := decodeEnvFingerprint([]byte("not json")); fp != nil {
		t.Errorf("corrupt data should decode to nil, got %+v", fp)
	}
	valid, err := time.Now().MarshalText()
	if err != nil {
		t.Fatal(err)
	}
	data := []byte(`{"captured_at":"` + string(valid) + `","branch":"main","head":"abc1234","dirty_files":["a.go"],"dirty_total":1}`)
	fp := decodeEnvFingerprint(data)
	if fp == nil {
		t.Fatal("valid data should decode")
	}
	if fp.Branch != "main" || fp.Head != "abc1234" || len(fp.DirtyFiles) != 1 || fp.DirtyTotal != 1 {
		t.Errorf("unexpected decode result: %+v", fp)
	}
}

func TestDiffEnvFingerprints(t *testing.T) {
	now := time.Now()
	base := &envFingerprint{
		CapturedAt: now,
		Branch:     "feat/x",
		Head:       "1111111aaa",
		DirtyFiles: []string{"a.go", "b.go", "c.go"},
		DirtyTotal: 3,
	}

	// nil cases: nothing to compare, no findings.
	if f := diffEnvFingerprints(nil, base); len(f) != 0 {
		t.Errorf("nil prev should yield no findings, got %v", f)
	}

	// Identical: no drift.
	if f := diffEnvFingerprints(base, base); len(f) != 0 {
		t.Errorf("identical fingerprints should yield no findings, got %v", f)
	}

	cur := &envFingerprint{
		CapturedAt: now.Add(time.Hour),
		Branch:     "main",
		Head:       "2222222bbb",
		DirtyFiles: []string{"b.go", "d.go"},
		DirtyTotal: 2,
	}
	f := diffEnvFingerprints(base, cur)
	joined := strings.Join(f, "\n")
	for _, want := range []string{"branch switched: feat/x → main", "HEAD moved: 1111111 → 2222222", "dirty at snapshot, clean now", "a.go, c.go", "newly modified since snapshot", "d.go"} {
		if !strings.Contains(joined, want) {
			t.Errorf("findings missing %q; got:\n%s", want, joined)
		}
	}
	// b.go stayed dirty on both sides - must not appear in either drift list.
	if strings.Contains(joined, "b.go") {
		t.Errorf("b.go is dirty in both snapshots and should not be reported")
	}
}

func TestDiffEnvFingerprintsPathCaps(t *testing.T) {
	now := time.Now()
	prev := &envFingerprint{CapturedAt: now, DirtyFiles: []string{"p1", "p2", "p3", "p4", "p5", "p6", "p7", "p8", "p9", "p10"}}
	cur := &envFingerprint{CapturedAt: now, DirtyFiles: []string{"p10"}}
	f := diffEnvFingerprints(prev, cur)
	if len(f) != 1 {
		t.Fatalf("want 1 finding (resolved only), got %v", f)
	}
	if !strings.Contains(f[0], "(+1 more)") {
		t.Errorf("long list should be capped with +N more, got %q", f[0])
	}
}

func TestBuildResumeReconciliationNote(t *testing.T) {
	if note := buildResumeReconciliationNote("1 completed", nil); note != "" {
		t.Errorf("no drift should produce an empty note, got %q", note)
	}
	note := buildResumeReconciliationNote("2 completed, 1 in progress, 0 pending", []string{"branch switched: a → b"})
	if !strings.Contains(note, "**Resume reconciliation**") || !strings.Contains(note, "branch switched") || !strings.Contains(note, "Re-verify") {
		t.Errorf("note missing expected sections:\n%s", note)
	}
}

func TestResumeNoteStoreNilSafety(t *testing.T) {
	m := &Model{} // resumeNote lazily initialized
	if got := m.resumeNoteValue(); got != "" {
		t.Errorf("fresh model should have empty note, got %q", got)
	}
	m.setResumeNote("drift note")
	if got := m.resumeNoteValue(); got != "drift note" {
		t.Errorf("roundtrip failed, got %q", got)
	}
	(*Model)(nil).setResumeNote("ignored") // must not panic
}

// captureEnvFingerprintInRepo is a helper that creates a real git repo with
// one commit and returns its fingerprint; skipped when git is unavailable.
func captureEnvFingerprintInRepo(t *testing.T) (*envFingerprint, string) {
	t.Helper()
	git := exec.Command("git", "--version")
	if err := git.Run(); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.email=t@t", "-c", "user.name=t"}, args...)...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	run("init", "-q")
	if err := os.WriteFile(filepath.Join(dir, "tracked.txt"), []byte("v1"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-qm", "init")
	if err := os.WriteFile(filepath.Join(dir, "tracked.txt"), []byte("v2"), 0o644); err != nil {
		t.Fatal(err)
	}
	fp := captureEnvFingerprint(dir)
	if fp == nil {
		t.Fatal("expected a fingerprint for a real git repo")
	}
	if fp.Branch == "" || fp.Head == "" {
		t.Errorf("fingerprint missing branch/head: %+v", fp)
	}
	if fp.DirtyTotal != 1 || len(fp.DirtyFiles) != 1 || fp.DirtyFiles[0] != "tracked.txt" {
		t.Errorf("unexpected dirty set: %+v", fp)
	}
	return fp, dir
}

func TestCaptureEnvFingerprint(t *testing.T) {
	captureEnvFingerprintInRepo(t)
}

func TestCaptureEnvFingerprintNonGit(t *testing.T) {
	// A dir outside any repo (parent of temp dir has no .git) should yield nil.
	if fp := captureEnvFingerprint(t.TempDir()); fp != nil {
		// Only acceptable if the environment has a git repo above TMPDIR
		// (some CI sandboxes do); in that case branch must still be real.
		if fp.Branch == "" && fp.Head == "" && fp.DirtyTotal == 0 {
			t.Errorf("empty fingerprint should have been nil: %+v", fp)
		}
	}
}
