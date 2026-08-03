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

func TestFormatGoBytes_RemovesUnusedImport(t *testing.T) {
	src := []byte("package main\n\nimport (\n\t\"fmt\"\n\t\"os\"\n)\n\nfunc main() {\n\tfmt.Println(\"hello\")\n}\n")
	out, changed := formatGoBytes("main.go", src)
	if !changed {
		t.Fatalf("expected unused import removal to trigger changed=true")
	}
	s := string(out)
	if strings.Contains(s, "\"os\"") {
		t.Fatalf("unused \"os\" import should have been removed; got:\n%s", s)
	}
	if !strings.Contains(s, "\"fmt\"") {
		t.Fatalf("used \"fmt\" import should have been kept; got:\n%s", s)
	}
}

func TestFormatGoBytes_KeepsUsedImports(t *testing.T) {
	src := []byte("package main\n\nimport (\n\t\"fmt\"\n\t\"os\"\n)\n\nfunc main() {\n\tfmt.Println(os.Args)\n}\n")
	out, _ := formatGoBytes("main.go", src)
	// Both imports are used — should not be changed by import removal
	// (but may be changed by formatting if not canonical)
	s := string(out)
	if !strings.Contains(s, "\"fmt\"") {
		t.Fatalf("used \"fmt\" import should be kept; got:\n%s", s)
	}
	if !strings.Contains(s, "\"os\"") {
		t.Fatalf("used \"os\" import should be kept; got:\n%s", s)
	}
}

func TestFormatGoBytes_KeepsBlankAndDotImports(t *testing.T) {
	src := []byte("package main\n\nimport (\n\t_ \"net/http/pprof\"\n\t. \"errors\"\n)\n\nfunc main() {\n\t_ = New(\"x\")\n}\n")
	out, _ := formatGoBytes("main.go", src)
	s := string(out)
	if !strings.Contains(s, "_ \"net/http/pprof\"") {
		t.Fatalf("blank import should be kept; got:\n%s", s)
	}
	if !strings.Contains(s, ". \"errors\"") {
		t.Fatalf("dot import should be kept; got:\n%s", s)
	}
}

func TestFormatGoBytes_RemovesMultipleUnusedImports(t *testing.T) {
	src := []byte(`package main

import (
	"fmt"
	"os"
	"strings"
	"encoding/json"
)

func main() {
	fmt.Println("hello")
}
`)
	out, changed := formatGoBytes("main.go", src)
	if !changed {
		t.Fatalf("expected unused imports to be removed")
	}
	s := string(out)
	if !strings.Contains(s, "\"fmt\"") {
		t.Fatalf("used \"fmt\" import should be kept; got:\n%s", s)
	}
	for _, unused := range []string{"\"os\"", "\"strings\"", "\"encoding/json\""} {
		if strings.Contains(s, unused) {
			t.Fatalf("unused import %s should have been removed; got:\n%s", unused, s)
		}
	}
}

func TestFormatGoBytes_KeepsAliasedImport(t *testing.T) {
	src := []byte("package main\n\nimport (\n\t\"fmt\"\n\tmyio \"io\"\n)\n\nfunc main() {\n\tfmt.Println(myio.EOF)\n}\n")
	out, _ := formatGoBytes("main.go", src)
	s := string(out)
	if !strings.Contains(s, "myio \"io\"") {
		t.Fatalf("used aliased import should be kept; got:\n%s", s)
	}
}

func TestFormatGoBytes_RemovesUnusedAliasedImport(t *testing.T) {
	src := []byte("package main\n\nimport (\n\t\"fmt\"\n\tmyio \"io\"\n)\n\nfunc main() {\n\tfmt.Println(\"hello\")\n}\n")
	out, changed := formatGoBytes("main.go", src)
	if !changed {
		t.Fatalf("expected unused aliased import to be removed")
	}
	s := string(out)
	if strings.Contains(s, "myio") {
		t.Fatalf("unused aliased import should have been removed; got:\n%s", s)
	}
}

func TestFormatGoBytes_KeepsVersionedImport(t *testing.T) {
	// Regression test: imports with /v2, /v3 suffixes (e.g. "charm.land/lipgloss/v2")
	// must NOT be removed as "unused" just because the last path segment ("v2")
	// doesn't appear as an identifier in the code. The actual identifier is the
	// segment before the version ("lipgloss").
	src := []byte(`package main

import (
	"fmt"
	"charm.land/lipgloss/v2"
)

func main() {
	fmt.Println(lipgloss.Width("hello"))
}
`)
	out, changed := formatGoBytes("main.go", src)
	s := string(out)
	if !strings.Contains(s, `"charm.land/lipgloss/v2"`) {
		t.Fatalf("versioned import lipgloss/v2 should be KEPT (it is used via lipgloss.Width); got:\n%s", s)
	}
	// "fmt" should also be kept since it's used.
	if !strings.Contains(s, `"fmt"`) {
		t.Fatalf("used \"fmt\" import should be kept; got:\n%s", s)
	}
	_ = changed
}

func TestFormatGoBytes_PreservesComments(t *testing.T) {
	src := []byte("package main\n\n// important comment\nimport \"fmt\"\n\nfunc main() {\n\tfmt.Println(\"hello\") // inline comment\n}\n")
	out, _ := formatGoBytes("main.go", src)
	s := string(out)
	if !strings.Contains(s, "important comment") {
		t.Fatalf("comment should be preserved; got:\n%s", s)
	}
	if !strings.Contains(s, "inline comment") {
		t.Fatalf("inline comment should be preserved; got:\n%s", s)
	}
}
