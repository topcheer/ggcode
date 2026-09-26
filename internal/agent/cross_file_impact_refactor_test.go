package agent

// Helper-level tests for the checkCrossFileImpact decomposition
// (r116 refactor). The orchestrator's end-to-end behavior is covered by
// TestIssue550_* and cross_file_impact_test.go; these tests pin the
// extracted helpers' contracts so future refactors stay semantic-preserving.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestEditedGoFilesFiltersNonGoAndKeepsOrder(t *testing.T) {
	got := editedGoFiles([]string{
		"a.go", "notes.txt", "sub/b.go", "c.GO", "", "d_test.go",
	})
	want := []string{"a.go", "sub/b.go", "d_test.go"}
	if len(got) != len(want) {
		t.Fatalf("editedGoFiles = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("editedGoFiles[%d] = %q, want %q (full: %v)", i, got[i], want[i], got)
		}
	}
	if editedGoFiles(nil) != nil {
		t.Fatal("editedGoFiles(nil) must be nil")
	}
}

func TestAbsEditedSetJoinsAndCleans(t *testing.T) {
	wd := string(filepath.Separator) + filepath.Join("tmp", "work")
	set := absEditedSet(wd, []string{"a.go", "./sub" + string(filepath.Separator) + "b.go", wd + string(filepath.Separator) + "c.go"})
	if !set[filepath.Join(wd, "a.go")] {
		t.Fatalf("relative edited file must join workingDir: %v", set)
	}
	if !set[filepath.Join(wd, "sub", "b.go")] {
		t.Fatalf("./-prefixed path must be cleaned: %v", set)
	}
	if !set[filepath.Join(wd, "c.go")] {
		t.Fatalf("absolute path must be kept and cleaned: %v", set)
	}
	if len(set) != 3 {
		t.Fatalf("unexpected set size: %v", set)
	}
}

func TestImpactTotalsSumsAcrossImpacts(t *testing.T) {
	impacts := []fileImpact{
		{removedSyms: []impactRemovedSymbol{{name: "a"}, {name: "b"}}, affectedFiles: []string{"x.go"}},
		{removedSyms: []impactRemovedSymbol{{name: "c"}}, affectedFiles: []string{"y.go", "z.go"}},
	}
	affected, removed := impactTotals(impacts)
	if affected != 3 || removed != 3 {
		t.Fatalf("impactTotals = (%d, %d), want (3, 3)", affected, removed)
	}
	if a, r := impactTotals(nil); a != 0 || r != 0 {
		t.Fatalf("impactTotals(nil) = (%d, %d), want (0, 0)", a, r)
	}
}

func TestRenderImpactWarningCapsAtMaxImpactFiles(t *testing.T) {
	var affected []string
	for i := 0; i < 10; i++ {
		affected = append(affected, strings.Repeat("f", i+1)+".go")
	}
	impacts := []fileImpact{{
		editedFile:    "edit.go",
		removedSyms:   []impactRemovedSymbol{{name: "sharedHelper", category: "func"}},
		affectedFiles: affected,
	}}
	msg := renderImpactWarning(impacts, 1, 10)
	if !strings.Contains(msg, "removed or renamed 1 exported symbol(s)") {
		t.Fatalf("missing total-removed sentence: %s", msg)
	}
	if !strings.Contains(msg, "referenced by 10 file(s)") {
		t.Fatalf("missing total-affected count: %s", msg)
	}
	if got := strings.Count(msg, "-> "); got != maxImpactFiles {
		t.Fatalf("listed %d affected files, want cap %d", got, maxImpactFiles)
	}
	if !strings.Contains(msg, "... and 2 more") {
		t.Fatalf("missing overflow line: %s", msg)
	}
	if !strings.Contains(msg, "sharedHelper (func)") {
		t.Fatalf("missing symbol+category listing: %s", msg)
	}
	if !strings.Contains(msg, "Verify with `go build`") {
		t.Fatalf("missing verify footer: %s", msg)
	}
}

func TestScanSiblingsForImpactSkipsCoEditedAndTextFallback(t *testing.T) {
	dir := t.TempDir()
	coEdited := filepath.Join(dir, "a_coedited.go")
	parsable := filepath.Join(dir, "b_parsable.go")
	broken := filepath.Join(dir, "c_broken.go")
	for name, content := range map[string]string{
		coEdited: "package p\n\nfunc useCoEdited() { sharedHelper() }\n",
		parsable: "package p\n\nfunc use() { sharedHelper() }\n",
		broken:   "package p\nfunc {{{ sharedHelper",
	} {
		if err := os.WriteFile(name, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	editedAbs := map[string]bool{filepath.Clean(coEdited): true}
	names := map[string]bool{"sharedHelper": true}

	scan := scanSiblingsForImpact(token.NewFileSet(), []string{coEdited, parsable, broken},
		editedAbs, dir, names, nil, time.Now().Add(time.Minute))

	if scan.affectedSet[coEdited] {
		t.Fatalf("co-edited sibling must be skipped (#550 C1): %v", scan.affectedSet)
	}
	if !scan.affectedSet["b_parsable.go"] {
		t.Fatalf("parsable sibling referencing the removed symbol must be affected: %v", scan.affectedSet)
	}
	if !scan.affectedSet["c_broken.go"] {
		t.Fatalf("unparsable sibling must fall back to the text scan (#1773 case 5): %v", scan.affectedSet)
	}
	if len(scan.pkgFiles) != 1 {
		t.Fatalf("only the parsable sibling enters pkgFiles, got %d", len(scan.pkgFiles))
	}
	if len(scan.pendings) != 0 {
		t.Fatalf("bare-name reference is a definite hit, not a near miss: %v", scan.pendings)
	}
}

func TestScanSiblingsForImpactHonorsDeadline(t *testing.T) {
	dir := t.TempDir()
	sib := filepath.Join(dir, "b.go")
	if err := os.WriteFile(sib, []byte("package p\n\nfunc use() { sharedHelper() }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	scan := scanSiblingsForImpact(token.NewFileSet(), []string{sib},
		nil, dir, map[string]bool{"sharedHelper": true}, nil, time.Now().Add(-time.Second))
	if len(scan.affectedSet) != 0 {
		t.Fatalf("expired deadline must stop the scan immediately: %v", scan.affectedSet)
	}
}

func TestResolveImpactPendingsNoopGuards(t *testing.T) {
	dirFset := token.NewFileSet()
	// No pendings → no-op even with owners and time left.
	scan := impactSiblingScan{affectedSet: make(map[string]bool)}
	resolveImpactPendings(dirFset, &scan, "", "", map[string]map[string]bool{"m": {"T": true}}, time.Now().Add(time.Minute))
	if len(scan.affectedSet) != 0 || len(scan.pkgFiles) != 0 {
		t.Fatalf("no pendings must be a no-op: %+v", scan)
	}
	// Expired deadline → no-op even with pendings.
	file, err := parser.ParseFile(dirFset, "sib.go", "package p\n\nfunc f(s T) { s.m() }\n", 0)
	if err != nil {
		t.Fatal(err)
	}
	scan = impactSiblingScan{
		affectedSet: make(map[string]bool),
		pkgFiles:    []*ast.File{file},
		pendings:    []impactPending{{relPath: "sib.go", file: file, near: []impactNearMiss{{recv: nil, sel: "m"}}}},
	}
	resolveImpactPendings(dirFset, &scan, "", "", map[string]map[string]bool{"m": {"T": true}}, time.Now().Add(-time.Second))
	if len(scan.affectedSet) != 0 {
		t.Fatalf("expired deadline must skip the typed pass: %v", scan.affectedSet)
	}
}

func TestAnalyzeEditedFileImpactMissingBaselineNil(t *testing.T) {
	// No git repo → gitFileContentAtHEAD fails → nil, no panic.
	dir := t.TempDir()
	edited := filepath.Join(dir, "a.go")
	if err := os.WriteFile(edited, []byte("package p\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if imp := analyzeEditedFileImpact(dir, edited, map[string]bool{}, time.Now().Add(time.Minute)); imp != nil {
		t.Fatalf("missing git baseline must yield nil, got %+v", imp)
	}
}
