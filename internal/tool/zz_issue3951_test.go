package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// #3951 probes: "Move to:" relocation must not bypass SandboxCheck.

func zz3951Tool(t *testing.T, sandboxDir string) *ApplyPatch {
	t.Helper()
	return &ApplyPatch{
		WorkingDir: sandboxDir,
		SandboxCheck: func(p string) bool {
			// Allow only paths inside sandboxDir.
			rel, err := filepath.Rel(sandboxDir, p)
			return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
		},
	}
}

// Escape probe: ../../outside/evil.txt must be REJECTED before any write.
func TestIssue3951_MoveToEscapeRejected(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "sandbox"), 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "sandbox", "a.txt"), []byte("line\n"), 0o644)
	ap := zz3951Tool(t, filepath.Join(dir, "sandbox"))
	diff := "*** Update File: a.txt\n@@\n-line\n+renamed\n*** Move to: ../../outside/evil.txt\n"
	res, err := ap.Execute(context.Background(), mustJSON3951(t, "sandbox/a.txt", diff))
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError || !strings.Contains(res.Content, "sandbox") {
		t.Fatalf("escape move-to must be rejected, got: %+v", res)
	}
	// No file may exist outside the sandbox.
	if _, statErr := os.Stat(filepath.Join(dir, "outside", "evil.txt")); statErr == nil {
		t.Fatal("file escaped the sandbox")
	}
	// Source must still exist (rejected before apply).
	if _, statErr := os.Stat(filepath.Join(dir, "sandbox", "a.txt")); statErr != nil {
		t.Fatalf("source was deleted on a rejected move: %v", statErr)
	}
}

// Legit in-sandbox move must keep working.
func TestIssue3951_MoveToWithinSandboxAllowed(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("line\n"), 0o644)
	ap := zz3951Tool(t, dir)
	diff := "*** Update File: a.txt\n@@\n-line\n+renamed\n*** Move to: b.txt\n"
	res, err := ap.Execute(context.Background(), mustJSON3951(t, "a.txt", diff))
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("in-sandbox move must succeed: %s", res.Content)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "b.txt")); statErr != nil {
		t.Fatalf("renamed target missing: %v", statErr)
	}
}

// Same-formula guard: the checked target equals the written target (no drift).
func TestIssue3951_CheckedTargetMatchesWrittenFormula(t *testing.T) {
	// The check computes Clean(Join(Dir(resolved), moveTo)) - identical to
	// applyV4ASection's write target. A moveTo that stays inside after the
	// join (e.g. sub/../../ok.txt resolves inside) must pass.
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "sub"), 0o755)
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("line\n"), 0o644)
	ap := zz3951Tool(t, dir)
	diff := "*** Update File: a.txt\n@@\n-line\n+ok\n*** Move to: sub/../c.txt\n"
	res, err := ap.Execute(context.Background(), mustJSON3951(t, "a.txt", diff))
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("join-form-legal move must pass: %s", res.Content)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "c.txt")); statErr != nil {
		t.Fatalf("target missing: %v", statErr)
	}
}

// Absolute-path probe: a "Move to:" that is an absolute path outside the
// sandbox must not escape. Note Go semantics: Clean(Join(dir, "/abs")) nests
// INSIDE dir (Join does not reset to the absolute root), so the written file
// must end up contained within the sandbox either way. The security contract
// is containment: nothing may land outside the sandbox root.
func TestIssue3951_MoveToAbsolutePathOutsideRejected(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(filepath.Dir(dir), "escape3951-abs")
	t.Cleanup(func() { os.RemoveAll(outside) })
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("line\n"), 0o644)
	ap := zz3951Tool(t, dir)
	diff := "*** Update File: a.txt\n@@\n-line\n+evil\n*** Move to: " + filepath.Join(outside, "evil.txt") + "\n"
	res, err := ap.Execute(context.Background(), mustJSON3951(t, "a.txt", diff))
	if err != nil {
		t.Fatal(err)
	}
	// Containment: the absolute target (outside the sandbox) must NOT receive the file.
	if _, statErr := os.Stat(filepath.Join(outside, "evil.txt")); statErr == nil {
		t.Fatalf("absolute-path escape wrote a file outside the sandbox: %+v", res)
	}
	if res.IsError {
		// Rejected: source must be intact.
		if _, statErr := os.Stat(filepath.Join(dir, "a.txt")); statErr != nil {
			t.Fatalf("source was deleted on a rejected move: %v", statErr)
		}
		return
	}
	// Succeeded: content must be somewhere INSIDE the sandbox root.
	found := false
	filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && d.Name() == "evil.txt" {
			found = true
		}
		return nil
	})
	if !found {
		t.Fatal("move reported success but evil.txt not found inside sandbox (containment violated or file lost)")
	}
}

func mustJSON3951(t *testing.T, path, diff string) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(map[string]string{"path": path, "diff": diff})
	if err != nil {
		t.Fatal(err)
	}
	return b
}
