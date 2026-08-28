package agent

import (
	"strings"
	"testing"
)

func TestTypeSwitchExhaustive_NoDefaultMultipleCases(t *testing.T) {
	src := `package x
type MyError struct{}
type OtherError struct{}
func handleErr(err error) string {
	switch e := err.(type) {
	case *MyError:
		return "my"
	case *OtherError:
		return "other"
	}
	return ""
}
`
	warnings := checkTypeSwitchExhaustive("test.go", "", src)
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning, got %d: %v", len(warnings), warnings)
	}
	if !strings.Contains(warnings[0], "no default branch") {
		t.Errorf("unexpected warning: %s", warnings[0])
	}
}

func TestTypeSwitchExhaustive_HasDefault(t *testing.T) {
	src := `package x
type MyError struct{}
type OtherError struct{}
func handleErr(err error) string {
	switch e := err.(type) {
	case *MyError:
		return "my"
	case *OtherError:
		return "other"
	default:
		return "unknown"
	}
}
`
	warnings := checkTypeSwitchExhaustive("test.go", "", src)
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings, got %d: %v", len(warnings), warnings)
	}
}

func TestTypeSwitchExhaustive_SingleCaseNotFlagged(t *testing.T) {
	src := `package x
type MyError struct{}
func handleErr(err error) string {
	switch e := err.(type) {
	case *MyError:
		return "my"
	}
	return ""
}
`
	warnings := checkTypeSwitchExhaustive("test.go", "", src)
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings for single-case switch, got %d", len(warnings))
	}
}

func TestTypeSwitchExhaustive_DeltaAware(t *testing.T) {
	oldSrc := `package x
type A struct{}
type B struct{}
func f(v interface{}) {
	switch v := v.(type) {
	case *A:
	case *B:
	}
}
`
	newSrc := `package x
type A struct{}
type B struct{}
type C struct{}
func f(v interface{}) {
	switch v := v.(type) {
	case *A:
	case *B:
	case *C:
	}
}
`
	warnings := checkTypeSwitchExhaustive("test.go", oldSrc, newSrc)
	// The switch existed in old content too, so delta-aware should suppress it
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings (delta-aware suppression), got %d: %v", len(warnings), warnings)
	}
}

func TestTypeSwitchExhaustive_NewSwitch(t *testing.T) {
	oldSrc := `package x
func f(v interface{}) {}
`
	newSrc := `package x
type A struct{}
type B struct{}
func f(v interface{}) {
	switch v := v.(type) {
	case *A:
	case *B:
	}
}
`
	warnings := checkTypeSwitchExhaustive("test.go", oldSrc, newSrc)
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning for new switch, got %d", len(warnings))
	}
}

func TestTypeSwitchExhaustive_NonGoFile(t *testing.T) {
	src := `console.log("hello")`
	warnings := checkTypeSwitchExhaustive("test.js", "", src)
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings for non-Go file, got %d", len(warnings))
	}
}

// TestIssue1212_SecondSwitchInSameFuncReported verifies that adding a SECOND
// default-less type switch to a function that already has one is reported.
// The old funcName-only dedup key made both switches share a key, so the new
// one silently deduped away (#1212 direction 1).
func TestIssue1212_SecondSwitchInSameFuncReported(t *testing.T) {
	oldSrc := `package x
type A struct{}
type B struct{}
func f(v interface{}) {
	switch v := v.(type) {
	case *A:
	case *B:
	}
	_ = v
}
`
	newSrc := `package x
type A struct{}
type B struct{}
func f(v interface{}) {
	switch v := v.(type) {
	case *A:
	case *B:
	}
	switch v := v.(type) {
	case *A:
	case *B:
	}
	_ = v
}
`
	warnings := checkTypeSwitchExhaustive("test.go", oldSrc, newSrc)
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning for newly added second switch, got %d: %v", len(warnings), warnings)
	}
}

// TestIssue1212_RenameDoesNotReReport verifies that renaming a function
// without touching its switch does not re-report the pre-existing switch
// (#1212 direction 2). Relative-line keys are rename-stable.
func TestIssue1212_RenameDoesNotReReport(t *testing.T) {
	oldSrc := `package x
type A struct{}
type B struct{}
func f(v interface{}) {
	switch v := v.(type) {
	case *A:
	case *B:
	}
	_ = v
}
`
	newSrc := `package x
type A struct{}
type B struct{}
func handleValueWithLongerName(v interface{}) {
	switch v := v.(type) {
	case *A:
	case *B:
	}
	_ = v
}
`
	warnings := checkTypeSwitchExhaustive("test.go", oldSrc, newSrc)
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings for pure rename, got %d: %v", len(warnings), warnings)
	}
}

func TestTypeSwitchExhaustive_TestFile(t *testing.T) {
	src := `package x
type A struct{}
type B struct{}
func f(v interface{}) {
	switch v := v.(type) {
	case *A:
	case *B:
	}
}
`
	warnings := checkTypeSwitchExhaustive("test_test.go", "", src)
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings for test file, got %d", len(warnings))
	}
}

func TestTypeSwitchExhaustive_EmptyContent(t *testing.T) {
	warnings := checkTypeSwitchExhaustive("test.go", "", "")
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings for empty content, got %d", len(warnings))
	}
}

func TestTypeSwitchExhaustive_SyntaxError(t *testing.T) {
	src := `package x
func f(v interface{}) {
	switch v := v.(type) {
`
	warnings := checkTypeSwitchExhaustive("test.go", "", src)
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings for unparseable code, got %d", len(warnings))
	}
}

func TestTypeSwitchExhaustive_ThreeCasesNoDefault(t *testing.T) {
	src := `package x
type A struct{}
type B struct{}
type C struct{}
func f(v interface{}) int {
	switch v.(type) {
	case *A:
		return 1
	case *B:
		return 2
	case *C:
		return 3
	}
	return 0
}
`
	warnings := checkTypeSwitchExhaustive("test.go", "", src)
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning for 3-case switch, got %d", len(warnings))
	}
	if !strings.Contains(warnings[0], "3 case(s)") {
		t.Errorf("warning should mention case count: %s", warnings[0])
	}
}

func TestTypeSwitchExhaustive_AssignSwitch(t *testing.T) {
	// switch x := y.(type) pattern
	src := `package x
type A struct{}
type B struct{}
func f(y interface{}) int {
	switch x := y.(type) {
	case *A:
		_ = x
		return 1
	case *B:
		_ = x
		return 2
	}
	return 0
}
`
	warnings := checkTypeSwitchExhaustive("test.go", "", src)
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning, got %d", len(warnings))
	}
}

func TestTypeSwitchExhaustive_MaxWarnings(t *testing.T) {
	src := `package x
type A struct{}
type B struct{}
type C struct{}
type D struct{}
type E struct{}
type F struct{}
func f1(v interface{}) {
	switch v.(type) {
	case *A: case *B:
	}
}
func f2(v interface{}) {
	switch v.(type) {
	case *C: case *D:
	}
}
func f3(v interface{}) {
	switch v.(type) {
	case *E: case *F:
	}
}
func f4(v interface{}) {
	switch v.(type) {
	case *A: case *C:
	}
}
`
	warnings := checkTypeSwitchExhaustive("test.go", "", src)
	if len(warnings) > maxTypeSwitchWarnings {
		t.Fatalf("expected at most %d warnings, got %d", maxTypeSwitchWarnings, len(warnings))
	}
}
