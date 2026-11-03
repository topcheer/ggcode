package chat

import (
	"fmt"

	"charm.land/lipgloss/v2"
)

func (t *BaseToolItem) ToggleExpanded() bool {
	t.expanded = !t.expanded
	t.Invalidate()
	return t.expanded
}

func (t *BaseToolItem) Expanded() bool { return t.expanded }

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
