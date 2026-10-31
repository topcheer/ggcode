package agent

// #3664 probe: changedDirsForLang must keep the module root ("."). A
// root-only change set (npm root index.ts / Python root main.py) used to
// yield zero dirs, degrading the suggested test command to the bare
// full-suite runner - the multi-language twin of the Go-path #3648 defect.

import (
	"reflect"
	"testing"
)

func TestIssue3664_RootFilesKeepRootDir(t *testing.T) {
	if got := changedDirsForLang([]string{"index.ts", "main.py"}, "typescript"); !reflect.DeepEqual(got, []string{"."}) {
		t.Fatalf("root-only TS change set must keep root dir, got %v", got)
	}
	if got := changedDirsForLang([]string{"main.py", "util.py"}, "python"); !reflect.DeepEqual(got, []string{"."}) {
		t.Fatalf("root-only PY change set must keep root dir, got %v", got)
	}
	// Mixed: root + nested both recorded.
	got := changedDirsForLang([]string{"index.ts", "src/a.ts"}, "typescript")
	if !reflect.DeepEqual(got, []string{".", "src"}) {
		t.Fatalf("mixed change set must record root and nested dirs, got %v", got)
	}
	// Nested-only unchanged behavior.
	if got := changedDirsForLang([]string{"src/a.ts", "lib/b.ts"}, "typescript"); !reflect.DeepEqual(got, []string{"lib", "src"}) {
		t.Fatalf("nested-only change set regressed, got %v", got)
	}
}
