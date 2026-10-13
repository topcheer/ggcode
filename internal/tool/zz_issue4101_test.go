package tool

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/topcheer/ggcode/internal/lsp"
)

// #4101: nearbyIdentifierPositions indexed the line by RUNES while LSP
// Position.Character is UTF-16 code units (send side + offsetForPosition
// both honor that). On astral-character lines every candidate sat left of
// the real symbol by the astral count before it.
func TestIssue4101NearbyPositionsUseUTF16Units(t *testing.T) {
	dir := t.TempDir()
	// The issue's repro line: tab + s := "😀😀" (2 astral = 2 runes, 4 UTF-16
	// units) + comment, then identifier FuncName.
	// rune index of FuncName = 14; UTF-16 prefix before it = 18.
	src := "package p\n\nfunc f() {\n\ts := \"\U0001F600\U0001F600\" // FuncName\n\t_ = s\n}\n"
	path := filepath.Join(dir, "a.go")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	// Line 4 (1-based). FuncName starts at rune 14; UTF-16 prefix before it
	// is 16 (7 single-unit runes + 2 astral x 2 units + 5 single-unit runes)
	// -> emitted Character must be 17. The old rune-indexed code emitted 15.
	got := nearbyIdentifierPositions(path, lsp.Position{Line: 4, Character: 30})
	if len(got) == 0 {
		t.Fatal("expected candidates")
	}
	foundFuncName := false
	for _, p := range got {
		if p.Character == 17 { // UTF-16 col of FuncName + 1 (1-based tool side)
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

	// BMP line (CJK): rune count == UTF-16 units, behavior unchanged.
	// Line 3: `// 中文注释 target` - target starts at rune 8 = UTF-16 8 -> 9.
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
