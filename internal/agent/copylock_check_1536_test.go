package agent

import (
	"go/parser"
	"go/token"
	"testing"
)

// #1536 case A pin: the body-level scan must stay deleted - a range over a
// pointer slice with a loop variable NAMED like a lock struct type is legal
// (copies a pointer) and must not fire. The 5bd1ab3d scan hit exactly this
// shape via name-based type matching.
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
	// Cache has no real sync field here (alias to plain struct) so even the
	// struct collector should not flag; the point of this pin is that NO
	// body-derived issue ("range var ... copies lock value" /
	// "assignment copies lock value") is ever produced.
	for _, iss := range findCopylockIssues(fset, file) {
		if iss.funcName == "" {
			t.Fatalf("signature-path issues must carry funcName (#213); got %+v", iss)
		}
	}
}

// #1536 case C pin: an ack phrase in one paragraph must not blind the
// detector to unrelated claims elsewhere in the same message.
func Test1536AckProximityNotWholeText(t *testing.T) {
	s := newContradictionState()
	// iter1: claim handler.go (will be contradicted later)
	s.recordContradictionClaims("the root cause is in auth.go", 1)
	// iter2: acknowledged revision near auth.go + a NEW unrelated claim far away
	s.recordContradictionClaims("On second thought, I was wrong about auth.go being the cause. Separately, after full re-checking I now believe the real bug is in handler.go.", 2)
	if n := len(s.contradictions); n != 0 {
		t.Fatalf("acknowledged revision must not pair (got %d contradictions)", n)
	}
	// iter3: restating the corrected claim must NOT pair against the stale
	// auth.go claim (the correction was recorded in history - case D).
	s.recordContradictionClaims("I still believe the bug is in handler.go", 3)
	if n := len(s.contradictions); n != 0 {
		t.Fatalf("restating the corrected claim must not pair against the superseded one (got %d)", n)
	}
}
