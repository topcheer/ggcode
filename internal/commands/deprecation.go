package commands

import (
	"fmt"
	"strings"
)

// DeprecationTag returns a compact annotation for skills the author has
// explicitly marked `deprecated: true` in frontmatter. This is declared
// lifecycle state, complementary to knight's inferred staleness heuristics:
// the author knows a workflow was superseded even if it is still in use.
// Returns "" for non-deprecated skills.
func (c *Command) DeprecationTag() string {
	if c == nil || !c.Deprecated {
		return ""
	}
	if successor := strings.TrimSpace(c.ReplacedBy); successor != "" {
		return fmt.Sprintf("(deprecated; successor: %s)", successor)
	}
	return "(deprecated)"
}

// DeprecationAdvisory returns a one-line advisory that is prepended to a
// deprecated skill's content at invocation time, steering the model away from
// building new workflows on it while still honoring the explicit invocation.
// Returns "" for non-deprecated skills.
func (c *Command) DeprecationAdvisory() string {
	if c == nil || !c.Deprecated {
		return ""
	}
	if successor := strings.TrimSpace(c.ReplacedBy); successor != "" {
		return fmt.Sprintf("[deprecated skill] %q is marked deprecated; prefer successor skill %q for new work. This invocation proceeds, but do not build new workflows on this skill.", c.Name, successor)
	}
	return fmt.Sprintf("[deprecated skill] %q is marked deprecated and may be removed; avoid building new workflows on it unless the user explicitly requires it.", c.Name)
}
