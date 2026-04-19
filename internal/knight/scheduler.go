package knight

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
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
	lastAnalysis   time.Time
	lastValidation time.Time
}

// New creates a new Knight instance.
func New(cfg config.KnightConfig, homeDir, projDir string, store session.Store) *Knight {
	knightDir := filepath.Join(homeDir, ".ggcode")
	return &Knight{
		cfg:      cfg,
		budget:   NewBudget(knightDir, cfg),
		index:    NewSkillIndex(homeDir, projDir),
		promoter: NewPromoter(homeDir, projDir),
		usage:    NewUsageTracker(filepath.Join(projDir, ".ggcode", "skill-usage.json")),
		store:    store,
		homeDir:  homeDir,
		projDir:  projDir,
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

			return k.promoter.Promote(s)
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
			return k.promoter.Reject(s)
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
	if now.Hour() == 2 && now.Sub(k.lastValidation) >= 24*time.Hour {
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
			continue
		}

		if k.cfg.TrustLevel == "auto" {
			debug.Log("knight", "auto-promoting skill %s", s.Name)
			k.promoter.Promote(s)
			k.emitReport(fmt.Sprintf("✅ Skill auto-promoted: %s (%s)", s.Name, s.Meta.Description))
		} else {
			// For "staged" trust level, notify user for review
			k.emitReport(fmt.Sprintf("📝 New skill candidate: %s\n%s\n👉 /knight approve %s to promote / /knight reject %s to decline",
				s.Name, s.Meta.Description, s.Name, s.Name))
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

	// Process candidates: high-score ones get LLM refinement + staging
	var reported []SkillCandidate
	for _, c := range result.SkillCandidates {
		if c.Score >= 1.0 && k.factory != nil {
			// High-value candidate: use LLM to generate a proper skill
			content, genErr := analyzer.GenerateSkillFromAnalysis(ctx, c, k.factory)
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
		report.WriteString(fmt.Sprintf("📊 Analyzed %d sessions, found %d patterns",
			result.SessionsAnalyzed, len(reported)))
		for _, c := range reported {
			report.WriteString(fmt.Sprintf("\n• [%.1f] %s (%s): %s", c.Score, c.Name, c.Scope, c.Description))
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
			count, lastUsed, _ := k.usage.GetUsage(skill.Name)
			debug.Log("knight", "skill %s may be stale: used=%d last=%v", skill.Name, count, lastUsed)
			if count == 0 {
				k.emitReport(fmt.Sprintf("⚠️ Skill '%s' has never been used. Consider removing it.", skill.Name))
			} else {
				k.emitReport(fmt.Sprintf("⚠️ Skill '%s' not used in 30+ days (last: %s). /knight freeze %s to keep or let it expire.", skill.Name, lastUsed.Format("2006-01-02"), skill.Name))
			}
		}

		// Effectiveness check: low average score?
		_, _, avgScore := k.usage.GetUsage(skill.Name)
		if avgScore > 0 && avgScore < 3.0 {
			debug.Log("knight", "skill %s has low effectiveness: %.1f/5", skill.Name, avgScore)
			k.emitReport(fmt.Sprintf("📉 Skill '%s' effectiveness: %.1f/5. Consider updating or removing.", skill.Name, avgScore))
		}
	}
}

// nightlyMaintenance runs deep maintenance tasks.
func (k *Knight) nightlyMaintenance(ctx context.Context) {
	debug.Log("knight", "running nightly maintenance")
	k.emitReport("Knight nightly maintenance started")
	// TODO: clean old snapshots, compress changelog, check test coverage
}

// emitReport sends a Knight report via IM if an emitter is configured.
func (k *Knight) emitReport(report string) {
	k.mu.Lock()
	em := k.emitter
	k.mu.Unlock()
	if em != nil {
		em.EmitKnightReport(report)
	}
}
