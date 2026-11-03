package agent

import (
	"go/parser"
	"go/token"
	"testing"
)

func Test1536BodyScanStaysDeleted(t *testing.T) {
	src := `package p
type Cache struct{ mu syncMutexAlias }
type syncMutexAlias = fakeMutex
type fakeMutex struct{ x int }

func f(caches []*Cache) {
	for _, Cache := range caches {
		_ = Cache
	}
	snapshot := *caches[0]
	_ = snapshot
}
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "t.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}

	for _, iss := range findCopylockIssues(fset, file) {
		if iss.funcName == "" {
			t.Fatalf("signature-path issues must carry funcName (#213); got %+v", iss)
		}
	}
}

func Test1536AckProximityNotWholeText(t *testing.T) {
	s := newContradictionState()

	s.recordContradictionClaims("the root cause is in auth.go", 1)

	s.recordContradictionClaims("On second thought, I was wrong about auth.go being the cause. Separately, after full re-checking I now believe the real bug is in handler.go.", 2)
	if n := len(s.contradictions); n != 0 {
		t.Fatalf("acknowledged revision must not pair (got %d contradictions)", n)
	}

	s.recordContradictionClaims("I still believe the bug is in handler.go", 3)
	if n := len(s.contradictions); n != 0 {
		t.Fatalf("restating the corrected claim must not pair against the superseded one (got %d)", n)
	}
}
