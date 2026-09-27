package tui

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/topcheer/ggcode/internal/knight"
	"github.com/topcheer/ggcode/internal/util"
)

// handleKnightCommand dispatches /knight subcommands.
//
// sa-162: the former 450-line monolith (cyclomatic complexity ~110) is
// decomposed into per-subcommand helpers below. Behavior is unchanged -
// user-visible strings, evaluation ordering, and error paths are preserved.
func (m *Model) handleKnightCommand(parts []string) tea.Cmd {
	subcmd := ""
	if len(parts) > 1 {
		subcmd = parts[1]
	}

	// /knight on and /knight off work in all modes (they persist config + toggle runtime).
	switch subcmd {
	case "on":
		return m.knightSetEnabled(true)
	case "off":
		return m.knightSetEnabled(false)
	}

	// All other subcommands require Knight to be running.
	if m.knight == nil {
		m.chatWriteSystem(nextSystemID(), "Knight is not available (only in daemon mode). Use /knight on to enable.")
		return nil
	}

	switch subcmd {
	case "status", "":
		m.openKnightPanel()
	case "budget":
		return m.knightBudgetCmd()
	case "queue":
		return m.knightQueueCmd()
	case "review":
		return m.knightReviewCmd(parts)
	case "run":
		return m.knightRunCmd(parts)
	case "propose":
		return m.knightProposeCmd(parts)
	case "proposals":
		return m.knightProposalsCmd(parts)
	case "policies":
		return m.knightPoliciesCmd()
	case "approve":
		return m.knightApproveSkillCmd(parts)
	case "reject":
		return m.knightRejectSkillCmd(parts)
	case "freeze":
		return m.knightSetSkillFrozenCmd(parts, true)
	case "unfreeze":
		return m.knightSetSkillFrozenCmd(parts, false)
	case "rollback":
		return m.knightRollbackSkillCmd(parts)
	case "skills":
		return m.knightSkillsCmd()
	case "scenarios":
		return m.knightScenariosCmd(parts)
	case "rejects", "reject-history":
		return m.knightRejectsCmd(parts)
	case "memory":
		return m.knightMemoryCmd()
	case "audit":
		return m.knightAuditCmd()
	case "reflect":
		return m.knightReflectCmd()
	case "rate":
		return m.knightRateCmd(parts)
	default:
		m.chatWriteSystem(nextSystemID(), "Knight commands: status, budget, queue, review [name], run <task>, propose <goal>, proposals [id|approve <id>|reject <id>], policies, approve <name>, reject <name>, freeze <name>, unfreeze <name>, rollback <name>, rate <name> <1-5>, skills, scenarios [clear], rejects [clear], memory, audit, reflect")
	}
	return nil
}

// knightSetEnabled persists the knight enabled flag and toggles the runtime
// instance when available. It works in every mode (unlike the other
// subcommands, which require a running knight daemon).
func (m *Model) knightSetEnabled(enable bool) tea.Cmd {
	action := "enable"
	if !enable {
		action = "disable"
	}
	if m.config == nil {
		m.chatWriteSystem(nextSystemID(), "Config not available")
		return nil
	}
	if err := m.config.SaveKnightEnabled(enable); err != nil {
		m.chatWriteSystem(nextSystemID(), fmt.Sprintf("Failed to %s Knight: %v", action, err))
		return nil
	}
	if m.knight != nil {
		if enable {
			if err := m.knight.Enable(context.Background()); err != nil {
				m.chatWriteSystem(nextSystemID(), fmt.Sprintf("Knight config saved, but failed to start: %v", err))
				return nil
			}
			m.chatWriteSystem(nextSystemID(), "Knight enabled and started.")
		} else {
			m.knight.Disable()
			m.chatWriteSystem(nextSystemID(), "Knight disabled and stopped.")
		}
	} else {
		if enable {
			m.chatWriteSystem(nextSystemID(), "Knight enabled. Restart to apply.")
		} else {
			m.chatWriteSystem(nextSystemID(), "Knight disabled. Restart to apply.")
		}
	}
	return nil
}

// knightBudgetCmd prints the current token budget status.
func (m *Model) knightBudgetCmd() tea.Cmd {
	used, remaining, limit := m.knight.BudgetStatus()
	if limit == 0 {
		m.chatWriteSystem(nextSystemID(), fmt.Sprintf("Knight budget: %d tokens used / unlimited", used))
	} else {
		m.chatWriteSystem(nextSystemID(), fmt.Sprintf("Knight budget: %d used / %d remaining / %d total", used, remaining, limit))
	}
	return nil
}

// knightQueueCmd lists deferred knight candidates.
func (m *Model) knightQueueCmd() tea.Cmd {
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
	return nil
}

// knightReviewCmd shows a staging-skills summary, or a detailed review of
// one named skill including validation results and recent auto-promote evals.
func (m *Model) knightReviewCmd(parts []string) tea.Cmd {
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
	return nil
}

// knightRunCmd starts an adhoc knight task on a cancellable context.
func (m *Model) knightRunCmd(parts []string) tea.Cmd {
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
	m.setLoading(true)
	// #1758 case 2: spinner.Start returns the FIRST tick Cmd - dropping
	// it left the animation frozen on frame one until Stop (the elapsed
	// timer kept running). Batch it with the task Cmd.
	tick := m.spinner.Start("Knight task")
	m.statusActivity = "Knight task"
	m.statusToolName = "knight"
	m.statusToolArg = util.Truncate(goal, 80)
	m.statusToolCount = 1
	return tea.Batch(tick, func() tea.Msg {
		// #1364: cancellable at shutdown instead of context.Background.
		taskCtx, handle := m.registerKnightTask()
		defer m.releaseKnightTask(handle)
		result, err := m.knight.RunAdhocTask(taskCtx, goal)
		return knightTaskResultMsg{
			Goal:   goal,
			Result: result,
			Err:    err,
		}
	})
}

// knightProposeCmd drafts a project improvement proposal on a cancellable context.
func (m *Model) knightProposeCmd(parts []string) tea.Cmd {
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
	m.setLoading(true)
	tick := m.spinner.Start("Knight proposal") // #1758 case 2: keep the tick
	m.statusActivity = "Knight proposal"
	m.statusToolName = "knight"
	m.statusToolArg = util.Truncate(goal, 80)
	m.statusToolCount = 1
	return tea.Batch(tick, func() tea.Msg {
		// #1364: cancellable at shutdown instead of context.Background.
		taskCtx, handle := m.registerKnightTask()
		defer m.releaseKnightTask(handle)
		proposal, result, err := m.knight.GenerateProjectImprovementProposal(taskCtx, goal)
		return knightProjectProposalResultMsg{
			Goal:     goal,
			Proposal: proposal,
			Result:   result,
			Err:      err,
		}
	})
}

// knightProposalsCmd lists project improvement proposals, shows one by id,
// or applies an approve/reject action to one by id.
func (m *Model) knightProposalsCmd(parts []string) tea.Cmd {
	if len(parts) >= 4 {
		action := strings.ToLower(parts[2])
		id := parts[3]
		note := ""
		if len(parts) > 4 {
			note = strings.Join(parts[4:], " ")
		}
		switch action {
		case "approve":
			// #1363 note: this path only transitions proposal STATUS - it
			// never calls PromoteStaging (verified in project_proposal.go),
			// so the global-skill --confirm-global gate above (direct
			// /knight approve) does not apply here.
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
	return nil
}

// knightPoliciesCmd lists the automation policies and their guardrails.
func (m *Model) knightPoliciesCmd() tea.Cmd {
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
	return nil
}

// knightApproveSkillCmd promotes a staging skill. Global-scope promotion
// requires an explicit --confirm-global second step (#1363).
func (m *Model) knightApproveSkillCmd(parts []string) tea.Cmd {
	if len(parts) < 3 {
		m.chatWriteSystem(nextSystemID(), "Usage: /knight approve <skill-name>")
		return nil
	}
	name := parts[2]
	// #1363: global-scope promotion needs an explicit second step - a
	// warning that is followed by immediate effect in the same keypress
	// is not a confirmation gate. Global skills inject into the system
	// prompt of EVERY project on this machine.
	if entry, err := m.knight.FindStagingSkill(name); err == nil && entry != nil && entry.Scope == "global" {
		confirmed := false
		for _, p := range parts[3:] {
			if p == "--confirm-global" {
				confirmed = true
			}
		}
		if !confirmed {
			m.chatWriteSystem(nextSystemID(), fmt.Sprintf("⚠️ '%s' is GLOBAL scope — it will affect every project on this machine.", name))
			m.chatWriteSystem(nextSystemID(), "Not promoted. Re-run with --confirm-global to proceed: /knight approve "+name+" --confirm-global")
			return nil
		}
	}
	if err := m.knight.PromoteStaging(name); err != nil {
		m.chatWriteSystem(nextSystemID(), fmt.Sprintf("Error: %v", err))
	} else {
		m.chatWriteSystem(nextSystemID(), fmt.Sprintf("✅ Skill '%s' promoted", name))
	}
	return nil
}

// knightRejectSkillCmd rejects a staging skill.
func (m *Model) knightRejectSkillCmd(parts []string) tea.Cmd {
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
	return nil
}

// knightSetSkillFrozenCmd freezes or unfreezes a skill by name.
func (m *Model) knightSetSkillFrozenCmd(parts []string, frozen bool) tea.Cmd {
	action := "freeze"
	if !frozen {
		action = "unfreeze"
	}
	if len(parts) < 3 {
		m.chatWriteSystem(nextSystemID(), fmt.Sprintf("Usage: /knight %s <skill-name>", action))
		return nil
	}
	name := parts[2]
	if err := m.knight.SetSkillFrozen(name, frozen); err != nil {
		m.chatWriteSystem(nextSystemID(), fmt.Sprintf("Error: %v", err))
	} else if frozen {
		m.chatWriteSystem(nextSystemID(), fmt.Sprintf("🔒 Skill '%s' frozen", name))
	} else {
		m.chatWriteSystem(nextSystemID(), fmt.Sprintf("🔓 Skill '%s' unfrozen", name))
	}
	return nil
}

// knightRollbackSkillCmd rolls a skill back to its previous version.
func (m *Model) knightRollbackSkillCmd(parts []string) tea.Cmd {
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
	return nil
}

// knightSkillsCmd lists active skills with usage and feedback statistics.
func (m *Model) knightSkillsCmd() tea.Cmd {
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
	return nil
}

// knightScenariosCmd lists saved replay scenarios or clears them.
func (m *Model) knightScenariosCmd(parts []string) tea.Cmd {
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
	return nil
}

// knightRejectsCmd lists recent reject/rollback events or clears the log.
func (m *Model) knightRejectsCmd(parts []string) tea.Cmd {
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
	return nil
}

// knightMemoryCmd lists recent semantic memory entries.
func (m *Model) knightMemoryCmd() tea.Cmd {
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
	return nil
}

// knightAuditCmd runs the governance audit over the trailing 30 days.
func (m *Model) knightAuditCmd() tea.Cmd {
	report, err := m.knight.RunGovernanceAudit(30 * 24 * time.Hour)
	if err != nil {
		m.chatWriteSystem(nextSystemID(), fmt.Sprintf("Error: %v", err))
		return nil
	}
	m.chatWriteSystem(nextSystemID(), report.FormatHuman())
	return nil
}

// knightReflectCmd runs self-reflection over the trailing 7 days.
func (m *Model) knightReflectCmd() tea.Cmd {
	report, err := m.knight.RunSelfReflection(context.Background(), 7*24*time.Hour)
	if err != nil {
		m.chatWriteSystem(nextSystemID(), fmt.Sprintf("Error: %v", err))
		return nil
	}
	m.chatWriteSystem(nextSystemID(), report.FormatHuman())
	return nil
}

// knightRateCmd records a 1-5 effectiveness rating for an active skill.
func (m *Model) knightRateCmd(parts []string) tea.Cmd {
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
