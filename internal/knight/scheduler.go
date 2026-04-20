package knight

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/session"
)

// Emitter is the interface Knight uses to send IM notifications.
type Emitter interface {
	EmitKnightReport(report string)
	HasTargets() bool
}

// Knight is the background auto-evolution agent. It runs scheduled tasks
// during idle time, analyzes sessions, creates and validates skills.
type Knight struct {
	cfg      config.KnightConfig
	budget   *Budget
	index    *SkillIndex
	promoter *Promoter
	usage    *UsageTracker
	store    session.Store
	emitter  Emitter
	factory  AgentFactory
	homeDir  string
	projDir  string

	mu       sync.Mutex
	running  bool
	cancel   context.CancelFunc
	lastIdle time.Time

	// Throttle timestamps — prevent running the same task every tick
	lastAnalysis    time.Time
	lastValidation  time.Time
	lastMaintenance time.Time
	notifiedStaging map[string]bool // tracks staging skills already notified to avoid spam
}

// New creates a new Knight instance.
func New(cfg config.KnightConfig, homeDir, projDir string, store session.Store) *Knight {
	knightDir := filepath.Join(homeDir, ".ggcode")
	return &Knight{
		cfg:             cfg,
		budget:          NewBudget(knightDir, cfg),
		index:           NewSkillIndex(homeDir, projDir),
		promoter:        NewPromoter(homeDir, projDir),
		usage:           NewUsageTracker(filepath.Join(projDir, ".ggcode", "skill-usage.json")),
		store:           store,
		homeDir:         homeDir,
		projDir:         projDir,
		notifiedStaging: make(map[string]bool),
	}
}

// Start begins the Knight background loop.
func (k *Knight) Start(ctx context.Context) error {
	if !k.cfg.Enabled {
		debug.Log("knight", "disabled, not starting")
		return nil
	}

	if err := k.budget.EnsureDir(); err != nil {
		return fmt.Errorf("knight: init budget dir: %w", err)
	}
	if err := k.usage.EnsureDir(); err != nil {
		return fmt.Errorf("knight: init usage dir: %w", err)
	}

	// Ensure staging directories exist
	for _, dir := range []string{
		filepath.Join(k.homeDir, ".ggcode", "skills-staging"),
		filepath.Join(k.projDir, ".ggcode", "skills-staging"),
		filepath.Join(k.projDir, ".ggcode", "skills-snapshots"),
	} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("knight: create dir %s: %w", dir, err)
		}
	}

	ctx, cancel := context.WithCancel(ctx)
	k.cancel = cancel
	k.running = true

	go k.runLoop(ctx)
	debug.Log("knight", "started (budget=%dM, trust=%s)", k.cfg.DailyTokenBudget/1_000_000, k.cfg.TrustLevel)
	return nil
}

// Stop gracefully shuts down Knight.
func (k *Knight) Stop() {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.cancel != nil {
		k.cancel()
		k.running = false
		debug.Log("knight", "stopped")
	}
}

// Running returns whether Knight is currently active.
func (k *Knight) Running() bool {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.running
}

// SetEmitter sets the IM emitter for Knight notifications.
func (k *Knight) SetEmitter(e Emitter) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.emitter = e
}

// SetFactory sets the agent factory for LLM-powered tasks.
func (k *Knight) SetFactory(f AgentFactory) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.factory = f
}

// getFactory returns the current agent factory under lock protection.
func (k *Knight) getFactory() AgentFactory {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.factory
}

// RecordSkillUse increments the usage counter for a skill.
// Called from the skill tool when a Knight-managed skill is loaded.
func (k *Knight) RecordSkillUse(name string) {
	if k.usage != nil {
		k.usage.RecordUse(name)
	}
}

// RecordSkillEffectiveness records a 1-5 effectiveness score for a skill.
func (k *Knight) RecordSkillEffectiveness(name string, score int) {
	if k.usage != nil {
		k.usage.RecordEffectiveness(name, score)
	}
}

// SkillUsage returns runtime usage stats for a skill.
func (k *Knight) SkillUsage(name string) (count int, lastUsed time.Time, avgScore float64) {
	if k.usage == nil {
		return 0, time.Time{}, 0
	}
	return k.usage.GetUsage(name)
}

// SkillFeedback returns runtime feedback stats for a skill.
func (k *Knight) SkillFeedback(name string) (avgScore float64, samples int) {
	if k.usage == nil {
		return 0, 0
	}
	return k.usage.GetFeedback(name)
}

// Index returns the skill index for external access.
func (k *Knight) Index() *SkillIndex {
	return k.index
}

// Status returns a human-readable status string.
func (k *Knight) Status() string {
	if !k.cfg.Enabled {
		return "disabled"
	}
	if !k.running {
		return "stopped"
	}
	used := k.budget.Used()
	limit := k.budget.DailyLimit()
	if limit == 0 {
		return fmt.Sprintf("running (tokens: %dK / unlimited)", used/1000)
	}
	return fmt.Sprintf("running (tokens: %dK / %dM)", used/1000, limit/1_000_000)
}

// NotifyActivity is called when the user sends a message, resetting the idle timer.
func (k *Knight) NotifyActivity() {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.lastIdle = time.Now()
}

// CanPerformTask checks if Knight has budget and is allowed to run.
func (k *Knight) CanPerformTask() bool {
	if !k.cfg.Enabled || !k.running {
		return false
	}
	return k.budget.CanSpend()
}

// PerformSkillAnalysis triggers an immediate skill analysis task.
func (k *Knight) PerformSkillAnalysis(ctx context.Context) error {
	if !k.CanPerformTask() {
		return fmt.Errorf("knight: no budget or not running")
	}
	return k.analyzeRecentSessions(ctx)
}

// PerformSkillValidation validates all active skills.
func (k *Knight) PerformSkillValidation(ctx context.Context) ([]ValidationResult, error) {
	if !k.CanPerformTask() {
		return nil, fmt.Errorf("knight: no budget or not running")
	}

	active, err := k.index.ActiveSkills()
	if err != nil {
		return nil, err
	}

	var results []ValidationResult
	for _, skill := range active {
		r := ValidateSkill(skill)
		results = append(results, r)
		debug.Log("knight", "validated skill %s: valid=%v errors=%v warnings=%v",
			skill.Name, r.Valid, r.Errors, r.Warnings)
	}
	return results, nil
}

// RunAdhocTask executes a user-directed Knight task using the configured agent factory.
func (k *Knight) RunAdhocTask(ctx context.Context, goal string) (TaskResult, error) {
	goal = strings.TrimSpace(goal)
	if goal == "" {
		return TaskResult{}, fmt.Errorf("knight: empty task goal")
	}
	factory := k.getFactory()
	if factory == nil {
		return TaskResult{}, fmt.Errorf("knight: task runner unavailable")
	}

	prompt := fmt.Sprintf(`Complete the following user-directed Knight task for the current project.

Task: %s

Requirements:
- Use available tools as needed
- Prefer safe, reversible actions
- If you modify files, validate with the repository's existing checks when practical
- End with a concise summary of what you changed, what remains, and any important risk`, goal)

	result := k.RunTask(ctx, "manual-task", prompt, factory)
	if result.Error != nil {
		return result, result.Error
	}
	summary := formatKnightTaskOutput(result.Output)
	k.emitReport(fmt.Sprintf("🌙 Knight manual task completed: %s\n%s", goal, summary))
	return result, nil
}

// PromoteStaging promotes a staging skill to active after validation.
func (k *Knight) PromoteStaging(skillName string) error {
	staging, err := k.index.StagingSkills()
	if err != nil {
		return err
	}

	for _, s := range staging {
		if s.Name == skillName {
			// Validate first
			result := ValidateSkill(s)
			if !result.Valid {
				return fmt.Errorf("skill %q failed validation: %s", skillName, result.Errors)
			}

			// Check for duplicates
			active, _ := k.index.ActiveSkills()
			if CheckDuplicate(s, active) {
				return fmt.Errorf("skill %q duplicates an existing skill", skillName)
			}

			if err := k.promoter.Promote(s); err != nil {
				return err
			}
			k.index.Invalidate()
			k.clearStagingNotification(s.Name)
			return nil
		}
	}
	return fmt.Errorf("staging skill %q not found", skillName)
}

// RejectStaging removes a staging skill.
func (k *Knight) RejectStaging(skillName string) error {
	staging, err := k.index.StagingSkills()
	if err != nil {
		return err
	}

	for _, s := range staging {
		if s.Name == skillName {
			if err := k.promoter.Reject(s); err != nil {
				return err
			}
			k.index.Invalidate()
			k.clearStagingNotification(s.Name)
			return nil
		}
	}
	return fmt.Errorf("staging skill %q not found", skillName)
}

// runLoop is the main Knight background loop.
func (k *Knight) runLoop(ctx context.Context) {
	// Tick every 5 minutes
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	// Initial delay — wait 1 minute before first task
	select {
	case <-time.After(1 * time.Minute):
	case <-ctx.Done():
		return
	}

	for {
		select {
		case <-ctx.Done():
			return
		case t := <-ticker.C:
			k.tick(ctx, t)
		}
	}
}

// tick runs one cycle of Knight tasks.
func (k *Knight) tick(ctx context.Context, now time.Time) {
	if !k.budget.CanSpend() {
		debug.Log("knight", "daily budget exhausted, skipping tick")
		return
	}

	// Flush usage tracker to disk periodically
	if k.usage != nil {
		k.usage.Flush()
	}

	if k.inQuietHours(now) {
		debug.Log("knight", "within quiet hours, skipping scheduled work")
		return
	}

	// Only run heavy tasks when user is idle
	isIdle := k.isIdle(now)

	// Every tick: check for staging skills that need attention
	k.reviewStagingSkills(ctx)

	// Hourly (throttled): analyze recent sessions for skill candidates
	if isIdle && now.Sub(k.lastAnalysis) >= time.Hour && k.hasCapability("skill_creation") {
		k.lastAnalysis = now
		k.analyzeRecentSessions(ctx)
	}

	// Every 6 hours: validate all skills
	if now.Sub(k.lastValidation) >= 6*time.Hour && k.hasCapability("skill_validation") {
		k.lastValidation = now
		k.validateAllSkills(ctx)
	}

	// Nightly (2 AM): deep maintenance
	if now.Hour() == 2 && now.Sub(k.lastMaintenance) >= 24*time.Hour {
		k.lastMaintenance = now
		k.nightlyMaintenance(ctx)
	}
}

// isIdle returns true if no user activity has happened for the configured idle delay.
func (k *Knight) isIdle(now time.Time) bool {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.lastIdle.IsZero() {
		return true // never set = assume idle
	}
	return now.Sub(k.lastIdle) >= time.Duration(k.cfg.IdleDelaySec)*time.Second
}

func (k *Knight) hasCapability(cap string) bool {
	for _, c := range k.cfg.Capabilities {
		if c == cap {
			return true
		}
	}
	return false
}

// reviewStagingSkills checks staging skills and auto-promotes if trust_level=auto.
func (k *Knight) reviewStagingSkills(ctx context.Context) {
	if strings.EqualFold(k.cfg.TrustLevel, "readonly") {
		return
	}
	staging, err := k.index.StagingSkills()
	if err != nil || len(staging) == 0 {
		return
	}

	active, _ := k.index.ActiveSkills()

	for _, s := range staging {
		result := ValidateSkill(s)
		if !result.Valid {
			debug.Log("knight", "staging skill %s validation failed: %v", s.Name, result.Errors)
			continue
		}
		if CheckDuplicate(s, active) {
			debug.Log("knight", "staging skill %s is duplicate, rejecting", s.Name)
			k.promoter.Reject(s)
			k.index.Invalidate()
			continue
		}

		if k.cfg.TrustLevel == "auto" {
			debug.Log("knight", "auto-promoting skill %s", s.Name)
			k.promoter.Promote(s)
			k.index.Invalidate()
			k.clearStagingNotification(s.Name)
			k.emitReport(fmt.Sprintf("✅ Skill auto-promoted: %s (%s)", s.Name, s.Meta.Description))
		} else {
			// For "staged" trust level, notify user once per skill
			if k.markStagingNotified(s.Name) {
				k.emitReport(fmt.Sprintf("📝 New skill candidate: %s\n%s\n👉 /knight approve %s to promote / /knight reject %s to decline",
					s.Name, s.Meta.Description, s.Name, s.Name))
			}
		}
	}
}

// analyzeRecentSessions scans recent session history for reusable patterns.
// High-score candidates are refined via LLM and written to staging.
func (k *Knight) analyzeRecentSessions(ctx context.Context) error {
	if k.store == nil {
		return nil
	}

	analyzer := NewSessionAnalyzer(k)
	result, err := analyzer.AnalyzeRecent(ctx)
	if err != nil {
		return err
	}

	if result.SessionsAnalyzed == 0 {
		return nil
	}

	active, _ := k.index.ActiveSkills()
	staging, _ := k.index.StagingSkills()

	// Process candidates: only converged candidates become staging writes.
	var reported []SkillCandidate
	for _, c := range result.SkillCandidates {
		if k.isKnownCandidate(c, active, staging) {
			debug.Log("knight", "skip known candidate %s (%s)", c.Name, c.Scope)
			continue
		}
		if k.shouldStageCandidate(c) && k.getFactory() != nil && !strings.EqualFold(k.cfg.TrustLevel, "readonly") {
			// High-value candidate: use LLM to generate a proper skill
			content, genErr := analyzer.GenerateSkillFromAnalysis(ctx, c, k.getFactory())
			if genErr != nil {
				debug.Log("knight", "LLM skill generation failed for %s: %v", c.Name, genErr)
				reported = append(reported, c)
				continue
			}
			path, writeErr := k.promoter.WriteStaging(c.Name, c.Scope, content)
			if writeErr != nil {
				debug.Log("knight", "write staging failed for %s: %v", c.Name, writeErr)
				reported = append(reported, c)
				continue
			}
			c.Reason += fmt.Sprintf(" (refined and staged: %s)", filepath.Base(path))
			reported = append(reported, c)
		} else {
			reported = append(reported, c)
		}
	}

	if len(reported) > 0 {
		var report strings.Builder
		maxShown := len(reported)
		if maxShown > 5 {
			maxShown = 5
		}
		report.WriteString(fmt.Sprintf("📊 Analyzed %d sessions, found %d converged patterns",
			result.SessionsAnalyzed, len(reported)))
		for _, c := range reported[:maxShown] {
			report.WriteString(fmt.Sprintf("\n• [%.1f] %s (%s, evidence=%d): %s", c.Score, c.Name, c.Scope, c.EvidenceCount, c.Description))
		}
		if len(reported) > maxShown {
			report.WriteString(fmt.Sprintf("\n… and %d more", len(reported)-maxShown))
		}
		k.emitReport(report.String())
	}

	return nil
}

// validateAllSkills checks all active skills for validity and freshness.
func (k *Knight) validateAllSkills(ctx context.Context) {
	active, err := k.index.ActiveSkills()
	if err != nil {
		return
	}

	for _, skill := range active {
		// Basic validation
		result := ValidateSkill(skill)
		if !result.Valid {
			debug.Log("knight", "skill %s has issues: %v", skill.Name, result.Errors)
		}

		// Freshness check: is the skill stale?
		if k.usage.IsStale(skill.Name, 30*24*time.Hour) {
			if k.shouldSuppressStaleNotice(skill.Name) {
				continue
			}
			count, lastUsed, _ := k.usage.GetUsage(skill.Name)
			debug.Log("knight", "skill %s may be stale: used=%d last=%v", skill.Name, count, lastUsed)
			if count == 0 {
				k.emitReport(fmt.Sprintf("⚠️ Skill '%s' has never been used. Consider removing it.", skill.Name))
			} else {
				k.emitReport(fmt.Sprintf("⚠️ Skill '%s' not used in 30+ days (last: %s). /knight rate %s 5 if it is still valuable, or let it fade out.", skill.Name, lastUsed.Format("2006-01-02"), skill.Name))
			}
		}

		// Effectiveness check: low average score?
		avgScore, samples, shouldWarn := k.shouldWarnLowEffectiveness(skill.Name)
		if shouldWarn {
			debug.Log("knight", "skill %s has low effectiveness: %.1f/5", skill.Name, avgScore)
			k.emitReport(fmt.Sprintf("📉 Skill '%s' effectiveness: %.1f/5 across %d signals. Consider updating or removing.", skill.Name, avgScore, samples))
		}
	}
}

// nightlyMaintenance runs deep maintenance tasks.
func (k *Knight) nightlyMaintenance(ctx context.Context) {
	debug.Log("knight", "running nightly maintenance")
	factory := k.getFactory()
	if factory == nil {
		k.emitReport("Knight nightly maintenance skipped: no task runner configured")
		return
	}

	var sections []string
	if k.hasCapability("regression_testing") {
		if section := k.runMaintenanceTask(ctx, "nightly-regression-audit", `Inspect the project health in read-only mode.

Prefer the repository's existing verification command if available (for example make verify-ci); otherwise use the most CI-aligned build/test/vet/gofmt checks already present in the repo.

Do not edit files. Produce a concise report with:
1. What command(s) you ran
2. Whether the project is healthy
3. The most important failing area, if any
4. The next concrete remediation step if something is broken`); section != "" {
			sections = append(sections, "🧪 Regression audit\n"+section)
		}
	}
	if k.hasCapability("doc_sync") {
		if section := k.runMaintenanceTask(ctx, "nightly-doc-audit", `Inspect project-facing documentation in read-only mode.

Focus on README, GGCODE.md, COPILOT.md, release/process docs, and obvious command references. Look for mismatches between docs and the current codebase or commands.

Do not edit files. Produce a concise report with:
1. Whether docs look aligned overall
2. The top mismatch, if any
3. The exact document(s) that should be updated next`); section != "" {
			sections = append(sections, "📝 Documentation audit\n"+section)
		}
	}
	if len(sections) == 0 {
		k.emitReport("Knight nightly maintenance had no enabled maintenance tasks")
		return
	}
	k.emitReport("🌙 Knight nightly maintenance\n\n" + strings.Join(sections, "\n\n"))
}

// emitReport sends a Knight report via IM if an emitter is configured.
func (k *Knight) emitReport(report string) {
	if k.inQuietHours(time.Now()) {
		return
	}
	k.mu.Lock()
	em := k.emitter
	k.mu.Unlock()
	if em == nil || !em.HasTargets() {
		return
	}
	em.EmitKnightReport(report)
}

func (k *Knight) markStagingNotified(name string) bool {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.notifiedStaging[name] {
		return false
	}
	k.notifiedStaging[name] = true
	return true
}

func (k *Knight) clearStagingNotification(name string) {
	k.mu.Lock()
	defer k.mu.Unlock()
	delete(k.notifiedStaging, name)
}

func (k *Knight) shouldSuppressStaleNotice(name string) bool {
	avg, samples := k.SkillFeedback(name)
	return samples >= 2 && avg >= 4.0
}

func (k *Knight) shouldWarnLowEffectiveness(name string) (avg float64, samples int, shouldWarn bool) {
	avg, samples = k.SkillFeedback(name)
	return avg, samples, samples >= 2 && avg < 3.0
}

func (k *Knight) runMaintenanceTask(ctx context.Context, taskName, prompt string) string {
	factory := k.getFactory()
	if factory == nil {
		return ""
	}
	result := k.RunTask(ctx, taskName, prompt, factory)
	if result.Error != nil {
		return fmt.Sprintf("task failed: %v", result.Error)
	}
	return formatKnightTaskOutput(result.Output)
}

func formatKnightTaskOutput(output string) string {
	output = strings.TrimSpace(output)
	if output == "" {
		return "task completed without a report"
	}
	if len(output) > 1500 {
		output = strings.TrimSpace(output[:1500]) + "\n…"
	}
	return output
}

func (k *Knight) shouldStageCandidate(c SkillCandidate) bool {
	if c.EvidenceCount >= 2 {
		return true
	}
	return c.Score >= 3.0
}

func (k *Knight) isKnownCandidate(c SkillCandidate, active, staging []*SkillEntry) bool {
	candidate := &SkillEntry{
		Name:  c.Name,
		Meta:  SkillMeta{Name: c.Name, Description: c.Description},
		Scope: c.Scope,
	}
	if CheckDuplicate(candidate, active) {
		return true
	}
	for _, s := range staging {
		if strings.EqualFold(strings.TrimSpace(s.Name), strings.TrimSpace(c.Name)) {
			return true
		}
		if len(c.Description) > 20 && len(s.Meta.Description) > 20 {
			desc := strings.ToLower(c.Description)
			existing := strings.ToLower(s.Meta.Description)
			if strings.Contains(desc, existing) || strings.Contains(existing, desc) {
				return true
			}
		}
	}
	return false
}

func (k *Knight) inQuietHours(now time.Time) bool {
	current := now.Hour()*60 + now.Minute()
	for _, spec := range k.cfg.QuietHours {
		start, end, ok := parseQuietWindow(spec)
		if !ok {
			continue
		}
		if start == end {
			return true
		}
		if start < end {
			if current >= start && current < end {
				return true
			}
			continue
		}
		if current >= start || current < end {
			return true
		}
	}
	return false
}

func parseQuietWindow(spec string) (startMinutes, endMinutes int, ok bool) {
	parts := strings.Split(strings.TrimSpace(spec), "-")
	if len(parts) != 2 {
		return 0, 0, false
	}
	start, okStart := parseClockMinutes(parts[0])
	end, okEnd := parseClockMinutes(parts[1])
	if !okStart || !okEnd {
		return 0, 0, false
	}
	return start, end, true
}

func parseClockMinutes(raw string) (int, bool) {
	parts := strings.Split(strings.TrimSpace(raw), ":")
	if len(parts) != 2 {
		return 0, false
	}
	hour, errHour := strconv.Atoi(parts[0])
	minute, errMinute := strconv.Atoi(parts[1])
	if errHour != nil || errMinute != nil || hour < 0 || hour > 23 || minute < 0 || minute > 59 {
		return 0, false
	}
	return hour*60 + minute, true
}
