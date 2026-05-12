package tui

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/diff"
	"github.com/topcheer/ggcode/internal/knight"
	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/provider"
	toolpkg "github.com/topcheer/ggcode/internal/tool"
	"github.com/topcheer/ggcode/internal/util"
)

func (m Model) handleModeSwitch() (tea.Model, tea.Cmd) {
	m.mode = m.mode.Next()
	// Update policy mode
	if cp, ok := m.policy.(*permission.ConfigPolicy); ok {
		cp.SetMode(m.mode)
	}
	m.persistModePreference()
	return m, nil
}

func (m *Model) handleModeCommand(parts []string) tea.Cmd {
	if len(parts) > 1 {
		newMode := permission.ParsePermissionMode(parts[1])
		m.mode = newMode
		if cp, ok := m.policy.(*permission.ConfigPolicy); ok {
			cp.SetMode(newMode)
		}
		m.persistModePreference()
	} else {
		m.chatWriteSystem(nextSystemID(), m.t("mode.current", m.mode))
	}
	return nil
}

func (m *Model) persistModePreference() {
	if m.config == nil {
		return
	}
	if err := m.config.SaveDefaultModePreference(m.mode.String()); err != nil {
		m.chatWriteSystem(nextSystemID(), m.t("mode.persist_failed", err))
	}
}

func (m *Model) handleCompactCommand() tea.Cmd {
	// Enter loading state and start spinner immediately.
	m.loading = true
	m.statusActivity = m.t("status.compacting")

	return tea.Batch(
		m.startLoadingSpinner(m.statusActivity),
		func() tea.Msg {
			// Cancel any background pre-compact so the manual /compact owns the
			// summarize call and we don't double-compact.
			m.agent.CancelPreCompact()
			cm := m.agent.ContextManager()
			if cm == nil {
				return compactResultMsg{err: m.t("compact.unavailable")}
			}
			tokens := cm.TokenCount()
			if err := cm.Summarize(context.Background(), m.agent.Provider()); err != nil {
				return compactResultMsg{err: fmt.Sprintf(m.t("compact.failed"), err)}
			}
			newTokens := cm.TokenCount()
			return compactResultMsg{text: fmt.Sprintf(m.t("compact.done_with_stats"), tokens, newTokens)}
		},
	)
}

func (m *Model) handleUndoCommand() tea.Cmd {
	return func() tea.Msg {
		cpMgr := m.agent.CheckpointManager()
		if cpMgr == nil {
			return streamMsg(m.t("checkpoint.disabled"))
		}
		cp, err := cpMgr.Undo()
		if err != nil {
			return streamMsg(m.t("checkpoint.undo_failed", err))
		}
		// Show diff (new -> old)
		diffText := diff.UnifiedDiff(cp.NewContent, cp.OldContent, 3)
		var b strings.Builder
		b.WriteString(m.t("checkpoint.undid", cp.ToolCall, displayToolFileTarget(cp.FilePath), cp.ID))
		b.WriteString(FormatDiff(diffText))
		b.WriteString("\n")
		return streamMsg(b.String())
	}
}

func (m *Model) handleTodoCommand(parts []string) tea.Cmd {
	if len(parts) > 1 && strings.ToLower(parts[1]) == "clear" {
		// Clear todos
		todoPath := toolpkg.TodoFilePath(workingDirFromModel(m))
		if err := os.Remove(todoPath); err != nil && !os.IsNotExist(err) {
			return func() tea.Msg {
				return streamMsg(m.t("todo.clear_failed", err))
			}
		}
		m.todoSnapshot = nil
		m.activeTodo = nil
		m.chatWriteSystem(nextSystemID(), m.t("todo.cleared"))
		return nil
	}
	m.openInspectorPanel(inspectorPanelTodos)
	return nil
}

func (m *Model) handleConfigCommand(parts []string) tea.Cmd {
	if len(parts) > 1 {
		switch strings.ToLower(parts[1]) {
		case "add-endpoint":
			return m.handleConfigAddEndpoint(parts[2:])
		case "remove-endpoint":
			return m.handleConfigRemoveEndpoint(parts[2:])
		}
	}
	if len(parts) > 1 && strings.ToLower(parts[1]) == "set" {
		if len(parts) < 4 {
			m.chatWriteSystem(nextSystemID(), m.t("config.usage"))
			return nil
		}
		key := parts[2]
		value := parts[3]
		if m.config == nil {
			m.chatWriteSystem(nextSystemID(), m.t("config.not_loaded"))
			return nil
		}
		switch key {
		case "model":
			if err := m.config.SetActiveSelection(m.config.Vendor, m.config.Endpoint, value); err != nil {
				m.chatWriteSystem(nextSystemID(), m.t("command.model_failed", err))
				return nil
			}
			if err := m.reloadActiveProvider(); err != nil {
				m.chatWriteSystem(nextSystemID(), m.t("command.model_failed", err))
				return nil
			}
			m.chatWriteSystem(nextSystemID(), m.t("config.model_set", value))
		case "vendor":
			endpoints := m.config.EndpointNames(value)
			if len(endpoints) == 0 {
				m.chatWriteSystem(nextSystemID(), m.t("command.provider_unknown", value, m.vendorNames()))
				return nil
			}
			if err := m.config.SetActiveSelection(value, endpoints[0], ""); err != nil {
				m.chatWriteSystem(nextSystemID(), m.t("command.provider_failed", err))
				return nil
			}
			if err := m.reloadActiveProvider(); err != nil {
				m.chatWriteSystem(nextSystemID(), m.t("command.provider_failed", err))
				return nil
			}
			m.chatWriteSystem(nextSystemID(), m.t("config.provider_set", value))
		case "endpoint":
			if err := m.config.SetActiveSelection(m.config.Vendor, value, ""); err != nil {
				m.chatWriteSystem(nextSystemID(), m.t("command.provider_failed", err))
				return nil
			}
			if err := m.reloadActiveProvider(); err != nil {
				m.chatWriteSystem(nextSystemID(), m.t("command.provider_failed", err))
				return nil
			}
			m.chatWriteSystem(nextSystemID(), m.t("config.provider_set", value))
		case "language":
			m.applyLanguageChange(normalizeLanguage(value))
		case "apikey":
			vendorScoped := len(parts) > 4 && (parts[4] == "--vendor" || parts[4] == "-v")
			apiKeyValue := value
			if m.config.Vendor == "" {
				m.chatWriteSystem(nextSystemID(), "No active vendor. Use /config set vendor <name> first.")
				return nil
			}
			if err := m.config.SetEndpointAPIKey(m.config.Vendor, m.config.Endpoint, apiKeyValue, vendorScoped); err != nil {
				m.chatWriteSystem(nextSystemID(), fmt.Sprintf("Failed to set API key: %s", err))
				return nil
			}
			if err := m.saveConfig(); err != nil {
				m.chatWriteSystem(nextSystemID(), fmt.Sprintf("Failed to save config: %s", err))
				return nil
			}
			scope := "endpoint " + m.config.Endpoint
			if vendorScoped {
				scope = "vendor " + m.config.Vendor
			}
			masked := "****"
			if len(apiKeyValue) > 8 {
				masked = apiKeyValue[:4] + strings.Repeat("*", len(apiKeyValue)-8) + apiKeyValue[len(apiKeyValue)-4:]
			}
			m.chatWriteSystem(nextSystemID(), fmt.Sprintf("\u2713 API key set for %s: %s", scope, masked))
			if err := m.reloadActiveProvider(); err != nil {
				m.chatWriteSystem(nextSystemID(), fmt.Sprintf("Provider reload: %s", err))
			}
		default:
			m.chatWriteSystem(nextSystemID(), m.t("config.unknown_key", key))
		}
		return nil
	}
	m.openInspectorPanel(inspectorPanelConfig)
	return nil
}

func (m *Model) reloadActiveProvider() error {
	if err := m.saveConfig(); err != nil {
		return err
	}
	if err := m.tryActivateCurrentSelection(); err != nil {
		return err
	}
	m.syncSessionSelection()
	return nil
}

func (m *Model) tryActivateCurrentSelection() error {
	if m.config == nil {
		return fmt.Errorf("config not loaded")
	}
	resolved, err := m.config.ResolveActiveEndpoint()
	if err != nil {
		return err
	}
	if resolved.APIKey == "" {
		if resolved.AuthType == "oauth" {
			return fmt.Errorf("no login configured for vendor %q endpoint %q", resolved.VendorID, resolved.EndpointID)
		}
		return fmt.Errorf("no api key configured for vendor %q endpoint %q", resolved.VendorID, resolved.EndpointID)
	}
	prov, err := provider.NewProvider(resolved)
	if err != nil {
		return err
	}
	if m.agent != nil {
		m.agent.SetProvider(prov)
		if resolved.ContextWindow > 0 {
			m.agent.ContextManager().SetMaxTokens(resolved.ContextWindow)
		}
		if resolved.MaxTokens > 0 {
			m.agent.ContextManager().SetOutputReserve(resolved.MaxTokens)
		}
		// Silently probe actual context window in background
		m.startContextProbe()
	}
	m.setActiveRuntimeSelection(resolved.VendorName, resolved.EndpointName, resolved.Model)
	return nil
}

// ensureProviderSync rebuilds the agent's provider from the current config
// if it's not already in sync. This guarantees that API key changes made in
// the provider panel take effect immediately on the next message, even if
// the user hasn't explicitly activated the vendor/endpoint.
func (m *Model) ensureProviderSync() {
	if m.config == nil || m.agent == nil {
		return
	}
	resolved, err := m.config.ResolveActiveEndpoint()
	if err != nil {
		debug.Log("provider", "ensureProviderSync: resolve failed: %v", err)
		return
	}
	if resolved.APIKey == "" {
		debug.Log("provider", "ensureProviderSync: no API key for %s/%s", resolved.VendorID, resolved.EndpointID)
		return
	}
	prov, err := provider.NewProvider(resolved)
	if err != nil {
		debug.Log("provider", "ensureProviderSync: new provider failed: %v", err)
		return
	}
	m.agent.SetProvider(prov)
	if resolved.ContextWindow > 0 {
		m.agent.ContextManager().SetMaxTokens(resolved.ContextWindow)
	}
	if resolved.MaxTokens > 0 {
		m.agent.ContextManager().SetOutputReserve(resolved.MaxTokens)
	}
	m.setActiveRuntimeSelection(resolved.VendorName, resolved.EndpointName, resolved.Model)
	m.syncSessionSelection()
	// Silently probe actual context window in background
	m.startContextProbe()
}

func (m *Model) syncSessionSelection() {
	if m.session == nil || m.config == nil {
		return
	}
	m.session.Vendor = m.config.Vendor
	m.session.Endpoint = m.config.Endpoint
	m.session.Model = m.config.Model
	if m.sessionStore != nil {
		_ = m.sessionStore.Save(m.session)
	}
}

func (m *Model) handleKnightCommand(parts []string) tea.Cmd {
	if m.knight == nil {
		m.chatWriteSystem(nextSystemID(), "Knight is not available (only in daemon mode)")
		return nil
	}

	subcmd := ""
	if len(parts) > 1 {
		subcmd = parts[1]
	}

	switch subcmd {
	case "status", "":
		m.openKnightPanel()
	case "budget":
		used, remaining, limit := m.knight.BudgetStatus()
		if limit == 0 {
			m.chatWriteSystem(nextSystemID(), fmt.Sprintf("Knight budget: %d tokens used / unlimited", used))
		} else {
			m.chatWriteSystem(nextSystemID(), fmt.Sprintf("Knight budget: %d used / %d remaining / %d total", used, remaining, limit))
		}
	case "queue":
		items, err := m.knight.Queue().List()
		if err != nil {
			m.chatWriteSystem(nextSystemID(), fmt.Sprintf("Error: %v", err))
			return nil
		}
		if len(items) == 0 {
			m.chatWriteSystem(nextSystemID(), "No deferred Knight candidates")
			return nil
		}
		m.chatWriteSystem(nextSystemID(), fmt.Sprintf("Deferred Knight candidates (%d):", len(items)))
		for _, item := range items {
			age := "new"
			if !item.FirstQueuedAt.IsZero() {
				age = fmt.Sprintf("%dd", int(time.Since(item.FirstQueuedAt).Hours()/24))
			}
			m.chatWriteSystem(nextSystemID(), fmt.Sprintf("  • %s:%s [priority=%.1f, touches=%d, age=%s, category=%s, evidence=%d] %s — %s",
				item.Scope, item.Name, item.QueuePriority, item.QueueTouchCount, age, item.Category, item.EvidenceCount, item.Description, util.Truncate(item.QueuePriorityReason, 120)))
		}
	case "review":
		staging, _ := m.knight.Index().StagingSkills()
		if len(staging) == 0 {
			m.chatWriteSystem(nextSystemID(), "No staging skills")
			return nil
		}
		if len(parts) >= 3 {
			name := parts[2]
			s, err := m.knight.FindStagingSkill(name)
			if err == nil {
				result := knight.ValidateSkill(s)
				content, err := os.ReadFile(s.Path)
				if err != nil {
					m.chatWriteSystem(nextSystemID(), fmt.Sprintf("Error: %v", err))
					return nil
				}
				m.chatWriteSystem(nextSystemID(), fmt.Sprintf("Reviewing staging skill '%s' (%s)", s.Name, s.Scope))
				m.chatWriteSystem(nextSystemID(), fmt.Sprintf("Validation: valid=%v warnings=%d errors=%d", result.Valid, len(result.Warnings), len(result.Errors)))
				if len(result.Warnings) > 0 {
					m.chatWriteSystem(nextSystemID(), "Warnings:")
					for _, warning := range result.Warnings {
						m.chatWriteSystem(nextSystemID(), fmt.Sprintf("  - %s", warning))
					}
				}
				if len(result.Errors) > 0 {
					m.chatWriteSystem(nextSystemID(), "Errors:")
					for _, issue := range result.Errors {
						m.chatWriteSystem(nextSystemID(), fmt.Sprintf("  - %s", issue))
					}
				}
				if evals, err := m.knight.RecentAutoPromoteEvalsForSkill(s.Scope, s.Name, 3); err == nil && len(evals) > 0 {
					m.chatWriteSystem(nextSystemID(), "Recent auto-promote evals:")
					for _, eval := range evals {
						m.chatWriteSystem(nextSystemID(), "  • "+formatAutoPromoteEval(eval))
					}
				}
				m.chatWriteSystem(nextSystemID(), strings.TrimSpace(string(content)))
				return nil
			}
			m.chatWriteSystem(nextSystemID(), fmt.Sprintf("Error: %v", err))
			return nil
		}
		m.chatWriteSystem(nextSystemID(), fmt.Sprintf("Staging skills (%d):", len(staging)))
		for _, s := range staging {
			result := knight.ValidateSkill(s)
			status := "valid"
			if !result.Valid {
				status = "invalid"
			}
			m.chatWriteSystem(nextSystemID(), fmt.Sprintf("  • %s (%s): %s [%s, warnings=%d, errors=%d]", s.Name, s.Scope, s.Meta.Description, status, len(result.Warnings), len(result.Errors)))
		}
	case "run":
		if len(parts) < 3 {
			m.chatWriteSystem(nextSystemID(), "Usage: /knight run <task>")
			return nil
		}
		goal := strings.TrimSpace(strings.Join(parts[2:], " "))
		if goal == "" {
			m.chatWriteSystem(nextSystemID(), "Usage: /knight run <task>")
			return nil
		}
		m.chatWriteSystem(nextSystemID(), fmt.Sprintf("🌙 Knight running: %s", goal))
		m.loading = true
		m.spinner.Start("Knight task")
		m.statusActivity = "Knight task"
		m.statusToolName = "knight"
		m.statusToolArg = util.Truncate(goal, 80)
		m.statusToolCount = 1
		return func() tea.Msg {
			result, err := m.knight.RunAdhocTask(context.Background(), goal)
			return knightTaskResultMsg{
				Goal:   goal,
				Result: result,
				Err:    err,
			}
		}
	case "propose":
		if len(parts) < 3 {
			m.chatWriteSystem(nextSystemID(), "Usage: /knight propose <project-improvement-goal>")
			return nil
		}
		goal := strings.TrimSpace(strings.Join(parts[2:], " "))
		if goal == "" {
			m.chatWriteSystem(nextSystemID(), "Usage: /knight propose <project-improvement-goal>")
			return nil
		}
		m.chatWriteSystem(nextSystemID(), fmt.Sprintf("📝 Knight drafting project proposal: %s", goal))
		m.loading = true
		m.spinner.Start("Knight proposal")
		m.statusActivity = "Knight proposal"
		m.statusToolName = "knight"
		m.statusToolArg = util.Truncate(goal, 80)
		m.statusToolCount = 1
		return func() tea.Msg {
			proposal, result, err := m.knight.GenerateProjectImprovementProposal(context.Background(), goal)
			return knightProjectProposalResultMsg{
				Goal:     goal,
				Proposal: proposal,
				Result:   result,
				Err:      err,
			}
		}
	case "proposals":
		if len(parts) >= 4 {
			action := strings.ToLower(parts[2])
			id := parts[3]
			note := ""
			if len(parts) > 4 {
				note = strings.Join(parts[4:], " ")
			}
			switch action {
			case "approve":
				p, err := m.knight.ApproveProposal(id, note)
				if err != nil {
					m.chatWriteSystem(nextSystemID(), fmt.Sprintf("Error: %v", err))
					return nil
				}
				m.chatWriteSystem(nextSystemID(), fmt.Sprintf("Approved proposal %s: %s", p.ID, p.Title))
				return nil
			case "reject":
				p, err := m.knight.RejectProposal(id, note)
				if err != nil {
					m.chatWriteSystem(nextSystemID(), fmt.Sprintf("Error: %v", err))
					return nil
				}
				m.chatWriteSystem(nextSystemID(), fmt.Sprintf("Rejected proposal %s: %s", p.ID, p.Title))
				return nil
			}
		}
		if len(parts) >= 3 {
			proposal, content, err := m.knight.ReadProjectImprovementProposal(parts[2])
			if err != nil {
				m.chatWriteSystem(nextSystemID(), fmt.Sprintf("Error: %v", err))
				return nil
			}
			m.chatWriteSystem(nextSystemID(), fmt.Sprintf("Project proposal %s: %s [%s]", proposal.ID, proposal.Title, proposal.Status))
			m.chatWriteSystem(nextSystemID(), strings.TrimSpace(content))
			return nil
		}
		proposals, err := m.knight.RecentProjectImprovementProposals(10)
		if err != nil {
			m.chatWriteSystem(nextSystemID(), fmt.Sprintf("Error: %v", err))
			return nil
		}
		if len(proposals) == 0 {
			m.chatWriteSystem(nextSystemID(), "No project improvement proposals")
			return nil
		}
		m.chatWriteSystem(nextSystemID(), fmt.Sprintf("Project improvement proposals (%d):", len(proposals)))
		for _, proposal := range proposals {
			m.chatWriteSystem(nextSystemID(), "  • "+formatProjectProposal(proposal))
		}
	case "policies":
		policies := m.knight.AutoPolicies()
		m.chatWriteSystem(nextSystemID(), fmt.Sprintf("Knight auto policies (%d):", len(policies)))
		for _, policy := range policies {
			eff := "active"
			if !policy.Effective {
				eff = "inactive"
				if policy.Reason != "" {
					eff = "inactive: " + policy.Reason
				}
			}
			m.chatWriteSystem(nextSystemID(), fmt.Sprintf("  • %s [%s] (%s): %s Guardrail: %s", policy.Name, policy.Mode, eff, policy.Description, policy.Guardrail))
		}
	case "approve":
		if len(parts) < 3 {
			m.chatWriteSystem(nextSystemID(), "Usage: /knight approve <skill-name>")
			return nil
		}
		name := parts[2]
		if entry, err := m.knight.FindStagingSkill(name); err == nil && entry != nil && entry.Scope == "global" {
			m.chatWriteSystem(nextSystemID(), fmt.Sprintf("⚠️  '%s' is GLOBAL scope — it will affect every project on this machine.", name))
		}
		if err := m.knight.PromoteStaging(name); err != nil {
			m.chatWriteSystem(nextSystemID(), fmt.Sprintf("Error: %v", err))
		} else {
			m.chatWriteSystem(nextSystemID(), fmt.Sprintf("✅ Skill '%s' promoted", name))
		}
	case "reject":
		if len(parts) < 3 {
			m.chatWriteSystem(nextSystemID(), "Usage: /knight reject <skill-name>")
			return nil
		}
		name := parts[2]
		if err := m.knight.RejectStaging(name); err != nil {
			m.chatWriteSystem(nextSystemID(), fmt.Sprintf("Error: %v", err))
		} else {
			m.chatWriteSystem(nextSystemID(), fmt.Sprintf("❌ Skill '%s' rejected", name))
		}
	case "freeze":
		if len(parts) < 3 {
			m.chatWriteSystem(nextSystemID(), "Usage: /knight freeze <skill-name>")
			return nil
		}
		name := parts[2]
		if err := m.knight.SetSkillFrozen(name, true); err != nil {
			m.chatWriteSystem(nextSystemID(), fmt.Sprintf("Error: %v", err))
		} else {
			m.chatWriteSystem(nextSystemID(), fmt.Sprintf("🔒 Skill '%s' frozen", name))
		}
	case "unfreeze":
		if len(parts) < 3 {
			m.chatWriteSystem(nextSystemID(), "Usage: /knight unfreeze <skill-name>")
			return nil
		}
		name := parts[2]
		if err := m.knight.SetSkillFrozen(name, false); err != nil {
			m.chatWriteSystem(nextSystemID(), fmt.Sprintf("Error: %v", err))
		} else {
			m.chatWriteSystem(nextSystemID(), fmt.Sprintf("🔓 Skill '%s' unfrozen", name))
		}
	case "rollback":
		if len(parts) < 3 {
			m.chatWriteSystem(nextSystemID(), "Usage: /knight rollback <skill-name>")
			return nil
		}
		name := parts[2]
		if err := m.knight.RollbackSkill(name); err != nil {
			m.chatWriteSystem(nextSystemID(), fmt.Sprintf("Error: %v", err))
		} else {
			m.chatWriteSystem(nextSystemID(), fmt.Sprintf("↩️ Skill '%s' rolled back", name))
		}
	case "skills":
		active, _ := m.knight.Index().ActiveSkills()
		if len(active) == 0 {
			m.chatWriteSystem(nextSystemID(), "No active skills")
		} else {
			m.chatWriteSystem(nextSystemID(), fmt.Sprintf("Active skills (%d):", len(active)))
			for _, s := range active {
				status := "✓"
				if s.Meta.Frozen {
					status = "🔒"
				}
				ref := knight.FormatSkillRefForDisplay(s.Scope, s.Name)
				used, _, _ := m.knight.SkillUsage(ref)
				exposed, _ := m.knight.SkillPromptExposure(ref)
				promptOK, promptFail := m.knight.SkillPromptOutcome(ref)
				avg, samples := m.knight.SkillFeedback(ref)
				feedback := "n/a"
				if samples > 0 {
					feedback = fmt.Sprintf("%.1f/5 (%d)", avg, samples)
				}
				m.chatWriteSystem(nextSystemID(), fmt.Sprintf("  %s %s (%s): %s [shown: %d, runs: +%d/-%d, used: %d, feedback: %s]", status, s.Name, s.Scope, s.Meta.Description, exposed, promptOK, promptFail, used, feedback))
			}
		}
	case "scenarios":
		if len(parts) >= 3 && strings.EqualFold(parts[2], "clear") {
			if err := m.knight.ClearSkillScenarios(); err != nil {
				m.chatWriteSystem(nextSystemID(), fmt.Sprintf("Error: %v", err))
				return nil
			}
			m.chatWriteSystem(nextSystemID(), "Cleared saved replay scenarios")
			return nil
		}
		scenarios, err := m.knight.RecentSkillScenarios(10)
		if err != nil {
			m.chatWriteSystem(nextSystemID(), fmt.Sprintf("Error: %v", err))
			return nil
		}
		if len(scenarios) == 0 {
			m.chatWriteSystem(nextSystemID(), "No saved replay scenarios")
			return nil
		}
		m.chatWriteSystem(nextSystemID(), fmt.Sprintf("Recent replay scenarios (%d):", len(scenarios)))
		for _, scenario := range scenarios {
			m.chatWriteSystem(nextSystemID(), "  • "+formatSkillScenario(scenario))
		}
	case "rejects", "reject-history":
		if len(parts) >= 3 && strings.EqualFold(parts[2], "clear") {
			if err := m.knight.ClearRejectFeedback(); err != nil {
				m.chatWriteSystem(nextSystemID(), fmt.Sprintf("Error: %v", err))
				return nil
			}
			m.chatWriteSystem(nextSystemID(), "Cleared reject feedback log")
			return nil
		}
		entries := m.knight.RecentRejectFeedback(20)
		if len(entries) == 0 {
			m.chatWriteSystem(nextSystemID(), "No reject feedback recorded")
			return nil
		}
		m.chatWriteSystem(nextSystemID(), fmt.Sprintf("Recent reject/rollback events (%d):", len(entries)))
		for _, line := range entries {
			m.chatWriteSystem(nextSystemID(), "  • "+line)
		}
	case "memory":
		entries, err := m.knight.RecentSemanticMemory(20)
		if err != nil {
			m.chatWriteSystem(nextSystemID(), fmt.Sprintf("Error: %v", err))
			return nil
		}
		if len(entries) == 0 {
			m.chatWriteSystem(nextSystemID(), "No semantic memory recorded yet")
			return nil
		}
		m.chatWriteSystem(nextSystemID(), fmt.Sprintf("Semantic memory (%d):", len(entries)))
		for _, e := range entries {
			when := ""
			if !e.Time.IsZero() {
				when = e.Time.Format("2006-01-02 15:04") + " "
			}
			m.chatWriteSystem(nextSystemID(), fmt.Sprintf("  • %s[%s] %s", when, e.Kind, e.Summary))
		}
	case "audit":
		report, err := m.knight.RunGovernanceAudit(30 * 24 * time.Hour)
		if err != nil {
			m.chatWriteSystem(nextSystemID(), fmt.Sprintf("Error: %v", err))
			return nil
		}
		m.chatWriteSystem(nextSystemID(), report.FormatHuman())
	case "reflect":
		report, err := m.knight.RunSelfReflection(context.Background(), 7*24*time.Hour)
		if err != nil {
			m.chatWriteSystem(nextSystemID(), fmt.Sprintf("Error: %v", err))
			return nil
		}
		m.chatWriteSystem(nextSystemID(), report.FormatHuman())
	case "rate":
		if len(parts) < 4 {
			m.chatWriteSystem(nextSystemID(), "Usage: /knight rate <skill-name> <1-5>")
			return nil
		}
		name := parts[2]
		score, err := strconv.Atoi(parts[3])
		if err != nil || score < 1 || score > 5 {
			m.chatWriteSystem(nextSystemID(), "Usage: /knight rate <skill-name> <1-5>")
			return nil
		}
		entry, err := m.knight.FindActiveSkill(name)
		if err != nil {
			m.chatWriteSystem(nextSystemID(), fmt.Sprintf("Error: %v", err))
			return nil
		}
		ref := knight.FormatSkillRefForDisplay(entry.Scope, entry.Name)
		m.knight.RecordSkillEffectiveness(ref, score)
		avg, samples := m.knight.SkillFeedback(ref)
		m.chatWriteSystem(nextSystemID(), fmt.Sprintf("⭐ Rated skill '%s' %d/5 (avg: %.1f/5 over %d signals)", name, score, avg, samples))
	default:
		m.chatWriteSystem(nextSystemID(), "Knight commands: status, budget, queue, review [name], run <task>, propose <goal>, proposals [id|approve <id>|reject <id>], policies, approve <name>, reject <name>, freeze <name>, unfreeze <name>, rollback <name>, rate <name> <1-5>, skills, scenarios [clear], rejects [clear], memory, audit, reflect")
	}
	return nil
}

func formatAutoPromoteEval(eval knight.AutoPromoteEvalLogEntry) string {
	decision := "review"
	if eval.Allowed {
		decision = "auto-promote"
	}
	replay := "fail"
	if eval.ReplayPass {
		replay = "pass"
	}
	if eval.SavedReplayRequired {
		savedReplay := eval.SavedReplayStatus
		if savedReplay == "" {
			savedReplay = "missing"
		}
		replay = fmt.Sprintf("%s,saved=%s,fp=%d,fn=%d", replay, savedReplay, eval.FalsePositiveCount, eval.FalseNegativeCount)
	}
	if eval.BaselineReplayRequired {
		baselineReplay := eval.BaselineReplayStatus
		if baselineReplay == "" {
			baselineReplay = "missing"
		}
		replay = fmt.Sprintf("%s,baseline=%s,overlap=%d", replay, baselineReplay, eval.OverlapCount)
	}
	when := ""
	if !eval.Time.IsZero() {
		when = eval.Time.Format("2006-01-02 15:04") + " "
	}
	reason := strings.TrimSpace(eval.Rationale)
	if reason == "" {
		reason = strings.TrimSpace(eval.FailureMode)
	}
	if reason != "" {
		reason = " — " + util.Truncate(reason, 100)
	}
	return fmt.Sprintf("%s%s:%s %s (replay=%s)%s", when, eval.Scope, eval.Skill, decision, replay, reason)
}

func formatSkillScenario(scenario knight.SkillScenarioLogEntry) string {
	outcome := "success"
	if !scenario.Success {
		outcome = "failure"
	}
	when := ""
	if !scenario.Time.IsZero() {
		when = scenario.Time.Format("2006-01-02 15:04") + " "
	}
	refs := strings.Join(scenario.SkillRefs, ", ")
	if refs != "" {
		refs = " refs=" + refs
	}
	task := util.Truncate(strings.ReplaceAll(strings.TrimSpace(scenario.Task), "\n", " "), 120)
	errText := ""
	if scenario.Error != "" {
		errText = " error=" + util.Truncate(strings.TrimSpace(scenario.Error), 80)
	}
	return fmt.Sprintf("%s%s:%s%s%s", when, outcome, task, refs, errText)
}

func formatProjectProposal(proposal knight.ProjectImprovementProposal) string {
	when := ""
	if !proposal.Time.IsZero() {
		when = proposal.Time.Format("2006-01-02 15:04") + " "
	}
	summary := strings.TrimSpace(proposal.Summary)
	if summary == "" {
		summary = strings.TrimSpace(proposal.Goal)
	}
	if summary != "" {
		summary = " — " + util.Truncate(summary, 100)
	}
	status := strings.TrimSpace(proposal.Status)
	if status == "" {
		status = "proposed"
	}
	return fmt.Sprintf("%s%s [%s] %s%s", when, proposal.ID, status, proposal.Title, summary)
}

func (m *Model) handleConfigAddEndpoint(args []string) tea.Cmd {
	if m.config == nil {
		m.chatWriteSystem(nextSystemID(), m.t("config.not_loaded"))
		return nil
	}
	// Usage: /config add-endpoint <name> <base_url> [--protocol openai] [--apikey sk-xxx]
	if len(args) < 2 {
		m.chatWriteSystem(nextSystemID(), "Usage: /config add-endpoint <name> <base_url> [--protocol openai] [--apikey sk-xxx]")
		return nil
	}
	name := args[0]
	baseURL := args[1]
	protocol := "openai"
	apiKey := ""

	for i := 2; i < len(args); i++ {
		switch args[i] {
		case "--protocol", "-p":
			i++
			if i < len(args) {
				protocol = args[i]
			}
		case "--apikey", "-k":
			i++
			if i < len(args) {
				apiKey = args[i]
			}
		}
	}

	vendor := m.config.Vendor
	if vendor == "" {
		m.chatWriteSystem(nextSystemID(), "No active vendor. Use /config set vendor <name> first.")
		return nil
	}

	if err := m.config.AddEndpoint(vendor, name, protocol, baseURL, apiKey); err != nil {
		m.chatWriteSystem(nextSystemID(), fmt.Sprintf("Failed to add endpoint: %s", err))
		return nil
	}
	if err := m.saveConfig(); err != nil {
		m.chatWriteSystem(nextSystemID(), fmt.Sprintf("Failed to save config: %s", err))
		return nil
	}

	msg := fmt.Sprintf("\u2713 Added endpoint %q to vendor %q (protocol=%s, base_url=%s)", name, vendor, protocol, baseURL)
	if apiKey != "" {
		masked := "****"
		if len(apiKey) > 8 {
			masked = apiKey[:4] + strings.Repeat("*", len(apiKey)-8) + apiKey[len(apiKey)-4:]
		}
		msg += fmt.Sprintf(", apikey=%s", masked)
	}
	m.chatWriteSystem(nextSystemID(), msg)
	m.chatWriteSystem(nextSystemID(), fmt.Sprintf("Use /config set endpoint %s to activate it.", name))
	return nil
}

func (m *Model) handleConfigRemoveEndpoint(args []string) tea.Cmd {
	if m.config == nil {
		m.chatWriteSystem(nextSystemID(), m.t("config.not_loaded"))
		return nil
	}
	if len(args) < 1 {
		m.chatWriteSystem(nextSystemID(), "Usage: /config remove-endpoint <name>")
		return nil
	}
	name := args[0]
	vendor := m.config.Vendor
	if vendor == "" {
		m.chatWriteSystem(nextSystemID(), "No active vendor.")
		return nil
	}
	if err := m.config.RemoveEndpoint(vendor, name); err != nil {
		m.chatWriteSystem(nextSystemID(), fmt.Sprintf("Failed to remove endpoint: %s", err))
		return nil
	}
	if err := m.saveConfig(); err != nil {
		m.chatWriteSystem(nextSystemID(), fmt.Sprintf("Failed to save config: %s", err))
		return nil
	}
	m.chatWriteSystem(nextSystemID(), fmt.Sprintf("\u2713 Removed endpoint %q from vendor %q", name, vendor))
	return nil
}
