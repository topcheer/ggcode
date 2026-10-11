package tool

// #3951 companion test (bidirectional):
// - Move to outside sandbox -> IsError, zero write, zero source deletion
// - Move to inside sandbox  -> normal move, no regression

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func setupMoveToSandbox(t *testing.T) (string, string, ApplyPatch) {
	t.Helper()
	base := t.TempDir()
	sandbox := filepath.Join(base, "sandbox")
	if err := os.MkdirAll(sandbox, 0o755); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(sandbox, "a.txt")
	if err := os.WriteFile(src, []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tt := ApplyPatch{
		WorkingDir: sandbox,
		SandboxCheck: func(p string) bool {
			rel, err := filepath.Rel(sandbox, p)
			return err == nil && rel == "a.txt"
		},
	}
	return base, sandbox, tt
}

func moveToInput(t *testing.T, target string) json.RawMessage {
	input, err := json.Marshal(map[string]string{
		"type": "update_file",
		"path": "a.txt",
		"diff": "*** Begin Patch\n*** Update File: a.txt\n*** Move to: " + target + "\n@@\n-hello\n+hello world\n*** End Patch\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	return input
}

func TestIssue3951_MoveToOutsideSandboxRejected(t *testing.T) {
	base, sandbox, tt := setupMoveToSandbox(t)
	res, err := tt.Execute(context.Background(), moveToInput(t, "../../outside/evil.txt"))
	if err != nil {
		t.Fatalf("execute err: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected IsError for outside move target, got: %s", res.Content)
	}
	// source must be intact (no deletion)
	if _, statErr := os.Stat(filepath.Join(sandbox, "a.txt")); statErr != nil {
		t.Fatalf("source deleted despite rejection: %v", statErr)
	}
	// nothing written outside sandbox (search upward like the original probe)
	for d := base; d != "/" && d != "."; d = filepath.Dir(d) {
		if _, statErr := os.Stat(filepath.Join(d, "outside", "evil.txt")); statErr == nil {
			t.Fatalf("escape wrote file at %s despite rejection", filepath.Join(d, "outside", "evil.txt"))
		}
	}
}

func TestIssue3951_MoveToInsideSandboxStillWorks(t *testing.T) {
	_, sandbox, tt := setupMoveToSandbox(t)
	// inside move: relocate within sandbox (SandboxCheck allows only a.txt in
	// this fixture, so use a checker that allows the whole sandbox instead)
	tt.SandboxCheck = func(p string) bool {
		rel, err := filepath.Rel(sandbox, p)
		return err == nil && rel == "a.txt" || (err == nil && rel == filepath.Join("sub", "moved.txt"))
	}
	res, err := tt.Execute(context.Background(), moveToInput(t, "sub/moved.txt"))
	if err != nil {
		t.Fatalf("execute err: %v", err)
	}
	if res.IsError {
		t.Fatalf("inside move should succeed, got: %s", res.Content)
	}
	data, readErr := os.ReadFile(filepath.Join(sandbox, "sub", "moved.txt"))
	if readErr != nil {
		t.Fatalf("moved file missing: %v", readErr)
	}
	if string(data) != "hello world\n" {
		t.Fatalf("moved content = %q, want updated content", string(data))
	}
	if _, statErr := os.Stat(filepath.Join(sandbox, "a.txt")); statErr == nil {
		t.Fatalf("source still present after successful move")
	}
}
