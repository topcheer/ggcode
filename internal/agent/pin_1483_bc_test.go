package agent

import "testing"

func TestQueryConvergeGlobIsNotAQuery(t *testing.T) {
	q := newQueryConvergeState()
	q.recordToolCall("glob", `{"pattern":"internal/agent/*.go"}`, 1)
	q.recordToolCall("glob", `{"pattern":"internal/tool/*.go"}`, 2)
	q.recordToolCall("glob", `{"pattern":"internal/config/*.go"}`, 3)
	if msg := q.maybeWarn(4); msg != "" {
		t.Fatalf("expected no warning: globs are path syntax, not rephrased queries, got: %s", msg)
	}
}

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
