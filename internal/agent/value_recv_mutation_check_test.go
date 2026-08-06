package agent

import (
	"testing"
)

func TestValueRecvMutation_Assignment(t *testing.T) {
	src := `package main

type Counter struct {
	count int
}

func (c Counter) Increment() {
	c.count = 10
}
`
	warnings := checkValueRecvMutation("test.go", "", src)
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning, got %d: %v", len(warnings), warnings)
	}
	if !contains(warnings[0], "VALUE receiver") {
		t.Errorf("warning should mention VALUE receiver: %s", warnings[0])
	}
	if !contains(warnings[0], "POINTER receiver") {
		t.Errorf("warning should suggest POINTER receiver: %s", warnings[0])
	}
}

func TestValueRecvMutation_IncDec(t *testing.T) {
	src := `package main

type Counter struct {
	count int
}

func (c Counter) Increment() {
	c.count++
}
`
	warnings := checkValueRecvMutation("test.go", "", src)
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning, got %d: %v", len(warnings), warnings)
	}
	if !contains(warnings[0], "c.count") {
		t.Errorf("warning should mention c.count: %s", warnings[0])
	}
}

func TestValueRecvMutation_PointerReceiverOK(t *testing.T) {
	src := `package main

type Counter struct {
	count int
}

func (c *Counter) Increment() {
	c.count++
	c.count = 100
}
`
	warnings := checkValueRecvMutation("test.go", "", src)
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings for pointer receiver, got %d: %v", len(warnings), warnings)
	}
}

func TestValueRecvMutation_NoMutationOK(t *testing.T) {
	src := `package main

type Counter struct {
	count int
}

func (c Counter) Value() int {
	return c.count
}
`
	warnings := checkValueRecvMutation("test.go", "", src)
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings for non-mutating method, got %d: %v", len(warnings), warnings)
	}
}

func TestValueRecvMutation_MultipleFields(t *testing.T) {
	src := `package main

import "time"

type Counter struct {
	count      int
	lastUpdate time.Time
}

func (c Counter) Update() {
	c.count++
	c.lastUpdate = time.Now()
}
`
	warnings := checkValueRecvMutation("test.go", "", src)
	if len(warnings) != 2 {
		t.Fatalf("expected 2 warnings, got %d: %v", len(warnings), warnings)
	}
}

func TestValueRecvMutation_NestedFuncLitOK(t *testing.T) {
	src := `package main

type Counter struct {
	count int
}

func (c Counter) Process() {
	go func() {
		c.count = 5
	}()
}
`
	warnings := checkValueRecvMutation("test.go", "", src)
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings (mutation in func literal), got %d: %v", len(warnings), warnings)
	}
}

func TestValueRecvMutation_AnonymousReceiver(t *testing.T) {
	src := `package main

type Foo struct {
	x int
}

func (Foo) Set() {
}
`
	warnings := checkValueRecvMutation("test.go", "", src)
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings for anonymous receiver, got %d: %v", len(warnings), warnings)
	}
}

func TestValueRecvMutation_UnderscoreReceiver(t *testing.T) {
	src := `package main

type Foo struct {
	x int
}

func (_ Foo) Set() {
	Foo{}.x = 1
}
`
	warnings := checkValueRecvMutation("test.go", "", src)
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings for _ receiver, got %d: %v", len(warnings), warnings)
	}
}

func TestValueRecvMutation_Decrement(t *testing.T) {
	src := `package main

type Gauge struct {
	val int
}

func (g Gauge) Dec() {
	g.val--
}
`
	warnings := checkValueRecvMutation("test.go", "", src)
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning for decrement, got %d: %v", len(warnings), warnings)
	}
}

func TestValueRecvMutation_CompoundAssignment(t *testing.T) {
	src := `package main

type Acc struct {
	total int
}

func (a Acc) Add(n int) {
	a.total += n
}
`
	warnings := checkValueRecvMutation("test.go", "", src)
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning for +=, got %d: %v", len(warnings), warnings)
	}
}

func TestValueRecvMutation_NonReceiverVarOK(t *testing.T) {
	src := `package main

type Counter struct {
	count int
}

func (c Counter) Process() {
	local := struct{ x int }{}
	local.x = 5
}
`
	warnings := checkValueRecvMutation("test.go", "", src)
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings for non-receiver var, got %d: %v", len(warnings), warnings)
	}
}

func TestValueRecvMutation_EmptyFile(t *testing.T) {
	warnings := checkValueRecvMutation("test.go", "", "")
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings for empty file, got %d", len(warnings))
	}
}

func TestValueRecvMutation_NonGoFile(t *testing.T) {
	warnings := checkValueRecvMutation("test.py", "", "def foo():\n  pass\n")
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings for non-Go file, got %d", len(warnings))
	}
}

func TestValueRecvMutation_Cap(t *testing.T) {
	src := `package main

type Big struct {
	a, b, c, d, e, f int
}

func (b Big) Mutate() {
	b.a = 1
	b.b = 2
	b.c = 3
	b.d = 4
	b.e = 5
}
`
	warnings := checkValueRecvMutation("test.go", "", src)
	if len(warnings) != 5 {
		t.Fatalf("expected 5 warnings (capped), got %d: %v", len(warnings), warnings)
	}
	// Last entry should be the truncation notice
	if !contains(warnings[4], "potentially more") {
		t.Errorf("last warning should be truncation notice: %s", warnings[4])
	}
}

func TestValueRecvMutation_ReceiverTypeShort(t *testing.T) {
	// vrmReceiverTypeShort extracts type name from AST expressions.
	// nil returns "?"
	if got := vrmReceiverTypeShort(nil); got != "?" {
		t.Errorf("expected '?' for nil, got %q", got)
	}
}
