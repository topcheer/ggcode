package agent

import (
	"strings"
	"testing"
)

func TestAppendIgnored_StandaloneCall(t *testing.T) {
	src := `package main
func main() {
	var items []int
	append(items, 42)
}`
	warnings := checkAppendIgnored("test.go", "", src)
	if len(warnings) == 0 {
		t.Fatal("expected warning for standalone append() call")
	}
	if !strings.Contains(warnings[0], "discarded") {
		t.Errorf("unexpected warning: %s", warnings[0])
	}
}

func TestAppendIgnored_AssignedCorrect(t *testing.T) {
	src := `package main
func main() {
	items := []int{1, 2, 3}
	items = append(items, 42)
	_ = items
}`
	assertNoAppendWarnings(t, src)
}

func TestAppendIgnored_BlankAssign(t *testing.T) {
	src := `package main
func main() {
	items := []int{1, 2}
	_ = append(items, 3)
}`
	assertNoAppendWarnings(t, src)
}

func TestAppendIgnored_NestedInFunctionCall(t *testing.T) {
	src := `package main
import "fmt"
func main() {
	items := []int{1, 2}
	fmt.Println(append(items, 3))
}`
	assertNoAppendWarnings(t, src)
}

func TestAppendIgnored_MultipleViolations(t *testing.T) {
	src := `package main
func main() {
	var a []int
	append(a, 1)
	append(a, 2)
}`
	warnings := checkAppendIgnored("test.go", "", src)
	if len(warnings) < 2 {
		t.Fatalf("expected at least 2 warnings, got %d", len(warnings))
	}
}

func TestAppendIgnored_CapLimit(t *testing.T) {
	src := `package main
func main() {
	var a []int
	append(a, 1)
	append(a, 2)
	append(a, 3)
	append(a, 4)
	append(a, 5)
	append(a, 6)
}`
	warnings := checkAppendIgnored("test.go", "", src)
	if len(warnings) > maxAppendIgnoredWarnings+1 {
		t.Errorf("expected at most %d warnings, got %d", maxAppendIgnoredWarnings+1, len(warnings))
	}
}

func TestAppendIgnored_NonGoFile(t *testing.T) {
	warnings := checkAppendIgnored("test.py", "", "append(items, 42)")
	if len(warnings) != 0 {
		t.Errorf("expected no warnings for non-Go file")
	}
}

func TestAppendIgnored_EmptyContent(t *testing.T) {
	warnings := checkAppendIgnored("test.go", "", "")
	if len(warnings) != 0 {
		t.Errorf("expected no warnings for empty content")
	}
}

func TestAppendIgnored_SyntaxError(t *testing.T) {
	src := "package main\nfunc main() {\n\tappend("
	assertNoAppendWarnings(t, src)
}

func TestAppendIgnored_InCompoundAssign(t *testing.T) {
	src := `package main
func main() {
	items := []int{1}
	items = append(items, 2, 3)
	_ = items
}`
	assertNoAppendWarnings(t, src)
}

func TestAppendIgnored_MethodCallNotBuiltin(t *testing.T) {
	src := `package main
type Builder struct{}
func (b *Builder) append(s string) {}
func main() {
	b := &Builder{}
	b.append("hello")
}`
	assertNoAppendWarnings(t, src)
}

func assertNoAppendWarnings(t *testing.T, src string) {
	t.Helper()
	warnings := checkAppendIgnored("test.go", "", src)
	if len(warnings) != 0 {
		t.Errorf("expected no warnings, got: %v", warnings)
	}
}
