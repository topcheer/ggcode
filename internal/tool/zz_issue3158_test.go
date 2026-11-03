package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func v4aUpdateSection3158(path string) v4aSection {
	return v4aSection{
		kind: "update",
		path: path,
		lines: []string{
			" context",
			"-old",
			"+new",
		},
	}
}

func TestIssue3158_DeleteOpOverUpdateSectionRejected(t *testing.T) {
	_, err := selectV4ASection([]v4aSection{v4aUpdateSection3158("a/x.go")}, "delete_file", "a/x.go")
	if err == nil {
		t.Fatal("delete_file over an update section must fail closed (#3158)")
	}
	if !strings.Contains(err.Error(), "contradicts") {
		t.Errorf("error should name the contradiction, got: %v", err)
	}
}

func TestIssue3158_OpKindMatrix(t *testing.T) {
	kinds := map[string]string{
		"create_file": "add",
		"update_file": "update",
		"delete_file": "delete",
	}
	for op, kind := range kinds {
		matching := v4aSection{kind: kind, path: "m/f"}
		if _, err := selectV4ASection([]v4aSection{matching}, op, "m/f"); err != nil {
			t.Errorf("op %s over matching kind %s must select, got: %v", op, kind, err)
		}
		for _, wrong := range []string{"add", "update", "delete"} {
			if wrong == kind {
				continue
			}
			mismatch := v4aSection{kind: wrong, path: "m/f"}
			if _, err := selectV4ASection([]v4aSection{mismatch}, op, "m/f"); err == nil {
				t.Errorf("op %s over mismatched kind %s must be rejected", op, wrong)
			}
		}
	}
}

func TestIssue3158_SingleSectionDefaultPathChecked(t *testing.T) {
	_, err := selectV4ASection([]v4aSection{v4aUpdateSection3158("b/y.go")}, "delete_file", "")
	if err == nil {
		t.Fatal("single-section default path must also enforce op-kind agreement")
	}
}

func TestIssue3158_UnknownOpTypeRejected(t *testing.T) {
	if _, err := selectV4ASection([]v4aSection{v4aUpdateSection3158("c/z.go")}, "rename_file", "c/z.go"); err == nil {
		t.Fatal("unknown opType must be rejected")
	}
}

func TestIssue3158_ExecuteSurfacesContradiction(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "victim.txt")
	if err := os.WriteFile(target, []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tool := ApplyPatch{WorkingDir: dir}
	patch := "*** Begin Patch\n*** Update File: victim.txt\n@@\n-old\n+new\n*** End Patch\n"
	res, err := tool.Execute(context.Background(), json.RawMessage(fmt.Sprintf(`{"type": "delete_file", "path": "victim.txt", "diff": %q}`, patch)))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !res.IsError {
		t.Fatalf("contradictory call must return an error result, got: %s", res.Content)
	}

	b, rerr := os.ReadFile(target)
	if rerr != nil || string(b) != "old\n" {
		t.Fatalf("victim file must be untouched, got %q err=%v", string(b), rerr)
	}
}
