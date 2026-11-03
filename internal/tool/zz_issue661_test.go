package tool

import "testing"

func TestRegistryClonePreservesCodeIndex(t *testing.T) {
	r := NewRegistry()
	idx := NewCodeIndexManager("")
	r.codeIndex = idx

	clone := r.Clone()
	if got := clone.CodeIndex(); got == nil {
		t.Fatal("cloned registry CodeIndex() returned nil; Clone must copy codeIndex (#661)")
	}
}
