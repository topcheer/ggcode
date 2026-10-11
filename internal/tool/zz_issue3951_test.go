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

func mustJSON3951(t *testing.T, path, diff string) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(map[string]string{"path": path, "diff": diff})
	if err != nil {
		t.Fatal(err)
	}
	return b
}
