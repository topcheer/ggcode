package agent

import (
	"strings"
	"testing"
)

func TestCheckErrorOrder_NoIssue(t *testing.T) {
	// Proper pattern: error checked before use.
	src := `package main
import "io"
func f() error {
	r, err := getReader()
	if err != nil {
		return err
	}
	defer r.Close()
	return nil
}
func getReader() (io.Reader, error) { return nil, nil }
`
	warnings := checkErrorOrder("test.go", "", src)
	if len(warnings) != 0 {
		t.Errorf("expected no warnings, got %d: %v", len(warnings), warnings)
	}
}

func TestCheckErrorOrder_DeferBeforeCheck(t *testing.T) {
	// Classic bug: defer resp.Body.Close() before if err != nil
	src := `package main
import "net/http"
func f() error {
	resp, err := http.Get("http://example.com")
	defer resp.Body.Close()
	if err != nil {
		return err
	}
	_ = resp
	return nil
}
`
	warnings := checkErrorOrder("test.go", "", src)
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning, got %d: %v", len(warnings), warnings)
	}
	if !strings.Contains(warnings[0], "resp") {
		t.Errorf("warning should mention resp: %s", warnings[0])
	}
	if !strings.Contains(warnings[0], "err") {
		t.Errorf("warning should mention err: %s", warnings[0])
	}
}

func TestCheckErrorOrder_UseBeforeCheck(t *testing.T) {
	// Using result before error check (not defer, direct use).
	src := `package main
func f() error {
	val, err := compute()
	print(val)
	if err != nil {
		return err
	}
	return nil
}
func compute() (int, error) { return 0, nil }
`
	warnings := checkErrorOrder("test.go", "", src)
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning, got %d: %v", len(warnings), warnings)
	}
}

func TestCheckErrorOrder_DeltaAware(t *testing.T) {
	// Old content already has the issue - new content adds nothing new.
	oldSrc := `package main
import "net/http"
func f() error {
	resp, err := http.Get("http://example.com")
	defer resp.Body.Close()
	if err != nil {
		return err
	}
	return nil
}
`
	newSrc := oldSrc // no change
	warnings := checkErrorOrder("test.go", oldSrc, newSrc)
	if len(warnings) != 0 {
		t.Errorf("expected 0 warnings (delta-aware), got %d: %v", len(warnings), warnings)
	}
}

func TestCheckErrorOrder_NewInstance(t *testing.T) {
	// Old content has one instance, new content has two.
	oldSrc := `package main
func a() error {
	v, err := compute()
	print(v)
	if err != nil { return err }
	return nil
}
func compute() (int, error) { return 0, nil }
`
	newSrc := `package main
func a() error {
	v, err := compute()
	print(v)
	if err != nil { return err }
	return nil
}
func b() error {
	w, err := compute()
	print(w)
	if err != nil { return err }
	return nil
}
`
	warnings := checkErrorOrder("test.go", oldSrc, newSrc)
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning (1 new instance), got %d: %v", len(warnings), warnings)
	}
}

func TestCheckErrorOrder_NoErrCheck(t *testing.T) {
	// Result used but error never checked in same block - not flagged
	// (error may be checked elsewhere).
	src := `package main
func f() {
	val, err := compute()
	print(val)
	_ = err
}
func compute() (int, error) { return 0, nil }
`
	warnings := checkErrorOrder("test.go", "", src)
	if len(warnings) != 0 {
		t.Errorf("expected 0 warnings (no err check in block), got %d: %v", len(warnings), warnings)
	}
}

func TestCheckErrorOrder_NonGoFile(t *testing.T) {
	warnings := checkErrorOrder("test.js", "", "var x = 1;")
	if len(warnings) != 0 {
		t.Errorf("expected no warnings for non-Go file, got %d", len(warnings))
	}
}

func TestCheckErrorOrder_NestedBlock(t *testing.T) {
	// Issue inside a nested block should be detected.
	src := `package main
func f() {
	for i := 0; i < 10; i++ {
		val, err := compute()
		print(val)
		if err != nil {
			continue
		}
	}
}
func compute() (int, error) { return 0, nil }
`
	warnings := checkErrorOrder("test.go", "", src)
	if len(warnings) != 1 {
		t.Errorf("expected 1 warning for nested block, got %d: %v", len(warnings), warnings)
	}
}

func TestCheckErrorOrder_BlankResultNotFlagged(t *testing.T) {
	// _ is used for result - only err matters, no use-before-check possible.
	src := `package main
func f() error {
	_, err := compute()
	if err != nil {
		return err
	}
	return nil
}
func compute() (int, error) { return 0, nil }
`
	warnings := checkErrorOrder("test.go", "", src)
	if len(warnings) != 0 {
		t.Errorf("expected 0 warnings for blank result, got %d: %v", len(warnings), warnings)
	}
}

func TestCheckErrorOrder_EmptyContent(t *testing.T) {
	warnings := checkErrorOrder("test.go", "", "")
	if len(warnings) != 0 {
		t.Errorf("expected 0 warnings for empty content, got %d", len(warnings))
	}
}

func TestCheckErrorOrder_SyntaxError(t *testing.T) {
	// Malformed Go source should not cause a panic.
	src := `package main
func f() {
`
	warnings := checkErrorOrder("test.go", "", src)
	if len(warnings) != 0 {
		t.Errorf("expected 0 warnings for broken syntax, got %d", len(warnings))
	}
}
