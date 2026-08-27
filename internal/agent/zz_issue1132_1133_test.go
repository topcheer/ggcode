package agent

import (
	"strings"
	"testing"
)

// --- Issue #1132: collectReboundVars only scanned the loop body's top level,
// so "item := item" written inside nested blocks was missed and captures were
// reported even though the variable had been correctly rebound. ---

// TestIssue1132_NestedBlockRebindSafe guards that a v := v rebinding inside a
// nested if block suppresses captures written after it.
func TestIssue1132_NestedBlockRebindSafe(t *testing.T) {
	src := `package main

func process(items []int) {
	for _, item := range items {
		if len(items) > 0 {
			item := item
			go func() {
				println(item)
			}()
		}
	}
}
`
	warnings := checkLoopVarCapture("example.go", "", src)
	if len(warnings) != 0 {
		t.Fatalf("#1132: nested-block rebinding must silence later captures, got: %v", warnings)
	}
}

// TestIssue1132_NestedForBlockRebindSafe covers rebinding nested inside a
// plain for block and inside a switch block.
func TestIssue1132_NestedForBlockRebindSafe(t *testing.T) {
	src := `package main

func process(items []int, n int) {
	for _, item := range items {
		switch n {
		case 1:
			item := item
			go func() {
				println(item)
			}()
		default:
			item := item
			defer func() {
				println(item)
			}()
		}
	}
}
`
	warnings := checkLoopVarCapture("example.go", "", src)
	if len(warnings) != 0 {
		t.Fatalf("#1132: switch-nested rebinding must silence captures, got: %v", warnings)
	}
}

// TestIssue1132_RebindAfterCaptureDoesNotSilence guards that recursion did
// not over-suppress: a rebinding placed AFTER a go statement cannot protect
// the goroutine launched before it.
func TestIssue1132_RebindAfterCaptureDoesNotSilence(t *testing.T) {
	src := `package main

func process(items []int) {
	for _, item := range items {
		go func() {
			println(item)
		}()
		item := item
	}
}
`
	warnings := checkLoopVarCapture("example.go", "", src)
	if len(warnings) == 0 {
		t.Fatal("#1132: capture before the rebinding must still be warned")
	}
}

// TestIssue1132_RebindOnlyProtectsLaterCaptures checks per-capture-point
// filtering: the first goroutine (before any rebinding) keeps its warning,
// the second one (after the rebinding) is silenced.
func TestIssue1132_RebindOnlyProtectsLaterCaptures(t *testing.T) {
	src := `package main

func process(items []int) {
	for _, item := range items {
		go func() {
			println(item)
		}()
		item := item
		go func() {
			println(item)
		}()
	}
}
`
	warnings := checkLoopVarCapture("example.go", "", src)
	if len(warnings) != 1 {
		t.Fatalf("#1132: expected exactly 1 warning (pre-rebinding capture), got %d: %v", len(warnings), warnings)
	}
}

// TestIssue1132_InClosureRebindDoesNotSilenceSibling guards the inverse
// hazard: an in-closure "item := item" scopes to that closure only and must
// not suppress other closures that still share the loop variable.
func TestIssue1132_InClosureRebindDoesNotSilenceSibling(t *testing.T) {
	src := `package main

func process(items []int) {
	for _, item := range items {
		go func() {
			item := item
			println(item)
		}()
		go func() {
			println(item)
		}()
	}
}
`
	warnings := checkLoopVarCapture("example.go", "", src)
	if len(warnings) == 0 {
		t.Fatal("#1132: sibling closure without rebinding must still be warned")
	}
}

// --- Issue #1133: identReferenced matched identifiers by name only, so a
// same-name declaration inside the closure was treated as a capture of the
// outer loop variable even when nothing was captured. ---

// TestIssue1133_ClosureLocalSameNameAssignSafe guards the exact reported
// pattern: the closure redeclares the name locally, nothing leaks out.
func TestIssue1133_ClosureLocalSameNameAssignSafe(t *testing.T) {
	src := `package main

func fetchOther() int { return 1 }

func process(items []int) {
	for _, item := range items {
		go func() {
			item := fetchOther()
			println(item)
		}()
	}
}
`
	warnings := checkLoopVarCapture("example.go", "", src)
	if len(warnings) != 0 {
		t.Fatalf("#1133: closure-local same-name assign is not a capture, got: %v", warnings)
	}
}

// TestIssue1133_CaptureBeforeShadowDeclStillWarns keeps the false-negative
// direction closed: referencing the loop variable BEFORE the local shadowing
// declaration is a genuine capture.
func TestIssue1133_CaptureBeforeShadowDeclStillWarns(t *testing.T) {
	src := `package main

func fetchOther() int { return 1 }

func process(items []int) {
	for _, item := range items {
		go func() {
			println(item)
			item := fetchOther()
			println(item)
		}()
	}
}
`
	warnings := checkLoopVarCapture("example.go", "", src)
	if len(warnings) == 0 {
		t.Fatal("#1133: capture before the shadow declaration must still be warned")
	}
}

// TestIssue1133_ShadowByVarAndRangeDefine covers the other define forms:
// "var x T" declarations and range clause variables declared inside the
// closure must not be counted as loop-variable captures.
func TestIssue1133_ShadowByVarAndRangeDefine(t *testing.T) {
	fetchSrc := `package main

func fetchKeys() []string { return nil }

func process(items map[string]int) {
	for _, item := range items {
		go func() {
			var item int = 42
			for key := range items {
				_ = key
				_ = item
			}
			_ = len(fetchKeys())
		}()
	}
}
`
	warnings := checkLoopVarCapture("shadow.go", "", fetchSrc)
	if len(warnings) != 0 {
		t.Fatalf("#1133: var/range shadows are not captures, got: %v", warnings)
	}

	deferSrc := `package main

func fetchOther() []string { return nil }

func process(items []string) {
	for _, item := range items {
		defer func() {
			for _, item := range fetchOther() {
				println(item)
			}
		}()
	}
}
`
	warnings = checkLoopVarCapture("defer.go", "", deferSrc)
	if len(warnings) != 0 {
		t.Fatalf("#1133: range-define shadow in deferred closure is not a capture, got: %v", warnings)
	}
}

// TestIssue1133_InnerClosureParamShadow covers parameters of a function
// literal nested INSIDE the captured closure: references after such a
// parameter resolve to the parameter, not the loop variable.
func TestIssue1133_InnerClosureParamShadow(t *testing.T) {
	src := `package main

func run(cb func(int)) {}

func process(items []int) {
	for _, item := range items {
		go func() {
			run(func(item int) {
				println(item)
			})
		}()
	}
}
`
	warnings := checkLoopVarCapture("example.go", "", src)
	if len(warnings) != 0 {
		t.Fatalf("#1133: inner parameter shadowing must not be flagged, got: %v", warnings)
	}
}

// TestIssue1133_GenuineCaptureStillWarned makes sure the shadow analysis did
// not turn the detector off for real captures.
func TestIssue1133_GenuineCaptureStillWarned(t *testing.T) {
	src := `package main

func process(items []int) {
	for _, item := range items {
		go func() {
			println(item)
		}()
	}
}
`
	warnings := checkLoopVarCapture("example.go", "", src)
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "'item'") && strings.Contains(w, "captured in goroutine") {
			found = true
		}
	}
	if !found {
		t.Fatalf("#1133: genuine capture must still warn, got: %v", warnings)
	}
}
