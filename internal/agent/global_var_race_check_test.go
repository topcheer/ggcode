package agent

import (
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

func TestCheckGlobalVarRace_BasicMutation(t *testing.T) {
	src := `package main

var counter int

func main() {
	go func() {
		counter = 42
	}()
}
`
	warnings := checkGlobalVarRace("test.go", "", src)
	if len(warnings) == 0 {
		t.Fatal("expected at least 1 warning for unsynchronized global mutation in goroutine")
	}
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "counter") && strings.Contains(w, "data race") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected warning about 'counter' data race, got: %v", warnings)
	}
}

func TestCheckGlobalVarRace_WithMutex(t *testing.T) {
	src := `package main

import "sync"

var (
	counter int
	mu      sync.Mutex
)

func main() {
	go func() {
		mu.Lock()
		defer mu.Unlock()
		counter = 42
	}()
}
`
	warnings := checkGlobalVarRace("test.go", "", src)
	for _, w := range warnings {
		if strings.Contains(w, "counter") {
			t.Fatalf("expected no warning for mutex-protected global, got: %s", w)
		}
	}
}

func TestCheckGlobalVarRace_WithAtomic(t *testing.T) {
	src := `package main

import "sync/atomic"

var counter int64

func main() {
	go func() {
		atomic.StoreInt64(&counter, 42)
	}()
}
`
	warnings := checkGlobalVarRace("test.go", "", src)
	for _, w := range warnings {
		if strings.Contains(w, "counter") {
			t.Fatalf("expected no warning for atomic-protected global, got: %s", w)
		}
	}
}

func TestCheckGlobalVarRace_NoGlobals(t *testing.T) {
	src := `package main

func main() {
	x := 1
	go func() {
		x = 2
	}()
}
`
	warnings := checkGlobalVarRace("test.go", "", src)
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings (x is local, not global), got: %v", warnings)
	}
}

func TestCheckGlobalVarRace_NoGoroutine(t *testing.T) {
	src := `package main

var counter int

func main() {
	counter = 42
}
`
	warnings := checkGlobalVarRace("test.go", "", src)
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings (no goroutine), got: %v", warnings)
	}
}

func TestCheckGlobalVarRace_NonGoFile(t *testing.T) {
	src := "var x = 1"
	warnings := checkGlobalVarRace("test.py", "", src)
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings for .py file, got: %v", warnings)
	}
}

func TestCheckGlobalVarRace_EmptyContent(t *testing.T) {
	warnings := checkGlobalVarRace("test.go", "", "")
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings for empty content, got: %v", warnings)
	}
}

func TestCheckGlobalVarRace_MultipleGlobals(t *testing.T) {
	src := `package main

var (
	count int
	name  string
)

func main() {
	go func() {
		count = 1
		name = "hello"
	}()
}
`
	warnings := checkGlobalVarRace("test.go", "", src)
	if len(warnings) < 2 {
		t.Fatalf("expected at least 2 warnings (count and name), got %d: %v", len(warnings), warnings)
	}
}

func TestCheckGlobalVarRace_CompoundAssign(t *testing.T) {
	src := `package main

var count int

func main() {
	go func() {
		count += 1
	}()
}
`
	warnings := checkGlobalVarRace("test.go", "", src)
	if len(warnings) == 0 {
		t.Fatal("expected warning for compound assignment to global in goroutine")
	}
}

func TestCheckGlobalVarRace_GoFuncCall(t *testing.T) {
	src := `package main

var count int

func worker() {
	count = 99
}

func main() {
	go worker()
}
`
	// go worker() calls a named function, not a func literal.
	// Our check only inspects func literals inside go statements,
	// so this should NOT produce a warning.
	warnings := checkGlobalVarRace("test.go", "", src)
	for _, w := range warnings {
		if strings.Contains(w, "count") {
			t.Fatalf("expected no warning for named func call in go statement, got: %s", w)
		}
	}
}

func TestCheckGlobalVarRace_MaxWarnings(t *testing.T) {
	src := `package main

var a, b, c, d, e, f, g, h int

func main() {
	go func() {
		a = 1
		b = 2
		c = 3
		d = 4
		e = 5
		f = 6
		g = 7
		h = 8
	}()
}
`
	warnings := checkGlobalVarRace("test.go", "", src)
	if len(warnings) > maxGlobalRaceWarnings+1 {
		t.Fatalf("expected at most %d+1 warnings (cap + truncation notice), got %d", maxGlobalRaceWarnings, len(warnings))
	}
}

func TestCheckGlobalVarRace_SyncSuppressesAll(t *testing.T) {
	src := `package main

import "sync"

var (
	data map[string]int
	mu   sync.Mutex
)

func main() {
	go func() {
		mu.Lock()
		data["key"] = 1
		mu.Unlock()
	}()
}
`
	warnings := checkGlobalVarRace("test.go", "", src)
	for _, w := range warnings {
		if strings.Contains(w, "data") {
			t.Fatalf("expected no warning for mutex-protected map global, got: %s", w)
		}
	}
}

func TestGvrCollectGlobals(t *testing.T) {
	src := `package main

var foo int
var bar string
const baz = 42

type myType struct{}
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "test.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	globals := gvrCollectGlobals(file)
	if !globals["foo"] {
		t.Error("expected 'foo' in globals")
	}
	if !globals["bar"] {
		t.Error("expected 'bar' in globals")
	}
	if len(globals) != 2 {
		t.Errorf("expected exactly 2 globals (foo, bar), got %d: %v", len(globals), globals)
	}
}
