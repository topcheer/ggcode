package agent

import (
	"testing"
)

func TestCheckIgnoredErrorReturn_StandaloneCall(t *testing.T) {
	src := `package main

import (
	"encoding/json"
	"fmt"
	"os"
)

func handler() {
	data := map[string]string{"key": "value"}
	json.Marshal(data)
	fmt.Fprintln(os.Stdout, "hello")
}
`
	warnings := checkIgnoredErrorReturn("test.go", "", src)
	if len(warnings) == 0 {
		t.Fatal("expected warnings for ignored error returns, got none")
	}
}

func TestCheckIgnoredErrorReturn_ExplicitDiscard(t *testing.T) {
	src := `package main

import (
	"encoding/json"
)

func handler() {
	data := map[string]string{"key": "value"}
	_ = json.Marshal(data)
}
`
	warnings := checkIgnoredErrorReturn("test.go", "", src)
	if len(warnings) == 0 {
		t.Fatal("expected warning for explicitly discarded error, got none")
	}
}

func TestCheckIgnoredErrorReturn_CheckedError(t *testing.T) {
	// When the error IS checked, no warning should be emitted.
	src := `package main

import (
	"encoding/json"
)

func handler() error {
	data := map[string]string{"key": "value"}
	_, err := json.Marshal(data)
	if err != nil {
		return err
	}
	return nil
}
`
	warnings := checkIgnoredErrorReturn("test.go", "", src)
	if len(warnings) > 0 {
		t.Fatalf("expected no warnings for properly checked error, got: %v", warnings)
	}
}

func TestCheckIgnoredErrorReturn_NonGoFile(t *testing.T) {
	warnings := checkIgnoredErrorReturn("test.py", "", "print('hello')")
	if len(warnings) > 0 {
		t.Fatalf("expected no warnings for non-Go file, got: %v", warnings)
	}
}

func TestCheckIgnoredErrorReturn_EmptyContent(t *testing.T) {
	warnings := checkIgnoredErrorReturn("test.go", "", "")
	if len(warnings) > 0 {
		t.Fatalf("expected no warnings for empty content, got: %v", warnings)
	}
}

func TestCheckIgnoredErrorReturn_DeltaAware(t *testing.T) {
	// Old content already has 1 ignored error. New content has same.
	old := `package main

import "encoding/json"

func handler() {
	data := map[string]string{"key": "value"}
	json.Marshal(data)
}
`
	// Same number of ignored errors -> no new warnings.
	warnings := checkIgnoredErrorReturn("test.go", old, old)
	if len(warnings) > 0 {
		t.Fatalf("expected no warnings when delta is zero, got: %v", warnings)
	}

	// New content adds another ignored error.
	newSrc := `package main

import (
	"encoding/json"
	"fmt"
	"os"
)

func handler() {
	data := map[string]string{"key": "value"}
	json.Marshal(data)
	fmt.Fprintln(os.Stdout, "hello")
}
`
	warnings = checkIgnoredErrorReturn("test.go", old, newSrc)
	if len(warnings) == 0 {
		t.Fatal("expected warnings for newly introduced ignored errors, got none")
	}
}

func TestCheckIgnoredErrorReturn_MethodChain(t *testing.T) {
	// Method calls on constructor results: json.NewEncoder(w).Encode(data)
	src := `package main

import (
	"encoding/json"
	"os"
)

func handler() {
	data := map[string]string{"key": "value"}
	json.NewEncoder(os.Stdout).Encode(data)
}
`
	warnings := checkIgnoredErrorReturn("test.go", "", src)
	if len(warnings) == 0 {
		t.Fatal("expected warning for ignored Encode method error, got none")
	}
}

func TestCheckIgnoredErrorReturn_NoErrorFuncs(t *testing.T) {
	// Functions that do NOT return errors should not trigger warnings.
	src := `package main

import "fmt"

func handler() {
	fmt.Println("hello")
	fmt.Printf("%d", 42)
}
`
	warnings := checkIgnoredErrorReturn("test.go", "", src)
	if len(warnings) > 0 {
		t.Fatalf("expected no warnings for non-error-returning calls, got: %v", warnings)
	}
}

func TestCheckIgnoredErrorReturn_Integration(t *testing.T) {
	// Full integration via checkWriteIntegrity.
	src := `package main

import "encoding/json"

func handler() {
	data := map[string]string{"key": "value"}
	json.Marshal(data)
}
`
	result := checkWriteIntegrity("main.go", "", src)
	if result == "" {
		t.Fatal("expected write integrity warning for ignored error, got empty result")
	}
}

func TestCheckIgnoredErrorReturn_IgnoredMethodByName(t *testing.T) {
	// Method call on a local variable - should be detected by method name heuristic.
	src := `package main

import "os"

func process() {
	f, _ := os.Create("test.txt")
	f.WriteString("data")
	f.Close()
}
`
	// f.Close() and f.WriteString("data") are standalone calls to known
	// error-returning methods. The receiver is a local variable, so the
	// full type can't be resolved, but the method name heuristic should flag them.
	warnings := checkIgnoredErrorReturn("test.go", "", src)
	if len(warnings) == 0 {
		t.Fatal("expected warnings for ignored Close/WriteString methods, got none")
	}
}
