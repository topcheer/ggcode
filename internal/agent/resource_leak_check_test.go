package agent

import (
	"strings"
	"testing"
)

func TestCheckResourceLeaks_NoLeak(t *testing.T) {
	src := `package main

import "os"

func readFile(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Read(make([]byte, 1024))
	return err
}
`
	warnings := checkResourceLeaks("test.go", "", src)
	if len(warnings) != 0 {
		t.Errorf("expected no warnings, got %d: %v", len(warnings), warnings)
	}
}

func TestCheckResourceLeaks_FileHandleLeak(t *testing.T) {
	src := `package main

import "os"

func readFile(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	_, err = f.Read(make([]byte, 1024))
	return err
}
`
	warnings := checkResourceLeaks("test.go", "", src)
	if len(warnings) == 0 {
		t.Fatal("expected a resource leak warning, got none")
	}
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "file handle") && strings.Contains(w, "Possible resource leak") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("warning did not mention file handle leak: %v", warnings)
	}
}

func TestCheckResourceLeaks_HTTPBodyLeak(t *testing.T) {
	src := `package main

import "net/http"

func fetchURL(url string) error {
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	return nil
}
`
	warnings := checkResourceLeaks("test.go", "", src)
	if len(warnings) == 0 {
		t.Fatal("expected a resource leak warning for HTTP body, got none")
	}
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "HTTP response body") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("warning did not mention HTTP response body: %v", warnings)
	}
}

func TestCheckResourceLeaks_HTTPBodyProperlyClosed(t *testing.T) {
	src := `package main

import (
	"io"
	"net/http"
)

func fetchURL(url string) error {
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, err = io.ReadAll(resp.Body)
	return err
}
`
	warnings := checkResourceLeaks("test.go", "", src)
	if len(warnings) != 0 {
		t.Errorf("expected no warnings when body is closed, got %d: %v", len(warnings), warnings)
	}
}

func TestCheckResourceLeaks_NetListenLeak(t *testing.T) {
	src := `package main

import "net"

func startServer() {
	l, err := net.Listen("tcp", ":8080")
	if err != nil {
		panic(err)
	}
}
`
	warnings := checkResourceLeaks("test.go", "", src)
	if len(warnings) == 0 {
		t.Fatal("expected a resource leak warning for net.Listen, got none")
	}
}

func TestCheckResourceLeaks_NetListenWithDeferClose(t *testing.T) {
	src := `package main

import "net"

func startServer() {
	l, err := net.Listen("tcp", ":8080")
	if err != nil {
		panic(err)
	}
	defer l.Close()
}
`
	warnings := checkResourceLeaks("test.go", "", src)
	if len(warnings) != 0 {
		t.Errorf("expected no warnings when listener is closed, got %d: %v", len(warnings), warnings)
	}
}

func TestCheckResourceLeaks_NonGoFile(t *testing.T) {
	src := `const fs = require('fs');
function read(p) {
	const f = fs.openSync(p);
	return f;
}
`
	warnings := checkResourceLeaks("test.js", "", src)
	if len(warnings) != 0 {
		t.Errorf("expected no warnings for non-Go file, got %d", len(warnings))
	}
}

func TestCheckResourceLeaks_EmptyFile(t *testing.T) {
	warnings := checkResourceLeaks("test.go", "", "")
	if len(warnings) != 0 {
		t.Errorf("expected no warnings for empty file, got %d", len(warnings))
	}
}

func TestCheckResourceLeaks_CreateFile(t *testing.T) {
	src := `package main

import "os"

func writeFile(path string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	_, err = f.Write([]byte("hello"))
	return err
}
`
	warnings := checkResourceLeaks("test.go", "", src)
	if len(warnings) == 0 {
		t.Fatal("expected a resource leak warning for os.Create, got none")
	}
}

func TestCheckResourceLeaks_MultipleLeaks(t *testing.T) {
	src := `package main

import (
	"net/http"
	"os"
)

func process(path, url string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	return nil
}
`
	warnings := checkResourceLeaks("test.go", "", src)
	// We expect at least 2 warnings (both leaks detected).
	// However, maxIntegrityWarnings caps at 3, but checkResourceLeaks
	// itself does not cap -- the capping happens in checkWriteIntegrity.
	if len(warnings) < 2 {
		t.Errorf("expected at least 2 leak warnings, got %d: %v", len(warnings), warnings)
	}
}

func TestCheckResourceLeaks_SyntaxError(t *testing.T) {
	src := `package main

import "os"

func readFile(path string {
	f, err := os.Open(path)
	defer f.Close()
	return err
}
`
	// Syntax errors should cause the check to return no warnings
	// (syntax errors are already caught by the syntax check).
	warnings := checkResourceLeaks("test.go", "", src)
	if len(warnings) != 0 {
		t.Errorf("expected no warnings for file with syntax errors, got %d", len(warnings))
	}
}

func TestCheckResourceLeaks_VariableShadowing(t *testing.T) {
	// When the resource is reassigned, the original should still be tracked
	// as long as it was assigned from a known resource call.
	src := `package main

import "os"

func readFile(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	f2, err := os.Open(path + ".bak")
	if err != nil {
		return err
	}
	defer f.Close()
	defer f2.Close()
	return nil
}
`
	warnings := checkResourceLeaks("test.go", "", src)
	if len(warnings) != 0 {
		t.Errorf("expected no warnings when both files are closed, got %d: %v", len(warnings), warnings)
	}
}

func TestCheckResourceLeaks_ClosedWithoutDefer(t *testing.T) {
	// Closing without defer should still count as having cleanup.
	src := `package main

import "os"

func readFile(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	_, err = f.Read(make([]byte, 10))
	f.Close()
	return err
}
`
	warnings := checkResourceLeaks("test.go", "", src)
	if len(warnings) != 0 {
		t.Errorf("expected no warnings when Close() is called (even without defer), got %d: %v", len(warnings), warnings)
	}
}

// TestResourceLeakDeltaSuppressesPreexisting verifies that a pre-existing
// (already-warned) leak is not re-reported on an unrelated edit, and no
// longer squeezes out the single maxIntegrityWarnings slot (#221).
func TestResourceLeakDeltaSuppressesPreexisting(t *testing.T) {
	old := `package p
import "os"
func load() {
	f, _ := os.Open("x")
	_ = f
}
`
	// Unrelated edit: add a comment line above the leak.
	edited := "// unrelated note\n" + old
	if w := checkResourceLeaks("test.go", old, edited); len(w) != 0 {
		t.Errorf("pre-existing leak re-reported on unrelated edit: %v", w)
	}

	// A newly introduced leak in a different function must still be flagged.
	edited2 := old + `
func load2() {
	g, _ := os.Open("y")
	_ = g
}
`
	w := checkResourceLeaks("test.go", old, edited2)
	if len(w) != 1 {
		t.Fatalf("expected 1 new-leak warning, got %d: %v", len(w), w)
	}
	if !strings.Contains(w[0], "load2") && !strings.Contains(w[0], "g ") {
		t.Errorf("warning should name the new leak, got: %s", w[0])
	}
}
