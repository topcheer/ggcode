package tool

import (
	"os"
	"strings"
	"testing"
)

func TestIssue4573_PersistUsesBuildTimeMtime(t *testing.T) {
	cim, err := os.ReadFile("code_index_manager.go")
	if err != nil {

		t.Fatalf("#4573 pin test cannot read code_index_manager.go: %v", err)
	}
	mgr := string(cim)

	start := strings.Index(mgr, "func (m *CodeIndexManager) persistIndex(")
	if start < 0 {
		t.Fatal("persistIndex not found")
	}
	end := strings.Index(mgr[start:], "\nfunc ")
	body := mgr[start : start+end]

	if strings.Contains(body, "os.Stat(filepath.Join(m.workingDir, doc.path)") {
		t.Fatal("#4573: persistIndex still re-Stats each doc at persist time - a mid-build rewrite would record {new mtime, old TF} and stale the cache forever")
	}
	if !strings.Contains(body, "doc.mtime") {
		t.Fatal("#4573: persistIndex must write the build-time mtime carried on the doc")
	}

	n := strings.Count(mgr, "mtime:  mtime,")
	if n < 2 {
		t.Fatalf("#4573: doBuild's cache-reuse and fresh-tokenize sites must both carry the build-time mtime, found %d", n)
	}

	bm, err := os.ReadFile("bm25.go")
	if err != nil {
		t.Fatalf("#4573 pin test cannot read bm25.go: %v", err)
	}
	if !strings.Contains(string(bm), "mtime") || !strings.Contains(string(bm), "bm25Doc struct") {
		t.Fatal("#4573: bm25Doc must carry the build-time mtime for persistIndex")
	}
}
