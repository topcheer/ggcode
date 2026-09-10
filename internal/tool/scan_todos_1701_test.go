package tool

import (
	"context"
	"strings"
	"testing"
)

// #1701 case 2: an unreadable/nonexistent root must ERROR, not report
// "Codebase is clean." - the #1510 misleading-success class.
func TestScanTodosBadRootErrors1701(t *testing.T) {
	var tool ScanTodosTool
	res, err := tool.Execute(context.Background(), []byte(`{"path": "/nonexistent-path-xyz-1701"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatalf("bad root must be an error, got: %s", res.Content)
	}
}

// #1701 case 4: unknown categories must ERROR (a typo used to filter
// everything and report clean).
func TestScanTodosUnknownCategoryErrors1701(t *testing.T) {
	var tool2 ScanTodosTool
	res, err := tool2.Execute(context.Background(), []byte(`{"path": ".", "categories": "FOO"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError || !strings.Contains(res.Content, "unknown category") {
		t.Fatalf("unknown category must error, got: %s", res.Content)
	}
}
