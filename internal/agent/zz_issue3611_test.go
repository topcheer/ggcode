package agent

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

	storeFn := strings.Index(code, "func (s *speculator) store(")
	if storeFn < 0 {
		t.Fatal("store() not found - probe anchor drifted")
	}
	storeEnd := storeFn + strings.Index(code[storeFn:], "\n}\n")
	if storeBody := code[storeFn:storeEnd]; strings.Contains(storeBody, "statSnapshotFor(") {
		t.Fatal("store() still stats the file itself - snapshot taken after Execute (#3611)")
	}

	gor := strings.Index(code, "result, err := t.Execute(specCtx, toolArgs)")
	if gor < 0 {
		t.Fatal("speculative Execute call not found - probe anchor drifted")
	}
	statAt := strings.LastIndex(code[:gor], "statSnapshotFor(")
	if statAt < 0 || statAt < storeEnd {
		t.Fatal("statSnapshotFor is not called before Execute in the speculation path (#3611)")
	}
}
