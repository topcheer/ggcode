package tool

import (
	"strings"
	"testing"
)

func TestFormatGoBytes_NonGoFile(t *testing.T) {
	data := []byte("hello world")
	out, changed := formatGoBytes("readme.md", data)
	if changed || string(out) != "hello world" {
		t.Fatalf("non-Go file should be unchanged; changed=%v out=%q", changed, out)
	}
}

func TestFormatGoBytes_AlreadyCanonical(t *testing.T) {
	src := []byte("package main\n\nfunc main() {}\n")
	out, changed := formatGoBytes("main.go", src)
	if changed {
		t.Fatalf("canonical source should not be changed; got %q", out)
	}
}

func TestFormatGoBytes_NormalizesIndentation(t *testing.T) {
	// Extra leading indentation on the body — gofmt removes it.
	src := []byte("package main\n\nfunc main() {\n\t\tprintln(\"x\")\n}\n")
	out, changed := formatGoBytes("main.go", src)
	if !changed {
		t.Fatalf("expected formatting to change non-canonical source")
	}
	if strings.Contains(string(out), "\n\t\tprintln") {
		t.Fatalf("expected gofmt to normalize double-indent; got %q", out)
	}
}

func TestFormatGoBytes_InvalidGoUnchanged(t *testing.T) {
	// Not valid Go — must be returned unchanged (no corruption).
	src := []byte("this is not {{{go}}} at all")
	out, changed := formatGoBytes("main.go", src)
	if changed || string(out) != string(src) {
		t.Fatalf("invalid Go should be returned unchanged; changed=%v out=%q", changed, out)
	}
}
