package agent

import (
	"strings"
	"testing"
)

func TestCheckNestingDepth_ShallowNoWarning(t *testing.T) {
	src := `package main

func process(items []int) int {
	if len(items) == 0 {
		return 0
	}
	for _, item := range items {
		if item > 0 {
			return item
		}
	}
	return -1
}
`
	warnings := checkNestingDepth("test.go", "", src)
	if len(warnings) != 0 {
		t.Errorf("expected no warnings for shallow nesting (depth 2), got %d: %v", len(warnings), warnings)
	}
}

func TestCheckNestingDepth_DeepNestingWarning(t *testing.T) {
	src := `package main

func process(data map[string][]int) int {
	for key, values := range data {
		if key != "" {
			for _, v := range values {
				if v > 0 {
					if v < 100 {
						return v
					}
				}
			}
		}
	}
	return 0
}
`
	warnings := checkNestingDepth("test.go", "", src)
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning for depth 5, got %d: %v", len(warnings), warnings)
	}
	if !strings.Contains(warnings[0], "depth 5") {
		t.Errorf("warning should mention depth 5, got: %s", warnings[0])
	}
	if !strings.Contains(warnings[0], "process") {
		t.Errorf("warning should mention function 'process', got: %s", warnings[0])
	}
}

func TestCheckNestingDepth_ElseIfFlatChain(t *testing.T) {
	src := `package main

func classify(n int) string {
	if n == 1 {
		return "one"
	} else if n == 2 {
		return "two"
	} else if n == 3 {
		return "three"
	} else if n == 4 {
		return "four"
	} else if n == 5 {
		return "five"
	} else if n == 6 {
		return "six"
	}
	return "other"
}
`
	warnings := checkNestingDepth("test.go", "", src)
	if len(warnings) != 0 {
		t.Errorf("else-if chain should not trigger nesting warning, got %d: %v", len(warnings), warnings)
	}
}

func TestCheckNestingDepth_ElseIfWithDeepBody(t *testing.T) {
	src := `package main

func classify(data []int) int {
	if len(data) == 0 {
		return 0
	} else if len(data) == 1 {
		for _, v := range data {
			if v > 0 {
				if v < 10 {
					if v == 5 {
						return v
					}
				}
			}
		}
	}
	return -1
}
`
	warnings := checkNestingDepth("test.go", "", src)
	// The else-if body has depth 4 (else-if=1, for=2, if=3, if=4, if=5)
	// Actually: top if = depth 1, else-if body starts at depth 1
	// for=2, if=3, if=4, if=5 -> depth 5
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning for deep nesting in else-if body, got %d: %v", len(warnings), warnings)
	}
}

func TestCheckNestingDepth_SwitchNesting(t *testing.T) {
	src := `package main

func handle(cmd string, data []int) int {
	switch cmd {
	case "process":
		for _, v := range data {
			if v > 0 {
				if v < 10 {
					if v == 5 {
						return v
					}
				}
			}
		}
	}
	return 0
}
`
	warnings := checkNestingDepth("test.go", "", src)
	// switch=1, case body=1, for=2, if=3, if=4, if=5 -> depth 5
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning for switch nesting, got %d: %v", len(warnings), warnings)
	}
}

func TestCheckNestingDepth_SelectNesting(t *testing.T) {
	src := `package main

func handle(ch chan int, data []int) int {
	select {
	case v := <-ch:
		if v > 0 {
			for _, d := range data {
				if d > 0 {
					if d < 10 {
						if d == v {
							return d
						}
					}
				}
			}
		}
	}
	return 0
}
`
	warnings := checkNestingDepth("test.go", "", src)
	// select=1, comm clause body=1, if=2, for=3, if=4, if=5, if=6 -> depth 6
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning for select nesting, got %d: %v", len(warnings), warnings)
	}
}

func TestCheckNestingDepth_DeltaAware_PreExistingNotFlagged(t *testing.T) {
	oldSrc := `package main

func process(data []int) int {
	for _, v := range data {
		if v > 0 {
			if v < 10 {
				if v == 5 {
					return v
				}
			}
		}
	}
	return 0
}
`
	// Same content, no change
	warnings := checkNestingDepth("test.go", oldSrc, oldSrc)
	if len(warnings) != 0 {
		t.Errorf("pre-existing deep nesting should not be flagged, got %d: %v", len(warnings), warnings)
	}
}

func TestCheckNestingDepth_DeltaAware_WorsenedNesting(t *testing.T) {
	oldSrc := `package main

func process(data []int) int {
	for _, v := range data {
		if v > 0 {
			return v
		}
	}
	return 0
}
`
	newSrc := `package main

func process(data []int) int {
	for _, v := range data {
		if v > 0 {
			if v < 10 {
				if v == 5 {
					if v > 0 {
						return v
					}
				}
			}
		}
	}
	return 0
}
`
	warnings := checkNestingDepth("test.go", oldSrc, newSrc)
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning for worsened nesting, got %d: %v", len(warnings), warnings)
	}
	if !strings.Contains(warnings[0], "depth 5") {
		t.Errorf("warning should mention depth 5, got: %s", warnings[0])
	}
}

func TestCheckNestingDepth_NonGoFileSkipped(t *testing.T) {
	src := `function deep() { if (a) { if (b) { if (c) { if (d) { if (e) {} } } } } }`
	warnings := checkNestingDepth("test.js", "", src)
	if len(warnings) != 0 {
		t.Errorf("non-Go files should be skipped, got %d warnings", len(warnings))
	}
}

func TestCheckNestingDepth_EmptyContentSkipped(t *testing.T) {
	warnings := checkNestingDepth("test.go", "", "")
	if len(warnings) != 0 {
		t.Errorf("empty content should be skipped, got %d warnings", len(warnings))
	}
}

func TestCheckNestingDepth_ParseErrorReturnsNil(t *testing.T) {
	src := `package main
func broken( {`
	warnings := checkNestingDepth("test.go", "", src)
	if len(warnings) != 0 {
		t.Errorf("parse errors should return no warnings, got %d", len(warnings))
	}
}

func TestCheckNestingDepth_LabeledStmtHandled(t *testing.T) {
	src := `package main

func process(items []int) int {
loop:
	for _, v := range items {
		if v > 0 {
			if v < 10 {
				if v == 5 {
					if v > 0 {
						break loop
					}
				}
			}
		}
	}
	return 0
}
`
	warnings := checkNestingDepth("test.go", "", src)
	// for=1, if=2, if=3, if=4, if=5 -> depth 5
	if len(warnings) != 1 {
		t.Fatalf("labeled stmt should not prevent detection, got %d: %v", len(warnings), warnings)
	}
}

func TestCheckNestingDepth_MultipleFunctionsCapped(t *testing.T) {
	src := `package main

func deepFunc1(data []int) int {
	for _, v := range data {
		if v > 0 {
			if v < 10 {
				if v == 5 {
					if v > 0 {
						return v
					}
				}
			}
		}
	}
	return 0
}

func deepFunc2(data []int) int {
	for _, v := range data {
		if v > 0 {
			if v < 10 {
				if v == 5 {
					if v > 0 {
						return v
					}
				}
			}
		}
	}
	return 0
}

func deepFunc3(data []int) int {
	for _, v := range data {
		if v > 0 {
			if v < 10 {
				if v == 5 {
					if v > 0 {
						return v
					}
				}
			}
		}
	}
	return 0
}

func deepFunc4(data []int) int {
	for _, v := range data {
		if v > 0 {
			if v < 10 {
				if v == 5 {
					if v > 0 {
						return v
					}
				}
			}
		}
	}
	return 0
}
`
	warnings := checkNestingDepth("test.go", "", src)
	// 4 deep functions at depth 5, warnings capped at maxNestingWarnings=3
	if len(warnings) != 4 {
		t.Fatalf("expected 4 entries (3 warnings + 1 summary), got %d: %v", len(warnings), warnings)
	}
	if !strings.Contains(warnings[3], "and 1 more") {
		t.Errorf("4th entry should be a summary, got: %s", warnings[3])
	}
}

func TestCheckNestingDepth_BoundaryDepth4NoWarning(t *testing.T) {
	src := `package main

func process(data []int) int {
	for _, v := range data {
		if v > 0 {
			if v < 10 {
				if v == 5 {
					return v
				}
			}
		}
	}
	return 0
}
`
	warnings := checkNestingDepth("test.go", "", src)
	// for=1, if=2, if=3, if=4 -> depth 4, threshold is > 4, so no warning
	if len(warnings) != 0 {
		t.Errorf("depth 4 should NOT trigger warning (threshold is > %d), got %d: %v", maxNestingDepth, len(warnings), warnings)
	}
}

func TestCheckNestingDepth_NestedBlocks(t *testing.T) {
	src := `package main

func process() int {
	for i := 0; i < 10; i++ {
		if i > 0 {
			if i > 1 {
				if i > 2 {
					if i > 3 {
						return i
					}
				}
			}
		}
	}
	return 0
}
`
	warnings := checkNestingDepth("test.go", "", src)
	// for=1, if=2, if=3, if=4, if=5 -> depth 5
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning for nested control flow, got %d: %v", len(warnings), warnings)
	}
}

func TestComputeMaxNesting_EmptyBody(t *testing.T) {
	depth := computeMaxNesting(nil)
	if depth != 0 {
		t.Errorf("nil body should have depth 0, got %d", depth)
	}
}

func TestFindDeepNesting_EmptySource(t *testing.T) {
	violations := findDeepNesting("")
	if violations != nil {
		t.Errorf("empty source should return nil, got %v", violations)
	}
}

func TestCheckNestingDepth_TypeSwitchNesting(t *testing.T) {
	src := `package main

func process(v interface{}) int {
	switch t := v.(type) {
	case int:
		if t > 0 {
			if t < 10 {
				if t == 5 {
					if t > 0 {
						return t
					}
				}
			}
		}
	}
	return 0
}
`
	warnings := checkNestingDepth("test.go", "", src)
	// type switch=1, case body=1, if=2, if=3, if=4, if=5 -> depth 5
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning for type switch nesting, got %d: %v", len(warnings), warnings)
	}
}
