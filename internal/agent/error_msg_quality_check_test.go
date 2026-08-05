package agent

import (
	"testing"
)

func TestCheckErrorMsgQuality_NonGoFile(t *testing.T) {
	result := checkErrorMsgQuality("foo.py", "", `errors.New("error")`)
	if result != nil {
		t.Fatalf("expected nil for non-Go file, got %v", result)
	}
}

func TestCheckErrorMsgQuality_TestFile(t *testing.T) {
	result := checkErrorMsgQuality("foo_test.go", "", `errors.New("error")`)
	if result != nil {
		t.Fatalf("expected nil for test file, got %v", result)
	}
}

func TestCheckErrorMsgQuality_EmptyContent(t *testing.T) {
	result := checkErrorMsgQuality("foo.go", "", "")
	if result != nil {
		t.Fatalf("expected nil for empty content, got %v", result)
	}
}

func TestCheckErrorMsgQuality_EmptyErrorsNew(t *testing.T) {
	src := `package main
import "errors"
func f() error {
	return errors.New("")
}`
	result := checkErrorMsgQuality("foo.go", "", src)
	if len(result) == 0 {
		t.Fatal("expected warning for empty errors.New message")
	}
	if !contains(result[0], "Empty error message") {
		t.Fatalf("expected 'Empty error message' warning, got: %s", result[0])
	}
}

func TestCheckErrorMsgQuality_EmptyErrorf(t *testing.T) {
	src := `package main
import "fmt"
func f() error {
	return fmt.Errorf("")
}`
	result := checkErrorMsgQuality("foo.go", "", src)
	if len(result) == 0 {
		t.Fatal("expected warning for empty fmt.Errorf message")
	}
	if !contains(result[0], "Empty error message") {
		t.Fatalf("expected 'Empty error message' warning, got: %s", result[0])
	}
}

func TestCheckErrorMsgQuality_GenericErrorsNew(t *testing.T) {
	cases := []string{
		`package main
import "errors"
func f() error { return errors.New("error") }`,
		`package main
import "errors"
func f() error { return errors.New("failed") }`,
		`package main
import "errors"
func f() error { return errors.New("something went wrong") }`,
		`package main
import "errors"
func f() error { return errors.New("unexpected error") }`,
		`package main
import "errors"
func f() error { return errors.New("internal error") }`,
	}
	for i, src := range cases {
		result := checkErrorMsgQuality("foo.go", "", src)
		if len(result) == 0 {
			t.Fatalf("case %d: expected warning for generic error message", i)
		}
		if !contains(result[0], "Generic error message") {
			t.Fatalf("case %d: expected 'Generic error message' warning, got: %s", i, result[0])
		}
	}
}

func TestCheckErrorMsgQuality_GenericErrorf(t *testing.T) {
	src := `package main
import "fmt"
func f() error {
	return fmt.Errorf("error")
}`
	result := checkErrorMsgQuality("foo.go", "", src)
	if len(result) == 0 {
		t.Fatal("expected warning for generic fmt.Errorf message")
	}
	if !contains(result[0], "Generic error message") {
		t.Fatalf("expected 'Generic error message' warning, got: %s", result[0])
	}
}

func TestCheckErrorMsgQuality_ContextFreeWrapping(t *testing.T) {
	src := `package main
import "fmt"
func f(err error) error {
	return fmt.Errorf("%w", err)
}`
	result := checkErrorMsgQuality("foo.go", "", src)
	if len(result) == 0 {
		t.Fatal("expected warning for context-free wrapping")
	}
	if !contains(result[0], "Context-free error wrapping") {
		t.Fatalf("expected 'Context-free error wrapping' warning, got: %s", result[0])
	}
}

func TestCheckErrorMsgQuality_GoodMessage(t *testing.T) {
	src := `package main
import "errors"
import "fmt"
func f(path string) error {
	_ = errors.New("config file not found: " + path)
	_ = fmt.Errorf("failed to parse config %s: %w", path, err)
	return nil
}`
	result := checkErrorMsgQuality("foo.go", "", src)
	if len(result) != 0 {
		t.Fatalf("expected no warnings for good messages, got: %v", result)
	}
}

func TestCheckErrorMsgQuality_GoodWrapping(t *testing.T) {
	src := `package main
import "fmt"
func f(err error) error {
	return fmt.Errorf("parsing config: %w", err)
}`
	result := checkErrorMsgQuality("foo.go", "", src)
	if len(result) != 0 {
		t.Fatalf("expected no warnings for good wrapping, got: %v", result)
	}
}

func TestCheckErrorMsgQuality_DeltaAware(t *testing.T) {
	oldSrc := `package main
import "errors"
func f() error { return errors.New("error") }`
	newSrc := `package main
import "errors"
func f() error { return errors.New("error") }
func g() error { return errors.New("failed") }`
	result := checkErrorMsgQuality("foo.go", oldSrc, newSrc)
	if len(result) == 0 {
		t.Fatal("expected warning for newly introduced generic error")
	}
	if !contains(result[0], "Generic error message") {
		t.Fatalf("expected 'Generic error message' warning, got: %s", result[0])
	}
}

func TestCheckErrorMsgQuality_DeltaNoNewInstances(t *testing.T) {
	oldSrc := `package main
import "errors"
func f() error { return errors.New("error") }`
	newSrc := oldSrc
	result := checkErrorMsgQuality("foo.go", oldSrc, newSrc)
	if len(result) != 0 {
		t.Fatalf("expected no warnings when no new instances, got: %v", result)
	}
}

func TestCheckErrorMsgQuality_NonLiteralMessage(t *testing.T) {
	src := `package main
import "errors"
func f(msg string) error {
	return errors.New(msg)
}`
	result := checkErrorMsgQuality("foo.go", "", src)
	if len(result) != 0 {
		t.Fatalf("expected no warnings for non-literal message, got: %v", result)
	}
}

func TestCheckErrorMsgQuality_MaxWarnings(t *testing.T) {
	src := `package main
import "errors"
func f() error { _ = errors.New("error"); _ = errors.New("failed"); _ = errors.New("oops"); _ = errors.New("bad"); return nil }`
	result := checkErrorMsgQuality("foo.go", "", src)
	if len(result) > 3 {
		t.Fatalf("expected at most 3 warnings, got %d", len(result))
	}
}

func TestCheckErrorMsgQuality_CaseInsensitive(t *testing.T) {
	src := `package main
import "errors"
func f() error { return errors.New("ERROR") }`
	result := checkErrorMsgQuality("foo.go", "", src)
	if len(result) == 0 {
		t.Fatal("expected warning for uppercase generic message")
	}
	if !contains(result[0], "Generic error message") {
		t.Fatalf("expected 'Generic error message' warning, got: %s", result[0])
	}
}

func TestCheckErrorMsgQuality_TrailingPunctuation(t *testing.T) {
	src := `package main
import "errors"
func f() error { return errors.New("failed.") }`
	result := checkErrorMsgQuality("foo.go", "", src)
	if len(result) == 0 {
		t.Fatal("expected warning for generic message with trailing period")
	}
	if !contains(result[0], "Generic error message") {
		t.Fatalf("expected 'Generic error message' warning, got: %s", result[0])
	}
}

func TestCheckErrorMsgQuality_VarErrfNotFlagged(t *testing.T) {
	// fmt.Errorf with format string but no error argument - should still catch generic
	src := `package main
import "fmt"
func f() error { return fmt.Errorf("something went wrong") }`
	result := checkErrorMsgQuality("foo.go", "", src)
	if len(result) == 0 {
		t.Fatal("expected warning for generic Errorf without error arg")
	}
}
