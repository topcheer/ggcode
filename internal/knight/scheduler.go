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
	store    session.Store
	emitter  Emitter
	homeDir  string
	projDir  string

	mu       sync.Mutex
	running  bool
	cancel   context.CancelFunc
	lastIdle time.Time
}

// New creates a new Knight instance.
func New(cfg config.KnightConfig, homeDir, projDir string, store session.Store) *Knight {
	knightDir := filepath.Join(homeDir, ".ggcode")
	return &Knight{
		cfg:      cfg,
		budget:   NewBudget(knightDir, cfg),
		index:    NewSkillIndex(homeDir, projDir),
		promoter: NewPromoter(homeDir, projDir),
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
	k.lastIdle = time.Time{} // reset idle timer
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

	// Rotate tasks based on time of day
	hour := now.Hour()

	// Every tick: check for staging skills that need attention
	k.reviewStagingSkills(ctx)

	// Hourly: analyze recent sessions for skill candidates
	if hour%1 == 0 && k.hasCapability("skill_creation") {
		k.analyzeRecentSessions(ctx)
	}

	// Every 6 hours: validate all skills
	if hour%6 == 0 && k.hasCapability("skill_validation") {
		k.validateAllSkills(ctx)
	}

	// Nightly (2 AM): deep maintenance
	if hour == 2 {
		k.nightlyMaintenance(ctx)
	}
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
			k.emitReport(fmt.Sprintf("✅ Skill 已自动晋升：%s (%s)", s.Name, s.Meta.Description))
		} else {
			// For "staged" trust level, notify user for review
			k.emitReport(fmt.Sprintf("📝 新 Skill 候选：%s\n%s\n👉 /knight approve %s 晋升 / /knight reject %s 拒绝",
				s.Name, s.Meta.Description, s.Name, s.Name))
		}
	}
}

// analyzeRecentSessions scans recent session history for reusable patterns.
func (k *Knight) analyzeRecentSessions(ctx context.Context) error {
	if k.store == nil {
		return nil
	}

	analyzer := NewSessionAnalyzer(k)
	result, err := analyzer.AnalyzeRecent(ctx)
	if err != nil {
		return err
	}

	if result.SessionsAnalyzed > 0 && len(result.SkillCandidates) > 0 {
		var report strings.Builder
		report.WriteString(fmt.Sprintf("📊 分析了 %d 个 session，发现 %d 个可复用模式",
			result.SessionsAnalyzed, len(result.SkillCandidates)))
		for _, c := range result.SkillCandidates {
			report.WriteString(fmt.Sprintf("\n• %s (%s): %s", c.Name, c.Scope, c.Description))
		}
		k.emitReport(report.String())
	}

	return nil
}

// validateAllSkills checks all active skills for validity.
func (k *Knight) validateAllSkills(ctx context.Context) {
	active, err := k.index.ActiveSkills()
	if err != nil {
		return
	}

	for _, skill := range active {
		result := ValidateSkill(skill)
		if !result.Valid {
			debug.Log("knight", "skill %s has issues: %v", skill.Name, result.Errors)
		}
		// TODO: mark stale skills, notify user via IM
	}
}

// nightlyMaintenance runs deep maintenance tasks.
func (k *Knight) nightlyMaintenance(ctx context.Context) {
	debug.Log("knight", "running nightly maintenance")
	k.emitReport("Knight 夜间维护开始")
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
