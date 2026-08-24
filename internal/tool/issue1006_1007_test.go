package tool

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestIssue1006ParseGitBlameTrailingEmptyLine pins the fix: git blame
// --line-porcelain output ends with \n, so Split always yields a trailing
// empty line; the old unguarded 'a'-'f' clause panicked on it.
func TestIssue1006ParseGitBlameTrailingEmptyLine(t *testing.T) {
	// Real-shaped porcelain output ending with \n (trailing empty element).
	// The header line carries finalLine=1; the panic regression is that
	// this call completes at all - the old code crashed on the trailing
	// empty line before returning.
	out := "a1b2c3d 1 1\nauthor Alice\nauthor-mail <a@x.com>\nauthor-time 1700000000\nauthor-tz +0800\nsummary init\n\tline one\n\n"
	if m := parseGitBlame([]byte(out)); len(m) == 0 || m[1].author == "" {
		t.Errorf("expected blame for line 1 with author, got %v", m)
	}
	// Empty input edges: must return without panic and without entries.
	if m := parseGitBlame([]byte("")); len(m) != 0 {
		t.Errorf("empty input must yield no entries, got %v", m)
	}
	if m := parseGitBlame([]byte("\n")); len(m) != 0 {
		t.Errorf("newline-only input must yield no entries, got %v", m)
	}
}

// TestIssue1007PythonInitPyRealNewline pins the fix: __init__.py must end
// with a real newline, not a literal backslash-n, or Python import fails
// with SyntaxError 100% of the time.
func TestIssue1007PythonInitPyRealNewline(t *testing.T) {
	dir := t.TempDir()
	tool := ScaffoldProject{WorkingDir: dir}
	input := scaffoldMustMarshal(t, map[string]interface{}{
		"language":     "python",
		"project_name": "myapp",
		"output_dir":   dir,
	})
	if _, err := tool.Execute(context.Background(), input); err != nil {
		t.Fatalf("scaffold failed: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "myapp", "__init__.py"))
	if err != nil {
		t.Fatalf("read __init__.py: %v", err)
	}
	got := string(data)
	if strings.Contains(got, `\n`) {
		t.Errorf("__init__.py contains literal backslash-n: %q", got)
	}
	if !strings.HasSuffix(got, "package.\"\"\"\n") {
		t.Errorf("__init__.py must end with docstring + real newline, got %q", got)
	}
}
