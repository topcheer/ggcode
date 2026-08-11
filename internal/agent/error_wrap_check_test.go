package agent

import (
	"strings"
	"testing"
)

func TestCheckErrorWrapping_PercentVInsteadOfW(t *testing.T) {
	old := `package main

import "fmt"

func process() error {
	err := doSomething()
	if err != nil {
		return fmt.Errorf("failed: %s", err)
	}
	return nil
}
`
	new := `package main

import "fmt"

func process() error {
	err := doSomething()
	if err != nil {
		return fmt.Errorf("failed: %v", err)
	}
	return nil
}
`
	warnings := checkErrorWrapping("main.go", old, new)
	if len(warnings) == 0 {
		t.Fatal("expected warning for percent-v in Errorf, got none")
	}
	if !strings.Contains(warnings[0], "%w") {
		t.Errorf("warning should mention wrapping verb, got: %s", warnings[0])
	}
}

func TestCheckErrorWrapping_PercentWCorrect(t *testing.T) {
	old := `package main

import "fmt"

func process() error {
	err := doSomething()
	if err != nil {
		return fmt.Errorf("failed: %s", err)
	}
	return nil
}
`
	new := `package main

import "fmt"

func process() error {
	err := doSomething()
	if err != nil {
		return fmt.Errorf("failed: %w", err)
	}
	return nil
}
`
	warnings := checkErrorWrapping("main.go", old, new)
	if len(warnings) != 0 {
		t.Errorf("correct wrapping usage should not warn, got: %v", warnings)
	}
}

func TestCheckErrorWrapping_ErrorsNewWithErrError(t *testing.T) {
	old := `package main

import "errors"

func process() error {
	return errors.New("something")
}
`
	new := `package main

import "errors"

func process() error {
	err := doSomething()
	return errors.New(err.Error())
}
`
	warnings := checkErrorWrapping("main.go", old, new)
	if len(warnings) == 0 {
		t.Fatal("expected warning for errors.New(err.Error()), got none")
	}
	if !strings.Contains(warnings[0], "errors.New") {
		t.Errorf("warning should mention errors.New, got: %s", warnings[0])
	}
}

func TestCheckErrorWrapping_StringConcatWithErrError(t *testing.T) {
	old := `package main

import "fmt"

func process() error {
	err := doSomething()
	return fmt.Errorf("failed: %w", err)
}
`
	new := `package main

import "fmt"

func process() error {
	err := doSomething()
	return fmt.Errorf("failed: " + err.Error())
}
`
	warnings := checkErrorWrapping("main.go", old, new)
	if len(warnings) == 0 {
		t.Fatal("expected warning for string concat with err.Error(), got none")
	}
}

func TestCheckErrorWrapping_NoNewIssues(t *testing.T) {
	// Pre-existing %v should not be flagged (delta-aware).
	src := `package main

import "fmt"

func process() error {
	err := doSomething()
	if err != nil {
		return fmt.Errorf("failed: %v", err)
	}
	return nil
}
`
	warnings := checkErrorWrapping("main.go", src, src)
	if len(warnings) != 0 {
		t.Errorf("pre-existing issues should not be flagged, got: %v", warnings)
	}
}

func TestCheckErrorWrapping_NonGoFile(t *testing.T) {
	warnings := checkErrorWrapping("main.py", "", "return errors.New(err.Error())")
	if len(warnings) != 0 {
		t.Errorf("non-Go files should not be checked, got: %v", warnings)
	}
}

func TestCheckErrorWrapping_TestFileSkipped(t *testing.T) {
	new := `package main

import "fmt"

func TestProcess(t *testing.T) {
	err := doSomething()
	_ = fmt.Errorf("failed: %v", err)
}
`
	warnings := checkErrorWrapping("main_test.go", "", new)
	if len(warnings) != 0 {
		t.Errorf("test files should be skipped, got: %v", warnings)
	}
}

func TestCheckErrorWrapping_PercentVWithNonErrorArg(t *testing.T) {
	// %v with a non-error arg should not trigger.
	new := `package main

import "fmt"

func process() error {
	val := 42
	return fmt.Errorf("count: %v", val)
}
`
	warnings := checkErrorWrapping("main.go", "", new)
	if len(warnings) != 0 {
		t.Errorf("%%v with non-error arg should not warn, got: %v", warnings)
	}
}

func TestExtractFormatVerbs(t *testing.T) {
	tests := []struct {
		format string
		want   []string
	}{
		{"hello %s world", []string{"%s"}},
		{"%d items, %s name", []string{"%d", "%s"}},
		{"no verbs here", nil},
		{"100%% done", nil}, // %% is literal
		{"%v and %w", []string{"%v", "%w"}},
		{"%[1]d indexed", []string{"%d"}}, // indexed verbs extract base
		{"%-5s padded", []string{"%s"}},
		{"%.2f precise", []string{"%f"}},
	}

	for _, tt := range tests {
		got := extractFormatVerbs(tt.format)
		if len(got) != len(tt.want) {
			t.Errorf("extractFormatVerbs(%q) = %v, want %v", tt.format, got, tt.want)
			continue
		}
		for i, v := range got {
			if v != tt.want[i] {
				t.Errorf("extractFormatVerbs(%q)[%d] = %s, want %s", tt.format, i, v, tt.want[i])
			}
		}
	}
}

func TestLooksLikeErrorArg(t *testing.T) {
	// Handled via AST, so we test indirectly through findErrorWrapIssues.
	src := `package main

import "fmt"

func a() error {
	err := foo()
	return fmt.Errorf("a: %v", err)
}

func b() error {
	customErr := bar()
	return fmt.Errorf("b: %v", customErr)
}
`
	issues := findErrorWrapIssues(src)
	if len(issues) != 2 {
		t.Errorf("expected 2 wrapping issues (err and customErr), got %d", len(issues))
	}
}
