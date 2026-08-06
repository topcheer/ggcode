package agent

import (
	"strings"
	"testing"
)

func TestCheckErrorNoPropagate_LogWithoutReturn(t *testing.T) {
	src := `package main

import "log"

func processData() error {
	data, err := fetch()
	if err != nil {
		log.Printf("fetch failed: %v", err)
	}
	_ = data
	return nil
}

func fetch() ([]byte, error) { return nil, nil }
`
	warnings := checkErrorNoPropagate("test.go", "", src)
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning, got %d: %v", len(warnings), warnings)
	}
	if !strings.Contains(warnings[0], "Error not propagated") {
		t.Errorf("unexpected warning: %s", warnings[0])
	}
	if !strings.Contains(warnings[0], "err") {
		t.Errorf("warning should mention errName: %s", warnings[0])
	}
}

func TestCheckErrorNoPropagate_ReturnPresent(t *testing.T) {
	src := `package main

import "log"

func processData() error {
	data, err := fetch()
	if err != nil {
		log.Printf("fetch failed: %v", err)
		return err
	}
	_ = data
	return nil
}

func fetch() ([]byte, error) { return nil, nil }
`
	warnings := checkErrorNoPropagate("test.go", "", src)
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings (has return), got %d: %v", len(warnings), warnings)
	}
}

func TestCheckErrorNoPropagate_BareReturn(t *testing.T) {
	// Bare return is already caught by error_swallow_check; we should not
	// double-flag it.
	src := `package main

func processData() error {
	_, err := fetch()
	if err != nil {
		return
	}
	return nil
}

func fetch() ([]byte, error) { return nil, nil }
`
	warnings := checkErrorNoPropagate("test.go", "", src)
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings (bare return), got %d: %v", len(warnings), warnings)
	}
}

func TestCheckErrorNoPropagate_EmptyBody(t *testing.T) {
	// Empty body is already caught by error_swallow_check.
	src := `package main

func processData() error {
	_, err := fetch()
	if err != nil {
	}
	return nil
}

func fetch() ([]byte, error) { return nil, nil }
`
	warnings := checkErrorNoPropagate("test.go", "", src)
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings (empty body), got %d: %v", len(warnings), warnings)
	}
}

func TestCheckErrorNoPropagate_FatalOK(t *testing.T) {
	src := `package main

import "log"

func processData() error {
	_, err := fetch()
	if err != nil {
		log.Fatalf("fatal: %v", err)
	}
	return nil
}

func fetch() ([]byte, error) { return nil, nil }
`
	warnings := checkErrorNoPropagate("test.go", "", src)
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings (log.Fatal), got %d: %v", len(warnings), warnings)
	}
}

func TestCheckErrorNoPropagate_PanicOK(t *testing.T) {
	src := `package main

func processData() error {
	_, err := fetch()
	if err != nil {
		panic(err)
	}
	return nil
}

func fetch() ([]byte, error) { return nil, nil }
`
	warnings := checkErrorNoPropagate("test.go", "", src)
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings (panic), got %d: %v", len(warnings), warnings)
	}
}

func TestCheckErrorNoPropagate_ContinueOK(t *testing.T) {
	src := `package main

import "log"

func processAll() error {
	items := []int{1, 2, 3}
	for _, item := range items {
		err := process(item)
		if err != nil {
			log.Printf("skip %d: %v", item, err)
			continue
		}
	}
	return nil
}

func process(int) error { return nil }
`
	warnings := checkErrorNoPropagate("test.go", "", src)
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings (continue), got %d: %v", len(warnings), warnings)
	}
}

func TestCheckErrorNoPropagate_BreakOK(t *testing.T) {
	src := `package main

import "log"

func processAll() error {
	items := []int{1, 2, 3}
	for _, item := range items {
		err := process(item)
		if err != nil {
			log.Printf("stop at %d: %v", item, err)
			break
		}
	}
	return nil
}

func process(int) error { return nil }
`
	warnings := checkErrorNoPropagate("test.go", "", src)
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings (break), got %d: %v", len(warnings), warnings)
	}
}

func TestCheckErrorNoPropagate_NonErrorFunc(t *testing.T) {
	// In functions that don't return error, logging without return is OK.
	src := `package main

import "log"

func processData() {
	err := sideEffect()
	if err != nil {
		log.Printf("error: %v", err)
	}
}

func sideEffect() error { return nil }
`
	warnings := checkErrorNoPropagate("test.go", "", src)
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings (non-error func), got %d: %v", len(warnings), warnings)
	}
}

func TestCheckErrorNoPropagate_DeltaAware(t *testing.T) {
	// Pre-existing pattern in oldContent should not be flagged.
	src := `package main

import "log"

func processData() error {
	_, err := fetch()
	if err != nil {
		log.Printf("fetch failed: %v", err)
	}
	return nil
}

func fetch() ([]byte, error) { return nil, nil }
`
	warnings := checkErrorNoPropagate("test.go", src, src)
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings (delta), got %d: %v", len(warnings), warnings)
	}
}

func TestCheckErrorNoPropagate_TestingFatal(t *testing.T) {
	src := `package main

import "testing"

func TestSomething(t *testing.T) {
	err := doSomething()
	if err != nil {
		t.Fatalf("failed: %v", err)
	}
}

func doSomething() error { return nil }
`
	warnings := checkErrorNoPropagate("test.go", "", src)
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings (t.Fatal), got %d: %v", len(warnings), warnings)
	}
}

func TestCheckErrorNoPropagate_NilCheckOnNil(t *testing.T) {
	// Should only match err != nil, not nil == err or other conditions.
	src := `package main

import "log"

func processData() error {
	err := doSomething()
	if err == nil {
		log.Printf("unexpected success")
	}
	return nil
}

func doSomething() error { return nil }
`
	warnings := checkErrorNoPropagate("test.go", "", src)
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings (err == nil), got %d: %v", len(warnings), warnings)
	}
}

func TestCheckErrorNoPropagate_OsExit(t *testing.T) {
	src := `package main

import (
	"log"
	"os"
)

func processData() error {
	_, err := fetch()
	if err != nil {
		log.Printf("fatal: %v", err)
		os.Exit(1)
	}
	return nil
}

func fetch() ([]byte, error) { return nil, nil }
`
	warnings := checkErrorNoPropagate("test.go", "", src)
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings (os.Exit), got %d: %v", len(warnings), warnings)
	}
}

func TestCheckErrorNoPropagate_MultipleIssues(t *testing.T) {
	src := `package main

import "log"

func processA() error {
	_, err := fetch()
	if err != nil {
		log.Printf("err: %v", err)
	}
	return nil
}

func processB() error {
	_, err2 := fetch()
	if err2 != nil {
		log.Printf("err: %v", err2)
	}
	return nil
}

func fetch() ([]byte, error) { return nil, nil }
`
	warnings := checkErrorNoPropagate("test.go", "", src)
	if len(warnings) != 2 {
		t.Fatalf("expected 2 warnings, got %d: %v", len(warnings), warnings)
	}
}

func TestCheckErrorNoPropagate_NestedReturn(t *testing.T) {
	// A return inside a nested if within the error handler is valid.
	src := `package main

import "log"

func processData() error {
	data, err := fetch()
	if err != nil {
		if len(data) == 0 {
			log.Printf("empty + error: %v", err)
			return err
		}
	}
	return nil
}

func fetch() ([]byte, error) { return nil, nil }
`
	warnings := checkErrorNoPropagate("test.go", "", src)
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings (nested return), got %d: %v", len(warnings), warnings)
	}
}

func TestCheckErrorNoPropagate_NonGoFile(t *testing.T) {
	warnings := checkErrorNoPropagate("test.py", "", "print('hello')")
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings for .py, got %d", len(warnings))
	}
}

func TestCheckErrorNoPropagate_EmptyContent(t *testing.T) {
	warnings := checkErrorNoPropagate("test.go", "", "")
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings for empty content, got %d", len(warnings))
	}
}
