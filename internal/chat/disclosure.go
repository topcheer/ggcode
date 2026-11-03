package chat

import (
	"fmt"

	"charm.land/lipgloss/v2"
)

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
