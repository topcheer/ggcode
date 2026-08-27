package agent

import (
	"strings"
	"testing"
)

func TestCheckExcessiveParams_NoParams(t *testing.T) {
	warnings := checkExcessiveParams("test.go", "", `package main
func simple() int { return 1 }
`)
	if len(warnings) != 0 {
		t.Errorf("expected 0 warnings, got %d: %v", len(warnings), warnings)
	}
}

func TestCheckExcessiveParams_BelowThreshold(t *testing.T) {
	warnings := checkExcessiveParams("test.go", "", `package main
func process(a, b, c int, d string, e bool) {}
`)
	if len(warnings) != 0 {
		t.Errorf("expected 0 warnings for 5 params, got %d: %v", len(warnings), warnings)
	}
}

func TestCheckExcessiveParams_AtThreshold(t *testing.T) {
	warnings := checkExcessiveParams("test.go", "", `package main
func complex(a, b, c int, d, e string, f bool) {}
`)
	if len(warnings) == 0 {
		t.Error("expected warning for 6 params, got 0")
	}
	if !strings.Contains(warnings[0], "complex") {
		t.Errorf("warning should mention function name, got: %s", warnings[0])
	}
	if !strings.Contains(warnings[0], "6 parameters") {
		t.Errorf("warning should mention 6 parameters, got: %s", warnings[0])
	}
}

func TestCheckExcessiveParams_WithReceiver(t *testing.T) {
	warnings := checkExcessiveParams("test.go", "", `package main
type Server struct{}
func (s *Server) handle(a, b, c, d, e int, f string) {}
`)
	if len(warnings) == 0 {
		t.Error("expected warning: 5 params + 1 receiver = 6")
	}
}

func TestCheckExcessiveParams_DeltaAware(t *testing.T) {
	old := `package main
func complex(a, b, c int, d, e string, f bool) {}
`
	newContent := old
	warnings := checkExcessiveParams("test.go", old, newContent)
	if len(warnings) != 0 {
		t.Errorf("delta-aware: expected 0 warnings for unchanged content, got %d", len(warnings))
	}
}

func TestCheckExcessiveParams_NewInstance(t *testing.T) {
	old := `package main
func complex(a, b, c int, d, e string, f bool) {}
`
	newContent := old + `
func another(x, y, z int, w, v string, u bool) {}
`
	warnings := checkExcessiveParams("test.go", old, newContent)
	if len(warnings) == 0 {
		t.Error("expected 1 warning for newly added function")
	}
	if !strings.Contains(warnings[0], "another") {
		t.Errorf("should flag the NEW function 'another', got: %s", warnings[0])
	}
}

func TestCheckExcessiveParams_SkipsTestFunctions(t *testing.T) {
	// #1187: the Test/Benchmark name exemption applies only to _test.go
	// files. go test never compiles such functions from production code, so
	// a Test-prefixed func in a non-test file IS checked.
	src := `package main
func TestMyFunc(t, ctx, cfg, db, logger, client struct{}) {}
`
	if warnings := checkExcessiveParams("foo_test.go", "", src); len(warnings) != 0 {
		t.Errorf("expected 0 warnings for test-file test function, got %d", len(warnings))
	}
	if warnings := checkExcessiveParams("test.go", "", src); len(warnings) == 0 {
		t.Error("expected 1 warning for Test-prefixed function in production file")
	}
}

func TestCheckExcessiveParams_NonGoFile(t *testing.T) {
	warnings := checkExcessiveParams("test.py", "", "def f(a,b,c,d,e,f): pass")
	if len(warnings) != 0 {
		t.Errorf("expected 0 warnings for non-Go file, got %d", len(warnings))
	}
}

func TestCheckExcessiveParams_MaxWarnings(t *testing.T) {
	src := `package main
func f1(a, b, c int, d, e string, f bool) {}
func f2(a, b, c int, d, e string, f bool) {}
func f3(a, b, c int, d, e string, f bool) {}
func f4(a, b, c int, d, e string, f bool) {}
func f5(a, b, c int, d, e string, f bool) {}
`
	warnings := checkExcessiveParams("test.go", "", src)
	// maxParamCountWarnings = 3, plus possibly 1 "more" message
	if len(warnings) > maxParamCountWarnings+1 {
		t.Errorf("expected at most %d warnings, got %d", maxParamCountWarnings+1, len(warnings))
	}
}
