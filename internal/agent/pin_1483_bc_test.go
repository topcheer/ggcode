package agent

import "testing"

// TestQueryConvergeGlobIsNotAQuery pins #1483 case B: glob's pattern argument
// is path syntax (internal/agent/*.go), not a natural-language query. Three
// DIFFERENT packages' globs must not look like the same question rephrased -
// if glob tokenization leaks back in, this test fails with a spurious warning.
func TestQueryConvergeGlobIsNotAQuery(t *testing.T) {
	q := newQueryConvergeState()
	q.recordToolCall("glob", `{"pattern":"internal/agent/*.go"}`, 1)
	q.recordToolCall("glob", `{"pattern":"internal/tool/*.go"}`, 2)
	q.recordToolCall("glob", `{"pattern":"internal/config/*.go"}`, 3)
	if msg := q.maybeWarn(4); msg != "" {
		t.Fatalf("expected no warning: globs are path syntax, not rephrased queries, got: %s", msg)
	}
}

// TestCountConcurrencyPrimitivesAliasedImports pins #1483 case C: an aliased
// import (`import concur "sync"`) must resolve to its real package, so
// concur.Mutex counts as a concurrency primitive. The old code compared the
// LOCAL name against "sync"/"atomic" literally and counted zero.
func TestCountConcurrencyPrimitivesAliasedImports(t *testing.T) {
	src := `package x

import concur "sync"
import atm "sync/atomic"

var m concur.Mutex
var n int32

func f() {
	m.Lock()
	atm.AddInt32(&n, 1)
	m.Unlock()
}
`
	if got := countConcurrencyPrimitives(src); got == 0 {
		t.Fatal("aliased sync/sync-atomic imports must count as concurrency primitives, got 0")
	}

	// Control: same code with default import names (baseline sanity).
	plain := `package x

import (
	"sync"
	"sync/atomic"
)

var m sync.Mutex
var n int32

func f() {
	m.Lock()
	atomic.AddInt32(&n, 1)
	m.Unlock()
}
`
	if got := countConcurrencyPrimitives(plain); got == 0 {
		t.Fatal("plain sync/sync-atomic imports must count, got 0")
	}

	// Negative: an unrelated package aliased as "sync" must NOT count.
	wrong := `package x

import sync "fmt"

func f() {
	sync.Println("hi")
}
`
	if got := countConcurrencyPrimitives(wrong); got != 0 {
		t.Fatalf("ident 'sync' bound to fmt must not count as concurrency primitive, got %d", got)
	}
}
