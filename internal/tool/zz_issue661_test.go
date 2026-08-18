package tool

import "testing"

// TestRegistryClonePreservesCodeIndex covers #661: Registry.Clone() must copy
// the codeIndex field so a cloned registry's CodeIndex() does not silently
// return nil (breaking @ fuzzy search with no error).
func TestRegistryClonePreservesCodeIndex(t *testing.T) {
	r := NewRegistry()
	idx := NewCodeIndexManager("")
	r.codeIndex = idx

	clone := r.Clone()
	if got := clone.CodeIndex(); got == nil {
		t.Fatal("cloned registry CodeIndex() returned nil; Clone must copy codeIndex (#661)")
	}
}
