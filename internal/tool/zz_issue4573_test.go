package tool

// #4573: persistIndex re-Stat'ed every file at persist time and wrote that
// mtime next to the TF read at BUILD time. A file rewritten between ReadFile
// and persist recorded {new mtime, old TF} on disk; since both the next
// build's cache check (doBuild) and the periodic external scan
// (scanForExternalChanges) compare mtimes, the stale TF was reused forever.
// The fix: bm25Doc carries the build-time mtime and persistIndex writes it -
// no re-Stat. Runtime reproduction needs a mid-build rewrite race, so the
// invariants are pinned structurally.

import (
	"os"
	"strings"
	"testing"
)

func TestIssue4573_PersistUsesBuildTimeMtime(t *testing.T) {
	cim, err := os.ReadFile("code_index_manager.go")
	if err != nil {
		t.Skip("source not readable in this environment")
	}
	mgr := string(cim)

	// persistIndex must not re-Stat: the TOCTOU window is the whole build.
	start := strings.Index(mgr, "func (m *CodeIndexManager) persistIndex(")
	if start < 0 {
		t.Fatal("persistIndex not found")
	}
	end := strings.Index(mgr[start:], "\nfunc ")
	body := mgr[start : start+end]
	// persistIndex must not re-Stat the DOCS for their mtime (the atomic
	// temp-file write below may legitimately Stat the cache file itself).
	// The TOCTOU regression form is re-stat'ing doc.path.
	if strings.Contains(body, "os.Stat(filepath.Join(m.workingDir, doc.path)") {
		t.Fatal("#4573: persistIndex still re-Stats each doc at persist time - a mid-build rewrite would record {new mtime, old TF} and stale the cache forever")
	}
	if !strings.Contains(body, "doc.mtime") {
		t.Fatal("#4573: persistIndex must write the build-time mtime carried on the doc")
	}

	// Both doBuild construction sites must capture the walk-time mtime.
	n := strings.Count(mgr, "mtime:  mtime,")
	if n < 2 {
		t.Fatalf("#4573: doBuild's cache-reuse and fresh-tokenize sites must both carry the build-time mtime, found %d", n)
	}

	// bm25Doc must declare the field.
	bm, err := os.ReadFile("bm25.go")
	if err != nil {
		t.Skip("bm25.go not readable")
	}
	if !strings.Contains(string(bm), "mtime int64") {
		t.Fatal("#4573: bm25Doc must carry the build-time mtime for persistIndex")
	}
}
