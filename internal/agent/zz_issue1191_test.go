package agent

import (
	"strings"
	"testing"
)

// Issue #1191: ownership transfer (acquire-and-return) must not be reported
// as a leak, otherwise the agent following the old imperative hint would
// "fix" correct code into a use-after-close.

func TestResourceLeakOwnershipTransferReturn(t *testing.T) {
	src := `package main

import "os"

func openConfig(path string) (*os.File, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	return f, nil // caller owns and closes
}
`
	if w := checkResourceLeaks("test.go", "", src); len(w) != 0 {
		t.Errorf("acquire-and-return idiom must not be flagged as leak, got: %v", w)
	}
}

func TestResourceLeakOwnershipTransferReturnHTTPBody(t *testing.T) {
	src := `package main

import "net/http"

func fetch(url string) (*http.Response, error) {
	resp, err := http.Get(url)
	if err != nil {
		return nil, err
	}
	return resp, nil
}
`
	if w := checkResourceLeaks("test.go", "", src); len(w) != 0 {
		t.Errorf("returning resp transfers body ownership to caller, got: %v", w)
	}
}

func TestResourceLeakOwnershipTransferWrappedReturn(t *testing.T) {
	// Indirect return (struct field) still transfers ownership.
	src := `package main

import "os"

type reader struct {
	f *os.File
}

func openWrapped(path string) (*reader, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	return &reader{f: f}, nil
}
`
	if w := checkResourceLeaks("test.go", "", src); len(w) != 0 {
		t.Errorf("ownership transfer via struct literal in return must be exempt, got: %v", w)
	}
}

func TestResourceLeakOwnershipTransferCallArgument(t *testing.T) {
	// Handing the resource to another function (which closes it) also
	// transfers ownership (#1191).
	src := `package main

import "os"

func consume(f *os.File) error {
	defer f.Close()
	_, err := f.Read(make([]byte, 10))
	return err
}

func process(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	return consume(f)
}
`
	if w := checkResourceLeaks("test.go", "", src); len(w) != 0 {
		t.Errorf("passing resource as call argument must be exempt, got: %v", w)
	}
}

func TestResourceLeakRealLeakStillReportedAndSoftenedWording(t *testing.T) {
	// A resource that is genuinely retained (not returned, not handed off)
	// must still be reported, but the message should ask the agent to verify
	// ownership instead of unconditionally ordering `defer x.Close()`.
	src := `package main

import "os"

func readFile(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	_ = f
	return nil
}
`
	w := checkResourceLeaks("test.go", "", src)
	if len(w) != 1 {
		t.Fatalf("expected exactly 1 leak warning, got %d: %v", len(w), w)
	}
	if !strings.Contains(w[0], "Possible resource leak") {
		t.Errorf("warning should stay recognizable, got: %s", w[0])
	}
	if !strings.Contains(w[0], "Verify ownership") {
		t.Errorf("warning should suggest verifying ownership instead of imperative defer, got: %s", w[0])
	}
}
