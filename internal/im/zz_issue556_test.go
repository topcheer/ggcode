package im

// Feature tests for GitHub issue #556 (internal/im side).
//   D: BindAdapterToWorkspace contract documentation (ghost-binding guard
//      lives at the desktop entry point; this test pins the supported
//      bind-before-start flow that a runtime-side existence check would
//      have broken).

import (
	"os"
	"strings"
	"testing"
)

// #556 D: binding an adapter that is not yet started/registered must remain
// a SUPPORTED flow at the runtime layer (deferred activation: "bound but not
// yet active; takes effect on next startup"). The ghost-binding validation is
// the desktop entry point's responsibility (desktop/wailskit/im.go).
func TestIssue556BindBeforeStartStillAllowed(t *testing.T) {
	m := NewManager()

	store := NewMemoryBindingStore()
	if err := m.SetBindingStore(store); err != nil {
		t.Fatalf("SetBindingStore: %v", err)
	}

	// "ghost-adapter" is intentionally absent from m.adapters — deferred
	// activation must still be allowed at the runtime layer.
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

// #556 D: the contract note documenting where ghost-binding validation lives
// must stay attached to BindAdapterToWorkspace.
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

// readFileString556 reads a source file from the package directory.
func readFileString556(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(name)
	if err != nil {
		t.Skipf("cannot read %s: %v", name, err)
	}
	return string(data)
}
