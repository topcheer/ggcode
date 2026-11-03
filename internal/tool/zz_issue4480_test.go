//go:build goolm

package tool

import "testing"

func TestIssue4480_TrimTreePrunesDeepSubtrees(t *testing.T) {

	var root *uiElement
	cur := &uiElement{Type: "view", Platform: "android", Label: "L0"}
	root = cur
	for i := 1; i < 15; i++ {
		child := &uiElement{Type: "view", Platform: "android", Label: "L1"}
		cur.Children = []*uiElement{child}
		cur = child
	}

	cur.Children = []*uiElement{{Type: "view", Platform: "android", Label: "deep-kept"}}

	trimTree(root, 0)

	node := root
	for i := 0; i < 9; i++ {
		if len(node.Children) != 1 {
			t.Fatalf("chain broken at depth %d (pre-cap nodes must be kept)", i)
		}
		node = node.Children[0]
	}

	if len(node.Children) != 0 {
		t.Fatalf("depth-9 node must have its subtree pruned, got %d children", len(node.Children))
	}
}
