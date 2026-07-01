package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/topcheer/ggcode/internal/agent"
)

// handleRulesCommand displays learned harness rules (the "ratchet" rules).
func (m *Model) handleRulesCommand() tea.Cmd {
	if m.agent == nil {
		m.chatWriteSystem(nextSystemID(), "Agent not initialized.")
		return nil
	}

	workingDir := m.agent.WorkingDir()
	if workingDir == "" {
		m.chatWriteSystem(nextSystemID(), "Working directory not set.")
		return nil
	}

	rs := agent.NewRuleStore(workingDir)
	if rs == nil {
		m.chatWriteSystem(nextSystemID(), "Cannot create rule store.")
		return nil
	}

	rules := rs.Rules()
	if len(rules) == 0 {
		m.chatWriteSystem(nextSystemID(),
			"No harness rules yet. Rules are automatically extracted from build/test errors "+
				"after each run. They help the agent avoid repeating mistakes.")
		return nil
	}

	var b strings.Builder
	b.WriteString("## Harness Rules (Learned from Past Errors)\n\n")
	b.WriteString(fmt.Sprintf("Total: %d rules (limit: %d)\n\n", len(rules), 60))

	categories := map[string][]agent.Rule{}
	for _, r := range rules {
		categories[r.Category] = append(categories[r.Category], r)
	}

	catOrder := []string{"build", "test", "git", "convention", "security"}
	for _, cat := range catOrder {
		catRules, ok := categories[cat]
		if !ok {
			continue
		}
		b.WriteString(fmt.Sprintf("### %s (%d)\n\n", cat, len(catRules)))
		for _, r := range catRules {
			b.WriteString(fmt.Sprintf("- **%s** (hits: %d)\n", r.Rule, r.HitCount))
			if r.FixHint != "" {
				b.WriteString(fmt.Sprintf("  - Fix: %s\n", r.FixHint))
			}
			b.WriteString(fmt.Sprintf("  - Pattern: `%s`\n\n", r.MatchPattern))
		}
	}

	b.WriteString("---\n")
	b.WriteString(fmt.Sprintf("Storage: %s/.ggcode/agent-rules.json\n", workingDir))

	m.chatWriteSystem(nextSystemID(), b.String())
	return nil
}
