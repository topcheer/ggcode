package tool

import (
	"reflect"
	"testing"
)

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

		_ = clone
	}
}

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

		va, vb := reflect.ValueOf(a), reflect.ValueOf(b)
		if va.Type() != vb.Type() {
			t.Fatalf("clone type drift: %v vs %v", va.Type(), vb.Type())
		}
		if va.Kind() == reflect.Ptr && va.Pointer() == vb.Pointer() {
			t.Fatal("Registry.Clone must give LSP tools independent instances (shared pointer)")
		}

	} else {
		t.Fatal("lspPathTool must implement Cloner for Registry.Clone")
	}
}
