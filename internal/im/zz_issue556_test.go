package im

import (
	"os"
	"strings"
	"testing"
)

func TestIssue556BindBeforeStartStillAllowed(t *testing.T) {
	m := NewManager()

	store := NewMemoryBindingStore()
	if err := m.SetBindingStore(store); err != nil {
		t.Fatalf("SetBindingStore: %v", err)
	}

	if err := m.BindAdapterToWorkspace("ghost-adapter", "/tmp/ws-556"); err != nil {
		t.Fatalf("bind-before-start flow broken: %v", err)
	}

	bindings, err := store.ListByWorkspace(normalizeWorkspace("/tmp/ws-556"))
	if err != nil {
		t.Fatalf("ListByWorkspace: %v", err)
	}
	if len(bindings) != 1 || bindings[0].Adapter != "ghost-adapter" {
		t.Fatalf("binding not persisted: %+v", bindings)
	}
}

func TestIssue556BindAdapterContractNote(t *testing.T) {
	src := readFileString556(t, "runtime_bindings.go")
	for _, marker := range []string{
		"#556 contract note",
		"desktop/wailskit/im.go",
		"BindAdapterToWorkspace",
	} {
		if !strings.Contains(src, marker) {
			t.Fatalf("contract note missing marker %q in runtime_bindings.go", marker)
		}
	}
}

func readFileString556(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(name)
	if err != nil {
		t.Skipf("cannot read %s: %v", name, err)
	}
	return string(data)
}
