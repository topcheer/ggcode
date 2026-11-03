package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func zz4796Write(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "target.go")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func zz4796Exec(t *testing.T, ef EditFile, path, oldText, newText string) (Result, error) {
	t.Helper()
	b, err := json.Marshal(map[string]interface{}{
		"file_path": path, "old_text": oldText, "new_text": newText, "description": "d",
	})
	if err != nil {
		t.Fatal(err)
	}
	return ef.Execute(context.Background(), b)
}

func TestIssue4796_SwallowedDeclarationWarns(t *testing.T) {
	path := zz4796Write(t, "package p\n\nfunc A() int {\n\treturn 1\n}\n\nfunc B() int {\n\treturn 2\n}\n")
	ef := EditFile{}

	res, err := zz4796Exec(t, ef, path,
		"func A() int {\n\treturn 1\n}\n\nfunc B() int {",
		"func A() int {\n\treturn 11\n}")
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("edit failed: %s", res.Content)
	}
	for _, want := range []string{"#4796", "DELETED 1 declaration-like", "func B() int {"} {
		if !strings.Contains(res.Content, want) {
			t.Errorf("result missing %q:\n%s", want, res.Content)
		}
	}
}

func TestIssue4796_KeptDeclarationNoWarn(t *testing.T) {
	path := zz4796Write(t, "package p\n\nfunc A() int {\n\treturn 1\n}\n\nfunc B() int {\n\treturn 2\n}\n")
	ef := EditFile{}

	res, err := zz4796Exec(t, ef, path,
		"func A() int {\n\treturn 1\n}\n\nfunc B() int {",
		"func A() int {\n\treturn 1\n}\n\nfunc B() int {")
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("edit failed: %s", res.Content)
	}
	if strings.Contains(res.Content, "#4796") {
		t.Errorf("kept declaration must not warn:\n%s", res.Content)
	}
}

func TestIssue4796_PlainEditNoWarn(t *testing.T) {
	path := zz4796Write(t, "package p\n\nfunc A() int {\n\treturn 1\n}\n")
	ef := EditFile{}
	res, err := zz4796Exec(t, ef, path, "return 1", "return 42")
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("edit failed: %s", res.Content)
	}
	if strings.Contains(res.Content, "#4796") {
		t.Errorf("plain in-function edit must not warn:\n%s", res.Content)
	}
}
