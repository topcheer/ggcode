package agent

import (
	"strings"
	"testing"
)

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
