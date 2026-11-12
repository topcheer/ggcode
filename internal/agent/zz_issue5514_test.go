package agent

import (
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// TestAnchorViolationAdvisorRemoved (#5514): the anchor-violation (AnchorStore)
// mechanism was removed wholesale after its bare-substring matcher false-fired
// on nearly every run_command target (lexical FP inflated the persisted
// violation counter). Falsify its existence:
//  1. the Agent struct no longer carries the anchorStore field - both wiring
//     points (the tool-loop advisor emission and the system-prompt anchors
//     section) dereferenced that field, so with it gone no tool call can
//     inject the advisor;
//  2. no non-test source file under internal/ mentions the mechanism.
func TestAnchorViolationAdvisorRemoved(t *testing.T) {
	// 1. The wiring field must not exist on the Agent struct.
	typ := reflect.TypeOf(Agent{})
	for i := 0; i < typ.NumField(); i++ {
		if typ.Field(i).Name == "anchorStore" {
			t.Fatal("Agent.anchorStore must be removed with the anchor-violation mechanism (#5514)")
		}
	}

	// 2. Source sweep: the mechanism must not resurface in production code.
	root := filepath.Join("..", "..", "internal")
	forbidden := []string{"anchor-violation", "AnchorStore", "anchors.json", "PersonaDrift"}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		data, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		for _, kw := range forbidden {
			if strings.Contains(string(data), kw) {
				t.Errorf("%s still references the removed anchor-violation mechanism (%q)", path, kw)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("source sweep failed: %v", err)
	}
}
