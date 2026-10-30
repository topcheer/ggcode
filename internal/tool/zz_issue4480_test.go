//go:build goolm

package tool

import "testing"

// #4480 Bug 2: trimTree's depth>8 branch used to just return, leaving the
// 9th-level node's entire subtree unpruned (only the recursion stopped).
// It must CUT the subtree: keep the depth-9 node, clear its children.
func TestIssue4480_TrimTreePrunesDeepSubtrees(t *testing.T) {
	// Build a 15-level chain, every node labeled (so the label filter
	// keeps them) with one child each.
	var root *uiElement
	cur := &uiElement{Type: "view", Platform: "android", Label: "L0"}
	root = cur
	for i := 1; i < 15; i++ {
		child := &uiElement{Type: "view", Platform: "android", Label: "L1"}
		cur.Children = []*uiElement{child}
		cur = child
	}
	// Deep leaf keeps a labeled child that pre-fix survived unpruned.
	cur.Children = []*uiElement{{Type: "view", Platform: "android", Label: "deep-kept"}}

	trimTree(root, 0)

	// Walk to depth 9: nodes at depth 0..8 keep their single child (the
	// cap applies BELOW them) ...
	node := root
	for i := 0; i < 9; i++ {
		if len(node.Children) != 1 {
			t.Fatalf("chain broken at depth %d (pre-cap nodes must be kept)", i)
		}
		node = node.Children[0]
	}
	// ... but the depth-9 node itself must have its subtree cut.
	if len(node.Children) != 0 {
		t.Fatalf("depth-9 node must have its subtree pruned, got %d children", len(node.Children))
	}
}
