package agent

import (
	"strings"
	"testing"
)

func TestCheckPrintfFormat_VariadicSpreadNoVerbCountWarning(t *testing.T) {
	src := `package main

import (
	"fmt"
	"log"
)

func f(kv []any, parts []string) {
	fmt.Sprintf("%s=%v\n", kv...)
	log.Printf("%s %s", parts...)
}
`
	warnings := checkPrintfFormat("test.go", "", src)
	if len(warnings) != 0 {
		t.Fatalf("variadic spread must not warn, got %d: %v", len(warnings), warnings)
	}
}

func TestCheckPrintfFormat_ExplicitIndexNoVerbCountWarning(t *testing.T) {
	src := `package main

import "fmt"

func f(a string) string { return fmt.Sprintf("%[1]s and %[1]s", a) }
`
	warnings := checkPrintfFormat("test.go", "", src)
	if len(warnings) != 0 {
		t.Fatalf("explicit index verbs must not warn, got %d: %v", len(warnings), warnings)
	}
}

func TestCheckPrintfFormat_ForwardingWrapperNoWarning(t *testing.T) {
	src := `package main

import "log"

func logf(format string, args ...any) { log.Printf(format, args...) }
func warnf(format string, args ...any) { log.Printf("[WARN] "+format, args...) }
`
	warnings := checkPrintfFormat("test.go", "", src)
	if len(warnings) != 0 {
		t.Fatalf("forwarding wrappers must not warn, got %d: %v", len(warnings), warnings)
	}
}

func TestCheckPrintfFormat_TruePositivesSurviveExemptions(t *testing.T) {
	src := `package main

import (
	"fmt"
	"log"
)

func f() {
	u := getUserInput()
	log.Printf(u)
	fmt.Printf("%d %d\n", 1)
}
`
	warnings := checkPrintfFormat("test.go", "", src)
	sawNonconstant, sawVerbCount := false, false
	for _, w := range warnings {
		if strings.Contains(w, "non-constant format string") {
			sawNonconstant = true
		}
		if strings.Contains(w, "verb(s)") {
			sawVerbCount = true
		}
	}
	if !sawNonconstant {
		t.Fatal("local-variable format must still warn (injection risk)")
	}
	if !sawVerbCount {
		t.Fatal("plain verb-count mismatch (no spread) must still warn")
	}
}

func TestCheckPrintfFormat_NonConstantFormat(t *testing.T) {
	src := `package main

import (
	"fmt"
	"log"
)

func process(name string) {
	log.Printf(name)
	fmt.Sprintf(name)
}
`
	warnings := checkPrintfFormat("test.go", "", src)
	if len(warnings) != 2 {
		t.Fatalf("expected 2 nonconstant-format warnings, got %d: %v", len(warnings), warnings)
	}
	for _, w := range warnings {
		if !strings.Contains(w, "non-constant format string") {
			t.Errorf("unexpected warning: %s", w)
		}
	}
}

func TestCheckPrintfFormat_ConstantFormatOK(t *testing.T) {

	src := `package main

import "log"

const FORMAT = "hello"

func process() {
	log.Printf(FORMAT)
}
`
	warnings := checkPrintfFormat("test.go", "", src)
	if len(warnings) != 0 {
		t.Errorf("expected 0 warnings for likely-constant format, got %d: %v", len(warnings), warnings)
	}
}

func TestCheckPrintfFormat_StringLiteralOK(t *testing.T) {
	src := `package main

import "fmt"

func process(name string) {
	fmt.Sprintf("hello %s", name)
}
`
	warnings := checkPrintfFormat("test.go", "", src)
	if len(warnings) != 0 {
		t.Errorf("expected 0 warnings for correct literal format, got %d: %v", len(warnings), warnings)
	}
}

func TestCheckPrintfFormat_VerbCountMismatch(t *testing.T) {
	src := `package main

import "fmt"

func process(name string) {
	fmt.Sprintf("hello %s %d", name)
}
`
	warnings := checkPrintfFormat("test.go", "", src)
	if len(warnings) != 1 {
		t.Fatalf("expected 1 verb-count warning, got %d: %v", len(warnings), warnings)
	}
	if !strings.Contains(warnings[0], "format string has") {
		t.Errorf("unexpected warning: %s", warnings[0])
	}
}

func TestCheckPrintfFormat_VerbCountExtraArgs(t *testing.T) {
	src := `package main

import "fmt"

func process(a, b, c int) {
	fmt.Sprintf("count: %d", a, b, c)
}
`
	warnings := checkPrintfFormat("test.go", "", src)
	if len(warnings) != 1 {
		t.Fatalf("expected 1 verb-count warning for too many args, got %d: %v", len(warnings), warnings)
	}
}

func TestCheckPrintfFormat_RedundantSprintf(t *testing.T) {
	src := `package main

import "fmt"

func process(n int) {
	fmt.Println(fmt.Sprintf("count: %d", n))
}
`
	warnings := checkPrintfFormat("test.go", "", src)
	if len(warnings) != 1 {
		t.Fatalf("expected 1 redundant-sprintf warning, got %d: %v", len(warnings), warnings)
	}
	if !strings.Contains(warnings[0], "double-formats") {
		t.Errorf("unexpected warning: %s", warnings[0])
	}
}

func TestCheckPrintfFormat_DeltaAware(t *testing.T) {

	oldSrc := `package main

import "log"

func a(name string) {
	log.Printf(name)
}
`
	newSrc := oldSrc + `
func b(other string) {
	log.Printf(other)
}
`

	warnings := checkPrintfFormat("test.go", oldSrc, newSrc)
	if len(warnings) != 1 {
		t.Fatalf("expected 1 delta warning, got %d: %v", len(warnings), warnings)
	}
}

func TestCheckPrintfFormat_ErrorfErrDotErrorOK(t *testing.T) {

	src := `package main

import "fmt"

func wrap(err error) error {
	return fmt.Errorf(err.Error())
}
`
	warnings := checkPrintfFormat("test.go", "", src)
	if len(warnings) != 0 {
		t.Errorf("expected 0 warnings for fmt.Errorf(err.Error()), got %d: %v", len(warnings), warnings)
	}
}

func TestCheckPrintfFormat_FprintfVerbCount(t *testing.T) {

	src := `package main

import "fmt"

func process(w interface{ Write([]byte) (int, error) }) {
	fmt.Fprintf(w, "hello %s %d")
}
`
	warnings := checkPrintfFormat("test.go", "", src)
	if len(warnings) != 1 {
		t.Fatalf("expected 1 verb-count warning for Fprintf, got %d: %v", len(warnings), warnings)
	}
	if !strings.Contains(warnings[0], "format string has") {
		t.Errorf("unexpected warning: %s", warnings[0])
	}
}

func TestCheckPrintfFormat_NonGoFile(t *testing.T) {
	warnings := checkPrintfFormat("test.py", "", "log.Printf(name)")
	if len(warnings) != 0 {
		t.Errorf("expected 0 warnings for non-Go file, got %d", len(warnings))
	}
}

func TestCheckPrintfFormat_EmptyContent(t *testing.T) {
	warnings := checkPrintfFormat("test.go", "", "")
	if len(warnings) != 0 {
		t.Errorf("expected 0 warnings for empty content, got %d", len(warnings))
	}
}

func TestCheckPrintfFormat_SyntaxError(t *testing.T) {

	src := `package main
import "fmt"
func broken( {
`
	warnings := checkPrintfFormat("test.go", "", src)
	if len(warnings) != 0 {
		t.Errorf("expected 0 warnings for file with syntax errors, got %d", len(warnings))
	}
}

func TestCountFormatVerbs(t *testing.T) {
	tests := []struct {
		format string
		want   int
	}{
		{"hello", 0},
		{"%s", 1},
		{"%s %d", 2},
		{"%%", 0},
		{"%%s", 0},
		{"100%% done: %s", 1},
		{"%[1]s", 1},
		{"%-5.2f", 1},
		{"%v %T %x", 3},
		{"%", 0},
	}
	for _, tt := range tests {
		got := countFormatVerbs(tt.format)
		if got != tt.want {
			t.Errorf("countFormatVerbs(%q) = %d, want %d", tt.format, got, tt.want)
		}
	}
}

func TestCheckPrintfFormat_LiteralPercentOK(t *testing.T) {

	src := `package main

import "fmt"

func process(progress string) {
	fmt.Sprintf("100%% done: %s", progress)
}
`
	warnings := checkPrintfFormat("test.go", "", src)
	if len(warnings) != 0 {
		t.Errorf("expected 0 warnings for literal percent, got %d: %v", len(warnings), warnings)
	}
}

func TestPrintfFormat_ReplacementDetected(t *testing.T) {
	oldSrc := "package main\nfunc f(u string) { log.Printf(u) }\n"
	newSrc := "package main\nfunc f(u string) { log.Printf(u); log.Println() }\n"
	_ = oldSrc
	_ = newSrc

	old2 := "package main\nimport \"log\"\nfunc f(u string) { log.Printf(u) }\n"
	new2 := "package main\nimport \"log\"\nfunc f(u string) { log.Print(u) }\nfunc g(v string) { log.Printf(v) }\n"
	w := checkPrintfFormat("a.go", old2, new2)
	if len(w) == 0 {
		t.Fatal("fix-one-add-another printf bug must be detected (net-0 delta, #172)")
	}
}
