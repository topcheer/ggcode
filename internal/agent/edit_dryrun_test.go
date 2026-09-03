package agent

import (
	"strings"
	"testing"
)

func TestDryRunValidate_GoSyntaxError(t *testing.T) {
	old := "package main\n\nfunc main() {}\n"
	// Missing closing brace - guaranteed syntax error
	newContent := "package main\n\nfunc main() {\n"

	msg := dryRunValidate("test.go", old, newContent)
	if msg == "" {
		t.Fatal("expected fatal error for Go syntax error, got empty")
	}
	if !strings.Contains(msg, "expected") {
		t.Errorf("expected parser error mention, got: %s", msg)
	}
}

func TestDryRunValidate_GoValid(t *testing.T) {
	old := "package main\n\nfunc main() {}\n"
	newContent := "package main\n\nfunc main() {\n\tprintln(\"hello\")\n}\n"

	msg := dryRunValidate("test.go", old, newContent)
	if msg != "" {
		t.Errorf("expected no fatal error for valid Go, got: %s", msg)
	}
}

func TestDryRunValidate_BinaryCorruption(t *testing.T) {
	old := "hello world"
	newContent := "hello\x00world"

	msg := dryRunValidate("test.txt", old, newContent)
	if msg == "" {
		t.Fatal("expected fatal error for null bytes")
	}
	if !strings.Contains(msg, "null byte") {
		t.Errorf("expected null byte mention, got: %s", msg)
	}
}

func TestDryRunValidate_ContentLoss(t *testing.T) {
	old := "package main\n\nfunc main() {}\n"
	newContent := "   \n   \n"

	msg := dryRunValidate("test.go", old, newContent)
	if msg == "" {
		t.Fatal("expected fatal error for content loss")
	}
	if !strings.Contains(msg, "EMPTY") {
		t.Errorf("expected EMPTY mention, got: %s", msg)
	}
}

func TestDryRunValidate_NewFileEmpty(t *testing.T) {
	// New file (old content empty) becoming empty should NOT trigger content loss
	msg := dryRunValidate("new.txt", "", "")
	if msg != "" {
		t.Errorf("expected no fatal error for new empty file, got: %s", msg)
	}
}

func TestDryRunValidate_MergeConflict(t *testing.T) {
	old := "package main\n"
	newContent := "package main\n<<<<<<< HEAD\nfunc a() {}\n=======\nfunc b() {}\n>>>>>>> feature\n"

	msg := dryRunValidate("test.go", old, newContent)
	if msg == "" {
		t.Fatal("expected fatal error for merge conflict markers")
	}
}

func TestDryRunValidate_NoChange(t *testing.T) {
	// Identical content should pass (no real change)
	content := "package main\n\nfunc main() {}\n"
	msg := dryRunValidate("test.go", content, content)
	if msg != "" {
		t.Errorf("expected no fatal error for unchanged content, got: %s", msg)
	}
}

func TestDryRunValidate_NonGoFile(t *testing.T) {
	// Non-Go files should skip syntax check but still check for corruption
	old := "console.log('hello')"
	newContent := "console.log('world')"

	msg := dryRunValidate("test.js", old, newContent)
	if msg != "" {
		t.Errorf("expected no fatal error for valid JS edit, got: %s", msg)
	}
}

func TestDryRunValidateBatch(t *testing.T) {
	plans := []fileEditPlan{
		{Path: "valid.go", OldContent: "package main\n", NewContent: "package main\n\nfunc main() {}\n"},
		{Path: "broken.go", OldContent: "package main\n", NewContent: "package main\n\nfunc main() {\n"},
		{Path: "ok.txt", OldContent: "hello", NewContent: "world"},
	}

	blockers := dryRunValidateBatch(plans)
	if len(blockers) != 1 {
		t.Fatalf("expected 1 blocker, got %d: %v", len(blockers), blockers)
	}
	if _, ok := blockers["broken.go"]; !ok {
		t.Errorf("expected broken.go to be blocked, got: %v", blockers)
	}
	if _, ok := blockers["valid.go"]; ok {
		t.Errorf("valid.go should not be blocked")
	}
}

func TestDryRunValidateBatch_AllValid(t *testing.T) {
	plans := []fileEditPlan{
		{Path: "a.go", OldContent: "package main\n", NewContent: "package main\n\nfunc a() {}\n"},
		{Path: "b.go", OldContent: "package main\n", NewContent: "package main\n\nfunc b() {}\n"},
	}

	blockers := dryRunValidateBatch(plans)
	if len(blockers) != 0 {
		t.Errorf("expected 0 blockers, got %d: %v", len(blockers), blockers)
	}
}

// TestDryRunValidate_ConflictResolutionNotBlocked pins #1454-B: resolving
// PRE-EXISTING conflict markers (git merge awaiting resolution) via a
// per-hunk edit_file must pass pre-write validation - the old whole-file
// count blocked the very operation that fixes conflicts with 'remove ALL
// markers'. Only markers the edit ADDS stay fatal.
func TestDryRunValidate_ConflictResolutionNotBlocked(t *testing.T) {
	old := "package p\n\nvar a int\n<<<<<<< HEAD\na = 1\n=======\na = 2\n>>>>>>> other\n\nfunc f() { _ = a }\n"
	// Resolve ONE hunk: new content drops all markers for this block.
	newContent := "package p\n\nvar a = 2\n\nfunc f() { _ = a }\n"
	if msg := dryRunValidate("test.go", old, newContent); msg != "" {
		t.Fatalf("conflict resolution blocked by pre-write gate: %s", msg)
	}
	// INTRODUCING markers stays fatal.
	clean := "package p\n\na := 1\n"
	withMarkers := "package p\n<<<<<<< HEAD\na := 1\n>>>>>>> x\n"
	if msg := dryRunValidate("test.go", clean, withMarkers); msg == "" {
		t.Fatal("edit introducing conflict markers must stay blocked")
	}
}
