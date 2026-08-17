package tool

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Probe A: non-unique old_text resolved via fuzzy fallback edits the FIRST
// occurrence even though error message claims uniqueness is required.
func TestProbeFuzzyDuplicate(t *testing.T) {
	content := "line1: alpha\nspecial block\nline3: alpha\nspecial block\n"
	oldFuzzy := "special block\nline3: alpha"
	if !strings.Contains(content, oldFuzzy) {
		// exact contains -> not fuzzy. Use trailing-space variant.
		oldFuzzy = "special block \nline3: alpha"
	}
	mr := resolveOldText(content, oldFuzzy)
	if mr.canonical == "" {
		t.Fatalf("expected match, got none")
	}
	t.Logf("transform=%q canonical=%q anchored=%v", mr.transform, mr.canonical, mr.anchored)
	if mr.transform != "" && strings.Count(content, mr.canonical) > 1 {
		// can't happen here; see probe B
	}
}

// Probe B: anchored single line where body text appears many times.
func TestProbeAnchoredDuplicate(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "dup.go")
	content := "\treturn nil\n\treturn nil\n\treturn nil\n"
	os.WriteFile(p, []byte(content), 0644)
	oldText := "   2\treturn nil" // read_file style anchor line 2
	mr := resolveOldText(content, oldText)
	t.Logf("canonical=%q anchored=%v start=%d transform=%q", mr.canonical, mr.anchored, mr.start, mr.transform)
	if mr.canonical == "" {
		t.Fatal("no match")
	}
	if mr.start != len("\treturn nil\n") {
		t.Logf("BUG? anchored start=%d expected=%d (wrong occurrence selected?)", mr.start, len("\treturn nil\n"))
	}
}

// Probe C: fuzzy/shift fallback with duplicated text — which occurrence wins?
func TestProbeFallbackFirstOccurrence(t *testing.T) {
	content := "func a() {\n\tX()\n}\nfunc b() {\n\tX()\n}\n"
	mr := resolveOldText(content, "\tX()\n}") // exact? "\tX()\n}" appears once? check
	t.Logf("C: canonical=%q transform=%q", mr.canonical, mr.transform)

	// Now duplicate: trailing-whitespace-tolerant path picks first.
	content2 := "\tif err != nil {\n\t\treturn err \n\t}\n\tif err != nil {\n\t\treturn err \n\t}\n"
	old2 := "if err != nil {\n\t\treturn err\n\t}" // no trailing space in old
	mr2 := resolveOldText(content2, old2)
	t.Logf("C2: canonical=%q transform=%q", mr2.canonical, mr2.transform)
}

// Probe D: multi_edit_file relative path — resolved against process cwd or WorkingDir?
func TestProbeMultiEditRelativePath(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "sub")
	os.MkdirAll(sub, 0755)
	p := filepath.Join(sub, "rel.go")
	os.WriteFile(p, []byte("alpha\nbeta\n"), 0644)

	tool := MultiEditFile{WorkingDir: sub}
	input := `{"file_path":"rel.go","edits":[{"old_text":"alpha","new_text":"gamma"}]}`
	res, err := tool.Execute(t.Context(), []byte(input))
	t.Logf("D: err=%v isError=%v content=%q", err, res.IsError, res.Content)
	if res.IsError || err != nil {
		t.Logf("D RESULT: relative file_path NOT resolved against WorkingDir -> %v / %s", err, res.Content)
	}
}

// Probe E: edit_file relative path for comparison (should resolve).
func TestProbeEditFileRelativePath(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "sub")
	os.MkdirAll(sub, 0755)
	p := filepath.Join(sub, "rel.go")
	os.WriteFile(p, []byte("alpha\nbeta\n"), 0644)

	tool := EditFile{WorkingDir: sub}
	input := `{"file_path":"rel.go","old_text":"alpha","new_text":"gamma"}`
	res, err := tool.Execute(t.Context(), []byte(input))
	t.Logf("E: err=%v isError=%v content=%q", err, res.IsError, res.Content)
}
