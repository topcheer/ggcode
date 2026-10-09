package agent

// #3611 probe: the freshness snapshot must be taken BEFORE the speculative
// Execute starts (the #1831 "taken at speculation time" contract). The old
// code statted inside store(), i.e. AFTER Execute completed - a mid-flight
// edit_file then blessed stale content with a post-edit mtime the self-heal
// could never catch. This pins the placement against speculate.go itself.

import (
	"os"
	"strings"
	"testing"
)

func TestIssue3611_SnapshotTakenBeforeExecute(t *testing.T) {
	src, err := os.ReadFile("speculate.go")
	if err != nil {
		t.Fatal(err)
	}
	code := string(src)

	// store() must not self-stat anymore.
	storeFn := strings.Index(code, "func (s *speculator) store(")
	if storeFn < 0 {
		t.Fatal("store() not found - probe anchor drifted")
	}
	storeEnd := storeFn + strings.Index(code[storeFn:], "\n}\n")
	if storeBody := code[storeFn:storeEnd]; strings.Contains(storeBody, "statSnapshotFor(") {
		t.Fatal("store() still stats the file itself - snapshot taken after Execute (#3611)")
	}

	// In the speculate goroutine, the stat must precede the Execute call.
	gor := strings.Index(code, "result, err := t.Execute(specCtx, toolArgs)")
	if gor < 0 {
		t.Fatal("speculative Execute call not found - probe anchor drifted")
	}
	statAt := strings.LastIndex(code[:gor], "statSnapshotFor(")
	if statAt < 0 || statAt < storeEnd {
		t.Fatal("statSnapshotFor is not called before Execute in the speculation path (#3611)")
	}
}
