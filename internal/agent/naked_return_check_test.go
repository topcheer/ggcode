package agent

import (
	"testing"
)

func TestNakedReturn_LongFunction(t *testing.T) {
	src := `package main

func process(data []byte) (result int, err error) {
	result = 0
	for i := 0; i < len(data); i++ {
		result += int(data[i])
		if result > 100 {
			return
		}
		// padding line
		_ = i
		_ = i
		_ = i
		_ = i
		_ = i
		_ = i
		_ = i
		_ = i
		_ = i
		_ = i
		_ = i
		_ = i
		_ = i
		_ = i
		_ = i
	}
	return
}
`
	warnings := checkNakedReturn("test.go", "", src)
	if len(warnings) == 0 {
		t.Fatal("expected naked return warning for long function")
	}
}

func TestNakedReturn_ShortFunction(t *testing.T) {
	src := `package main

func short(x int) (y int) {
	y = x + 1
	return
}
`
	warnings := checkNakedReturn("test.go", "", src)
	if len(warnings) != 0 {
		t.Fatalf("expected no warning for short function, got %v", warnings)
	}
}

func TestNakedReturn_NoNamedReturns(t *testing.T) {
	src := `package main

func process(data []byte) (int, error) {
	// lots of padding lines to exceed threshold
	_ = data
	_ = data
	_ = data
	_ = data
	_ = data
	_ = data
	_ = data
	_ = data
	_ = data
	_ = data
	_ = data
	_ = data
	_ = data
	_ = data
	_ = data
	_ = data
	_ = data
	_ = data
	_ = data
	_ = data
	return 0, nil
}
`
	warnings := checkNakedReturn("test.go", "", src)
	if len(warnings) != 0 {
		t.Fatalf("expected no warning for unnamed returns, got %v", warnings)
	}
}

func TestNakedReturn_DeltaAware(t *testing.T) {
	src := `package main

func process(data []byte) (result int, err error) {
	result = 0
	for i := 0; i < len(data); i++ {
		result += int(data[i])
		if result > 100 {
			return
		}
		_ = i
		_ = i
		_ = i
		_ = i
		_ = i
		_ = i
		_ = i
		_ = i
		_ = i
		_ = i
		_ = i
		_ = i
		_ = i
		_ = i
		_ = i
	}
	return
}
`
	warnings := checkNakedReturn("test.go", src, src)
	if len(warnings) != 0 {
		t.Fatalf("expected delta-aware: no warning when same content, got %v", warnings)
	}
}

func TestNakedReturn_ExplicitReturn(t *testing.T) {
	src := `package main

func process(data []byte) (result int, err error) {
	result = 0
	for i := 0; i < len(data); i++ {
		result += int(data[i])
		if result > 100 {
			return result, nil
		}
		_ = i
		_ = i
		_ = i
		_ = i
		_ = i
		_ = i
		_ = i
		_ = i
		_ = i
		_ = i
		_ = i
		_ = i
		_ = i
		_ = i
		_ = i
	}
	return result, nil
}
`
	warnings := checkNakedReturn("test.go", "", src)
	if len(warnings) != 0 {
		t.Fatalf("expected no warning for explicit returns, got %v", warnings)
	}
}

func TestNakedReturn_TestFileSkipped(t *testing.T) {
	src := `package main

func process(data []byte) (result int, err error) {
	result = 0
	for i := 0; i < len(data); i++ {
		result += int(data[i])
		if result > 100 {
			return
		}
		_ = i
		_ = i
		_ = i
		_ = i
		_ = i
		_ = i
		_ = i
		_ = i
		_ = i
		_ = i
		_ = i
		_ = i
		_ = i
		_ = i
		_ = i
	}
	return
}
`
	warnings := checkNakedReturn("test_test.go", "", src)
	if len(warnings) != 0 {
		t.Fatalf("expected no warning for test file, got %v", warnings)
	}
}

func TestNakedReturn_MultipleFunctions(t *testing.T) {
	src := `package main

func longFunc1() (a int, b int) {
	a = 1
	b = 2
	// padding
	_ = a
	_ = a
	_ = a
	_ = a
	_ = a
	_ = a
	_ = a
	_ = a
	_ = a
	_ = a
	_ = a
	_ = a
	_ = a
	_ = a
	_ = a
	_ = a
	return
}

func longFunc2() (x int, y int) {
	x = 1
	y = 2
	// padding
	_ = x
	_ = x
	_ = x
	_ = x
	_ = x
	_ = x
	_ = x
	_ = x
	_ = x
	_ = x
	_ = x
	_ = x
	_ = x
	_ = x
	_ = x
	_ = x
	return
}
`
	warnings := checkNakedReturn("test.go", "", src)
	if len(warnings) < 2 {
		t.Fatalf("expected at least 2 warnings, got %d: %v", len(warnings), warnings)
	}
}

func TestNakedReturn_NestedGoroutine(t *testing.T) {
	src := `package main

func outer() (result int) {
	result = 0
	go func() (inner int) {
		inner = 42
		// padding for inner func
		_ = inner
		_ = inner
		_ = inner
		_ = inner
		_ = inner
		_ = inner
		_ = inner
		_ = inner
		_ = inner
		_ = inner
		_ = inner
		_ = inner
		_ = inner
		_ = inner
		_ = inner
		_ = inner
		return
	}()
	// padding for outer
	_ = result
	_ = result
	_ = result
	_ = result
	_ = result
	_ = result
	_ = result
	_ = result
	_ = result
	_ = result
	_ = result
	_ = result
	_ = result
	_ = result
	_ = result
	_ = result
	return
}
`
	// Only the goroutine func literal has named returns in this test.
	// Our checker only looks at FuncDecl, not FuncLit, so we only catch outer.
	warnings := checkNakedReturn("test.go", "", src)
	if len(warnings) == 0 {
		t.Fatal("expected at least 1 warning for outer function")
	}
}
