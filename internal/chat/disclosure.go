package chat

import (
	"fmt"

	"charm.land/lipgloss/v2"
)

// sa-86: second-level progressive disclosure for tool output.
//
// Frontier context: agent-native UI discussions (HumanLayer agent-native
// UI series; ardalis.com "Optimizing AI Agents with Progressive
// Disclosure", 2025) treat progressive disclosure as the core density
// lever for agent interfaces - level 1 shows a collapsed summary, level
// 2 expands on demand. ggcode already had level 1 (FormatBody caps tool
// bodies at ToolBodyMaxLines and marks "… N more lines"), but there was
// NO second level: a truncated go-test failure body could not be
// expanded anywhere in the UI - the user had to re-run the command or
// dig debug logs.
//
// This file adds the missing second level: BaseToolItem.expanded lifts
// the render caps (chat/tools.go RenderBody) and List.ToggleLastExpandable
// flips the most recent expandable tool item, bound to alt+e in the TUI
// (ctrl+e is reserved by textarea's end-of-line binding).

// ToggleExpanded flips the expanded state and invalidates the render
// cache so Height/Render recompute. Returns the new state.
func (t *BaseToolItem) ToggleExpanded() bool {
	t.expanded = !t.expanded
	t.Invalidate()
	return t.expanded
}

// Expanded reports the current disclosure level (false = collapsed).
func (t *BaseToolItem) Expanded() bool { return t.expanded }

// truncationHint renders the level-1 disclosure hint. The key affordance
// "(alt+e)" degrades to the bare hint, then to an ellipsis, when the
// render width cannot hold it - the width invariant (tool_render_invariant)
// trumps the affordance.
func truncationHint(hidden, width int) string {
	hint := fmt.Sprintf("  … %d more lines (alt+e)", hidden)
	if lipgloss.Width(hint) <= width {
		return hint
	}
	hint = fmt.Sprintf("  … %d more lines", hidden)
	if lipgloss.Width(hint) <= width {
		return hint
	}
	return "  …"
}

// ToggleLastExpandable flips the most recent item that supports
// disclosure expansion (BaseToolItem and descendants). Returns false
// when no expandable item exists (nothing appended yet).
func (l *List) ToggleLastExpandable() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	for i := len(l.items) - 1; i >= 0; i-- {
		if ex, ok := l.items[i].(interface {
			ToggleExpanded() bool
		}); ok {
			ex.ToggleExpanded()
			l.dirty = true
			return true
		}
	}
	return false
}
