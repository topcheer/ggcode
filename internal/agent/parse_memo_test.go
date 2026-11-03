package agent

import (
	"go/ast"
	"go/parser"
	"sync"
	"testing"
)

const memoTestSrc = "package p\n\nfunc F() int { return 1 }\n"

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

func TestParseGoSourceMemoKeyIsolation(t *testing.T) {
	resetParseMemo()

	fa, _, _ := parseGoSource("a.go", memoTestSrc, 0)
	fb, _, _ := parseGoSource("b.go", memoTestSrc, 0)
	fc, _, _ := parseGoSource("a.go", memoTestSrc, parser.ParseComments)
	fd, _, _ := parseGoSource("a.go", memoTestSrc+"\n// x\n", 0)

	for name, f := range map[string]*ast.File{
		"b.go": fb, "comments-mode": fc, "different-src": fd,
	} {
		if f == fa {
			t.Errorf("key variation %q unexpectedly shared the cached AST", name)
		}
	}
}

func TestParseGoSourceMemoReset(t *testing.T) {
	resetParseMemo()

	f1, _, _ := parseGoSource("a.go", memoTestSrc, 0)
	resetParseMemo()
	f2, _, _ := parseGoSource("a.go", memoTestSrc, 0)
	if f1 == f2 {
		t.Error("expected a fresh AST after resetParseMemo")
	}
}

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

	if err1.Error() != err2.Error() {
		t.Errorf("error text differs across memo hits: %q vs %q", err1, err2)
	}
}

func TestParseGoSourceMemoBounded(t *testing.T) {
	resetParseMemo()

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
