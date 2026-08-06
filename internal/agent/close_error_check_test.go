package agent

import (
	"testing"
)

func TestCheckCloseErrorIgnored_DeferFileClose(t *testing.T) {
	src := `package main

import "os"

func process(path string) error {
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()
	return nil
}
`
	warnings := checkCloseErrorIgnored("test.go", "", src)
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning, got %d: %v", len(warnings), warnings)
	}
	if !ceContains(warnings[0], "ignores returned error") {
		t.Errorf("unexpected warning: %s", warnings[0])
	}
}

func TestCheckCloseErrorIgnored_ClosureHandled(t *testing.T) {
	src := `package main

import "os"

func process(path string) error {
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer func() {
		if err := file.Close(); err != nil {
			panic(err)
		}
	}()
	return nil
}
`
	warnings := checkCloseErrorIgnored("test.go", "", src)
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings for closure-wrapped Close, got %d: %v", len(warnings), warnings)
	}
}

func TestCheckCloseErrorIgnored_NonCloseMethod(t *testing.T) {
	src := `package main

func process() {
	defer cleanup()
}
`
	warnings := checkCloseErrorIgnored("test.go", "", src)
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings for non-Close defer, got %d", len(warnings))
	}
}

func TestCheckCloseErrorIgnored_ChainedClose(t *testing.T) {
	src := `package main

import "os"

func process(path string) {
	defer os.Stdout.Close()
}
`
	warnings := checkCloseErrorIgnored("test.go", "", src)
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning for chained .Close(), got %d", len(warnings))
	}
}

func TestCheckCloseErrorIgnored_MultipleCloses(t *testing.T) {
	src := `package main

import "os"

func process(f1, f2 *os.File) {
	defer f1.Close()
	defer f2.Close()
}
`
	warnings := checkCloseErrorIgnored("test.go", "", src)
	if len(warnings) != 2 {
		t.Fatalf("expected 2 warnings, got %d", len(warnings))
	}
}

func TestCheckCloseErrorIgnored_CloseWithArgs(t *testing.T) {
	src := `package main

func process() {
	defer something.Close(context)
}
`
	warnings := checkCloseErrorIgnored("test.go", "", src)
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings for Close with args, got %d", len(warnings))
	}
}

func TestCheckCloseErrorIgnored_NonGoFile(t *testing.T) {
	src := `defer file.Close()`
	warnings := checkCloseErrorIgnored("test.py", "", src)
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings for non-Go file, got %d", len(warnings))
	}
}

func TestCheckCloseErrorIgnored_EmptyContent(t *testing.T) {
	warnings := checkCloseErrorIgnored("test.go", "", "")
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings for empty content, got %d", len(warnings))
	}
}

func TestCheckCloseErrorIgnored_SyntaxError(t *testing.T) {
	src := `package main
func broken( {
`
	warnings := checkCloseErrorIgnored("test.go", "", src)
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings for unparseable code, got %d", len(warnings))
	}
}

func TestCheckCloseErrorIgnored_Truncation(t *testing.T) {
	src := "package main\n\nfunc f() {\n"
	for i := 0; i < 8; i++ {
		src += "\tdefer f" + string(rune('a'+i)) + ".Close()\n"
	}
	src += "}\n"
	warnings := checkCloseErrorIgnored("test.go", "", src)
	if len(warnings) != maxCloseErrWarnings+1 {
		t.Fatalf("expected %d warnings (truncated + notice), got %d", maxCloseErrWarnings+1, len(warnings))
	}
	last := warnings[len(warnings)-1]
	if !contains(last, "more ignored Close()") {
		t.Errorf("expected truncation notice, got: %s", last)
	}
}

func ceContains(s, substr string) bool {
	return len(s) >= len(substr) && ceIndexOf(s, substr) >= 0
}

func ceIndexOf(s, sub string) int {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
