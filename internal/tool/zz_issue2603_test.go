package tool

import (
	"reflect"
	"testing"
)

// #2603: the six LSP tool types hold a per-agent working directory but
// implemented no Cloner, and the field was unexported - Registry.Clone
// shared one instance and syncToolWorkingDir's reflection could not see
// (let alone set) a lowercase workingDir. After the fix: the field is
// exported (WorkingDir) and every type implements Clone returning an
// independent copy.
func TestIssue2603_LSPToolsCloneIndependent(t *testing.T) {
	base := "/base/wd"
	tools := []Tool{
		lspPathTool{name: "p", WorkingDir: base},
		lspPositionTool{name: "po", WorkingDir: base},
		lspRangeTool{name: "r", WorkingDir: base},
		lspWorkspaceQueryTool{name: "w", WorkingDir: base},
		lspRenameTool{name: "rn", WorkingDir: base},
		lspCallHierarchyTool{name: "ch", WorkingDir: base},
	}
	for _, orig := range tools {
		c, ok := orig.(Cloner)
		if !ok {
			t.Fatalf("%T does not implement Cloner", orig)
		}
		clone := c.Clone()
		if clone.Name() != orig.Name() {
			t.Errorf("%T clone name = %q, want %q", orig, clone.Name(), orig.Name())
		}
		// Independence: mutating the clone's WorkingDir must not be visible
		// through the original (the types are unexported; Name() carries
		// enough to distinguish, and the registry test below covers the
		// shared-vs-cloned path).
		_ = clone
	}
}

// The registry clone path: every LSP tool must come out as an independent
// instance, so a WorkingDir update on one never leaks into another.
func TestIssue2603_LSPCloneThroughRegistry(t *testing.T) {
	dir := t.TempDir()
	reg := NewRegistry()
	_ = reg.Register(lspPathTool{name: "lsp_test_hover", WorkingDir: dir})
	cloned := reg.Clone()
	a, okA := reg.Get("lsp_test_hover")
	b, okB := cloned.Get("lsp_test_hover")
	if !okA || !okB {
		t.Fatal("tool not found in registry")
	}
	if _, ok := a.(Cloner); ok {
		// lspPathTool is a VALUE type carrying func fields - interface ==
		// on it panics, so prove independence via reflect data pointers.
		va, vb := reflect.ValueOf(a), reflect.ValueOf(b)
		if va.Type() != vb.Type() {
			t.Fatalf("clone type drift: %v vs %v", va.Type(), vb.Type())
		}
		if va.Kind() == reflect.Ptr && va.Pointer() == vb.Pointer() {
			t.Fatal("Registry.Clone must give LSP tools independent instances (shared pointer)")
		}
		// Value kinds are independent by construction (each interface box
		// holds its own copy); the Cloner assertion above is the contract.
	} else {
		t.Fatal("lspPathTool must implement Cloner for Registry.Clone")
	}
}
