package tui

import (
	"fmt"
	"strings"

	"github.com/topcheer/ggcode/internal/permission"
)

// showPermissionsInfo prints the current human-in-the-loop permission
// posture to the chat: the active mode, the persistent command-level
// allow/deny patterns ("always allow" choices, persisted across restarts
// via rules_persist), and the session-learned approval memory.
//
// Motivation (2025-2026 HITL governance guidance — bounded autonomy needs
// visible operational limits): learned auto-approval is only acceptable
// when it is visible. Before this command both rule sets were write-only
// from the user's perspective — "always allow" choices and the approval
// learning loop silently changed what the agent executes without asking,
// with no way to inspect the accumulated state.
func (m *Model) showPermissionsInfo() {
	if m.policy == nil {
		m.chatWriteSystem(nextSystemID(), "Permission policy not initialized.")
		return
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Permission mode: %s", m.policy.Mode())

	if cp, ok := m.policy.(*permission.ConfigPolicy); ok {
		if rs := cp.CommandRuleSet(); rs != nil {
			if allow := rs.AllowPatterns(); len(allow) > 0 {
				b.WriteString("\n\nAlways allow (persistent):")
				for _, p := range allow {
					fmt.Fprintf(&b, "\n  + %s", p)
				}
			}
			if deny := rs.DenyPatterns(); len(deny) > 0 {
				b.WriteString("\n\nAlways deny (persistent):")
				for _, p := range deny {
					fmt.Fprintf(&b, "\n  - %s", p)
				}
			}
		}
	}

	if m.agent != nil {
		rules := m.agent.LearnedApprovalRules()
		if len(rules) == 0 {
			b.WriteString("\n\nLearned this session: nothing yet (approving the same pattern repeatedly teaches auto-approval).")
		} else {
			b.WriteString("\n\nLearned this session (approval memory):")
			for _, r := range rules {
				if r.AutoApproved {
					fmt.Fprintf(&b, "\n  * %s — auto-approves without asking", r.Key)
				} else {
					fmt.Fprintf(&b, "\n  · %s — %d consecutive approval(s) toward auto-approval", r.Key, r.Consecutive)
				}
			}
		}
	}

	b.WriteString("\n")
	m.chatWriteSystem(nextSystemID(), b.String())
}
