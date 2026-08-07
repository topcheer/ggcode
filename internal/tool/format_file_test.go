package tool

import (
	"bytes"
	"testing"
)

func TestFormatGoBytes_NonGoFile(t *testing.T) {
	data := []byte("hello world")
	out, changed := formatGoBytes("readme.md", data)
	if changed {
		t.Fatalf("non-Go file should not be formatted")
	}
	if !bytes.Equal(out, data) {
		t.Fatalf("non-Go file should be returned unchanged")
	}
}

func TestFormatGoBytes_GoFmt(t *testing.T) {
	// Unformatted Go code.
	data := []byte("package main\n\nfunc main( ){\nfmt.Println(\"hi\")\n}\n")
	out, changed := formatGoBytes("main.go", data)
	if !changed {
		t.Fatalf("expected gofmt to change the content")
	}
	// Should be valid Go after formatting.
	if !bytes.Contains(out, []byte("package main")) {
		t.Fatalf("formatted output should still be valid Go")
	}
}

func TestFormatGoBytes_AlreadyFormatted(t *testing.T) {
	data := []byte("package main\n")
	out, changed := formatGoBytes("main.go", data)
	if changed {
		t.Fatalf("already-formatted code should not be changed")
	}
	if !bytes.Equal(out, data) {
		t.Fatalf("already-formatted code should be returned unchanged")
	}
}

func TestFormatGoBytes_SyntaxError(t *testing.T) {
	data := []byte("not valid go {{{")
	out, changed := formatGoBytes("broken.go", data)
	if changed {
		t.Fatalf("syntax-error code should not be changed")
	}
	if !bytes.Equal(out, data) {
		t.Fatalf("syntax-error code should be returned unchanged")
	}
}
