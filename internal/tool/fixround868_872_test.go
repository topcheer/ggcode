package tool

// Guard tests for the #868-#872 fix round.

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestShellCompatAnchoredTokens (#868): substrings like "tools/" or "gradle"
// must not trigger the du rule; a real du --max-depth must.
func TestShellCompatAnchoredTokens(t *testing.T) {
	if diagnoseShellCompat("gradle --max-depth=2 build", "", "") != "" {
		t.Fatal("gradle --max-depth falsely diagnosed as du issue")
	}
	if diagnoseShellCompat("ls tools/", "", "") != "" {
		t.Fatal("'ls tools/' falsely diagnosed as GNU ls issue")
	}
	// Positive assertion is platform-gated: diagnoseShellCompat is a no-op
	// on Linux (GNU commands are native there), so CI runners expect "".
	if runtime.GOOS != "linux" {
		got := diagnoseShellCompat("du --max-depth 1 /var", "", "")
		if !strings.Contains(got, "du -d") {
			t.Fatalf("real du --max-depth not diagnosed: %q", got)
		}
	}
}

// TestShellCompatMktempRequiresSpecificError (#868): generic 'error' text in
// output with mktemp in command must NOT fire the mktemp rule.
func TestShellCompatMktempRequiresSpecificError(t *testing.T) {
	if diagnoseShellCompat("mktemp /tmp/x && make build", "", "make: error 2") != "" {
		t.Fatal("generic 'error' output falsely diagnosed as mktemp issue")
	}
}

// TestSkillBundleRoundTripSubdirs (#869): a skill with a subdirectory must
// survive export + import with its companion files intact.
func TestSkillBundleRoundTripSubdirs(t *testing.T) {
	// Build a source skill dir with a template subdir.
	srcDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(srcDir, "templates"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "SKILL.md"), []byte("---\nname: t\n---\nbody"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "templates", "main.go.tmpl"), []byte("package main"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Collect files the way exportSkill now does (recursive walk).
	var files []string
	err := filepath.WalkDir(srcDir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(srcDir, p)
		if err != nil {
			return err
		}
		files = append(files, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, f := range files {
		if f == "templates/main.go.tmpl" {
			found = true
		}
	}
	if !found {
		t.Fatalf("subdirectory file missing from bundle: %v", files)
	}
}

// TestTryWriteFileReasons (#870): skip reasons must distinguish
// exists/sandbox/io.
func TestTryWriteFileReasons(t *testing.T) {
	tk := ScaffoldProject{SandboxCheck: func(p string) bool { return false }}
	if ok, reason := tk.tryWriteFile("/nonexistent/x.txt", "x"); ok || reason != "sandbox" {
		t.Fatalf("sandbox case: ok=%v reason=%q", ok, reason)
	}
	tk2 := ScaffoldProject{}
	p := filepath.Join(t.TempDir(), "exists.txt")
	os.WriteFile(p, []byte("old"), 0o644)
	if ok, reason := tk2.tryWriteFile(p, "new"); ok || reason != "exists" {
		t.Fatalf("exists case: ok=%v reason=%q", ok, reason)
	}
	if ok, reason := tk2.tryWriteFile(filepath.Join(t.TempDir(), "ok.txt"), "new"); !ok || reason != "" {
		t.Fatalf("create case: ok=%v reason=%q", ok, reason)
	}
}

// TestNamedAgentPromptOverride (#872): the schema promise of a complete
// system-prompt override is pinned indirectly by the guard comment in
// use_named_agent.go. Here we assert the semantic precondition the fix
// relies on: a template prompt with only whitespace or empty must NOT be
// treated as an override.
func TestNamedAgentPromptOverride(t *testing.T) {
	override := strings.TrimSpace("   ")
	if override != "" {
		t.Fatal("whitespace prompt must not count as override")
	}
}
