package agent

import (
	"go/ast"
	"go/parser"
	"sync"
	"testing"
)

const memoTestSrc = "package p\n\nfunc F() int { return 1 }\n"

// TestParseGoSourceMemoHit verifies that a repeated call with an identical
// (filePath, mode, src) key returns the cached result - same *ast.File and
// *token.FileSet pointers, no re-parse.
func TestParseGoSourceMemoHit(t *testing.T) {
	resetParseMemo()

	f1, fs1, err1 := parseGoSource("a.go", memoTestSrc, 0)
	if err1 != nil {
		t.Fatalf("first parse: %v", err1)
	}
	f2, fs2, err2 := parseGoSource("a.go", memoTestSrc, 0)
	if err2 != nil {
		t.Fatalf("second parse: %v", err2)
	}
	if f1 != f2 {
		t.Error("expected identical *ast.File from memo hit")
	}
	if fs1 != fs2 {
		t.Error("expected identical *token.FileSet from memo hit")
	}
}

// TestParseGoSourceMemoKeyIsolation verifies that differing filePath, mode,
// or src each get their own cache entry (no cross-key AST sharing).
func TestParseGoSourceMemoKeyIsolation(t *testing.T) {
	resetParseMemo()

	fa, _, _ := parseGoSource("a.go", memoTestSrc, 0)
	fb, _, _ := parseGoSource("b.go", memoTestSrc, 0) // different path
	fc, _, _ := parseGoSource("a.go", memoTestSrc, parser.ParseComments)
	fd, _, _ := parseGoSource("a.go", memoTestSrc+"\n// x\n", 0) // different src

	for name, f := range map[string]*ast.File{
		"b.go": fb, "comments-mode": fc, "different-src": fd,
	} {
		if f == fa {
			t.Errorf("key variation %q unexpectedly shared the cached AST", name)
		}
	}
}

// TestParseGoSourceMemoReset verifies resetParseMemo drops all entries: the
// next identical-key parse must produce a fresh AST.
func TestParseGoSourceMemoReset(t *testing.T) {
	resetParseMemo()

	f1, _, _ := parseGoSource("a.go", memoTestSrc, 0)
	resetParseMemo()
	f2, _, _ := parseGoSource("a.go", memoTestSrc, 0)
	if f1 == f2 {
		t.Error("expected a fresh AST after resetParseMemo")
	}
}

// TestParseGoSourceMemoErrorSemantics verifies that parse failures are cached
// exactly like successes: repeated identical bad input yields the same
// (possibly partial) AST and error, mirroring a direct parser.ParseFile call.
func TestParseGoSourceMemoErrorSemantics(t *testing.T) {
	resetParseMemo()

	bad := "package p\n\nfunc (\n"
	f1, _, err1 := parseGoSource("bad.go", bad, 0)
	f2, _, err2 := parseGoSource("bad.go", bad, 0)
	if err1 == nil {
		t.Fatal("expected parse error for malformed source")
	}
	if f1 != f2 {
		t.Error("expected identical (partial) AST across memo hits on error")
	}
	// Error values may not be pointer-equal after wrapping; require the same
	// text so callers' error-handling is stable.
	if err1.Error() != err2.Error() {
		t.Errorf("error text differs across memo hits: %q vs %q", err1, err2)
	}
}

// TestParseGoSourceMemoBounded verifies the memo evicts oldest entries beyond
// parseMemoMaxEntries instead of growing unboundedly.
func TestParseGoSourceMemoBounded(t *testing.T) {
	resetParseMemo()

	// Fill with parseMemoMaxEntries + 1 distinct keys, then re-request the
	// first: it must have been evicted (fresh AST), proving bounded size.
	var first *ast.File
	for i := 0; i <= parseMemoMaxEntries; i++ {
		f, _, _ := parseGoSource("f.go", memoTestSrc, parser.Mode(i))
		if i == 0 {
			first = f
		}
	}
	again, _, _ := parseGoSource("f.go", memoTestSrc, 0)
	if again == first {
		t.Errorf("expected oldest entry to be evicted after %d insertions", parseMemoMaxEntries+1)
	}

	parseMemo.Lock()
	n := len(parseMemo.entries)
	parseMemo.Unlock()
	if n > parseMemoMaxEntries {
		t.Errorf("memo grew to %d entries, max is %d", n, parseMemoMaxEntries)
	}
}

// TestParseGoSourceMemoConcurrent verifies the memo is safe under the access
// pattern of runChecksParallel: many goroutines parsing overlapping sources
// (cache hits racing with misses) without corruption or deadlock. Run with
// -race in CI to make this meaningful.
func TestParseGoSourceMemoConcurrent(t *testing.T) {
	resetParseMemo()

	sources := []string{memoTestSrc, "package q\n\nvar Q = 1\n", "package r\n\nvar X = 1\n"}
	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			src := sources[g%len(sources)]
			for i := 0; i < 25; i++ {
				f, _, err := parseGoSource("c.go", src, 0)
				if err != nil || f == nil {
					t.Errorf("goroutine %d iter %d: err=%v file=%v", g, i, err, f)
					return
				}
				if len(f.Decls) == 0 {
					t.Errorf("goroutine %d: empty Decls for %q", g, src)
				}
			}
		}(g)
	}
	wg.Wait()
}
