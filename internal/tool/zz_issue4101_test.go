package tool

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/topcheer/ggcode/internal/lsp"
)

func TestIssue4101NearbyPositionsUseUTF16Units(t *testing.T) {
	dir := t.TempDir()

	src := "package p\n\nfunc f() {\n\ts := \"\U0001F600\U0001F600\" // FuncName\n\t_ = s\n}\n"
	path := filepath.Join(dir, "a.go")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	got := nearbyIdentifierPositions(path, lsp.Position{Line: 4, Character: 30})
	if len(got) == 0 {
		t.Fatal("expected candidates")
	}
	foundFuncName := false
	for _, p := range got {
		if p.Character == 17 {
			foundFuncName = true
		}
	}
	if !foundFuncName {
		t.Fatalf("FuncName candidate must be emitted at UTF-16 col 17, got %v", got)
	}
	for _, p := range got {
		if p.Character == 15 {
			t.Fatalf("rune-indexed offset 15 (the bug) must not appear, got %v", got)
		}
	}

	src2 := "package q\n\n// 中文注释 target\n"
	path2 := filepath.Join(dir, "b.go")
	if err := os.WriteFile(path2, []byte(src2), 0o644); err != nil {
		t.Fatal(err)
	}
	got2 := nearbyIdentifierPositions(path2, lsp.Position{Line: 3, Character: 10})
	if len(got2) == 0 {
		t.Fatal("BMP line: expected candidates")
	}
	foundTarget := false
	for _, p := range got2 {
		if p.Character == 9 {
			foundTarget = true
		}
	}
	if !foundTarget {
		t.Fatalf("BMP target candidate at col 9 expected, got %v", got2)
	}
}
