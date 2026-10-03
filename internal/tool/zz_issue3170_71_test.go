package tool

// Regression probes for #3170 + #3171 (grep, one PR).
//
// #3170: the schema documents "Defaults: 250 in content mode", but every
// cap site only applied an EXPLICIT head_limit - omitted (0) meant no cap
// at all, streaming unbounded content on broad patterns.
//
// #3171: files_with_matches ordered by pathDepth on the rg backend but by
// plain lexicographic sort on the fallback - the same offset/head_limit
// window showed different files on machines with vs without ripgrep.
// Both backends now share sortPathsDepthThenLex ((depth, lex) total order).

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// #3170 fallback path: formatContentMatches with head_limit omitted must
// cap at the documented 250 and show the pagination hint.
func TestIssue3170_FallbackContentDefaultCap(t *testing.T) {
	var matches []fileMatch
	for i := 1; i <= 300; i++ {
		matches = append(matches, fileMatch{
			path:    fmt.Sprintf("pkg/f%03d.go", i),
			lineNum: 1,
			line:    "TODO probe",
		})
	}
	res := formatContentMatches(matches, grepArgs{OutputMode: "content"})
	lines := 0
	for _, ln := range strings.Split(res.Content, "\n") {
		if strings.Contains(ln, ":1: TODO") {
			lines++
		}
	}
	if lines != 250 { // == maxContentHeadLimit (literal keeps this probe compilable against the pre-fix tree for red/green discrimination)
		t.Errorf("omitted head_limit must default to %d in content mode, got %d entries", 250, lines)
	}
	if !strings.Contains(res.Content, "use offset to see more") {
		t.Errorf("capped output must carry the pagination hint, got: ...%s", tail3170(res.Content, 80))
	}

	// Explicit large head_limit is honored verbatim (no undocumented cap).
	res = formatContentMatches(matches, grepArgs{OutputMode: "content", HeadLimit: 280})
	lines = strings.Count(res.Content, ":1: TODO")
	if lines != 280 {
		t.Errorf("explicit head_limit=280 must be honored, got %d", lines)
	}
}

// #3170 rg path (end-to-end, skip when rg is unavailable): content mode
// with omitted head_limit caps at 250.
func TestIssue3170_RGContentDefaultCapE2E(t *testing.T) {
	// Anchor the REAL rg binary: package-mate tests cache a bogus rgPath
	// ("/existing/rg") without restoring, which would make this probe exec
	// a phantom binary and observe 0 entries regardless of the fix.
	rgBin, err := exec.LookPath("rg")
	if err != nil {
		t.Skip("ripgrep not available; rg-side cap exercised via --max-count construction shared with the trim logic")
	}
	prev := rgPath.Load()
	rgBinCopy := rgBin
	rgPath.Store(&rgBinCopy)
	t.Cleanup(func() {
		if p, ok := prev.(*string); ok {
			rgPath.Store(p) // exact prior (possibly bogus) state restored
		}
	})
	dir := t.TempDir()
	for i := 1; i <= 260; i++ {
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("f%03d.go", i)), []byte("package p\nTODO probe\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	tool := Grep{}
	res, err := tool.Execute(context.Background(), json.RawMessage(
		fmt.Sprintf(`{"pattern":"TODO probe","path":%q,"output_mode":"content"}`, dir)))
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Count(res.Content, "TODO probe")
	// 1 entry per file (single match line each): default cap 250.
	if got != 250 { // == maxContentHeadLimit (literal: see fallback probe note)
		t.Errorf("rg content mode with omitted head_limit: %d entries, want default cap %d", got, 250)
	}
}

// #3171: shared order is (pathDepth, lexicographic) - a TOTAL order, so
// both backends produce identical sequences.
func TestIssue3171_DepthThenLexTotalOrder(t *testing.T) {
	paths := []string{"a/b.go", "z.go", "b.go", "a/b/c.go", "m/x.go"}
	sortPathsDepthThenLex(paths)
	want := []string{"b.go", "z.go", "a/b.go", "m/x.go", "a/b/c.go"}
	for i := range want {
		if paths[i] != want[i] {
			t.Fatalf("order[%d] = %s, want %s (full: %v)", i, paths[i], want[i], paths)
		}
	}
}

// #3171 fallback path: files_with_matches output follows the same
// (depth, lex) order as the rg backend (was: pure lexicographic, which
// put a/b.go before b.go - the cross-backend pagination drift of #3171).
func TestIssue3171_FallbackFilesOrderMatchesRG(t *testing.T) {
	files := map[string]bool{
		"a/b.go": true, "b.go": true, "z.go": true, "a/b/c.go": true,
	}
	res := formatFilesWithMatches(files, grepArgs{OutputMode: "files_with_matches"})
	var got []string
	for _, ln := range strings.Split(res.Content, "\n") {
		ln = strings.TrimSpace(ln)
		if ln == "" || strings.HasPrefix(ln, "(") || strings.Contains(ln, "file(s) matched") {
			continue // blank / summary lines
		}
		got = append(got, ln)
	}
	want := []string{"b.go", "z.go", "a/b.go", "a/b/c.go"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if !strings.HasSuffix(got[i], want[i]) {
			t.Fatalf("entry[%d] = %q does not end with %q (full: %v)", i, got[i], want[i], got)
		}
	}
}

func tail3170(s string, n int) string {
	if len(s) > n {
		return s[len(s)-n:]
	}
	return s
}
