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
	"github.com/topcheer/ggcode/internal/safego"
	"github.com/topcheer/ggcode/internal/session"
)

// Emitter is the interface Knight uses to send IM notifications.
type Emitter interface {
	EmitKnightReport(report string)
	HasTargets() bool
}

// EventSink receives Knight task lifecycle events for display in TUI/daemon.
type EventSink interface {
	// OnTaskStart is called when a Knight task begins.
	OnTaskStart(taskName string)
	// OnTaskComplete is called when a Knight task finishes with a detailed report.
	OnTaskComplete(taskName string, report string, duration time.Duration)
}

// Knight is the background auto-evolution agent. It runs scheduled tasks
// during idle time, analyzes sessions, creates and validates skills.
type Knight struct {
	cfg          config.KnightConfig
	budget       *Budget
	bucketBudget *bucketBudget
	index        *SkillIndex
	promoter     *Promoter
	usage        *UsageTracker
	queue        *CandidateQueue
	rejects      *rejectFeedbackStore
	emitGate     *emitThrottle
	store        session.Store
	emitter      Emitter
	sink         EventSink
	factory      AgentFactory
	homeDir      string
	projDir      string
	lock         *instanceLock // cross-process lock for exclusive Knight access

	mu       sync.Mutex
	running  bool
	cancel   context.CancelFunc
	lastIdle time.Time

	// Throttle timestamps — prevent running the same task every tick
	lastAnalysis           time.Time
	lastAnalysisAttempt    time.Time
	lastValidation         time.Time
	lastValidationAttempt  time.Time
	lastMaintenance        time.Time
	lastMaintenanceAttempt time.Time

	// Session dedup — persists across ticks, survives analyzer recreation
	analyzedSessions map[string]time.Time // session ID → time of last analysis
	notifiedStaging  map[string]bool      // tracks staging skills already notified to avoid spam
	stagingFailCount map[string]int       // consecutive validation failure count per staging skill
}

const (
	knightTickInterval              = 5 * time.Minute
	knightInitialDelay              = 1 * time.Minute
	knightAnalysisEvery             = 1 * time.Hour
	knightValidationEvery           = 6 * time.Hour
	knightMaintenanceEvery          = 24 * time.Hour
	knightRetryBackoffEvery         = 15 * time.Minute
	knightMaxGeneratedSkills        = 3
	knightMaxGenFailures            = 3 // abandon candidate after this many consecutive generation failures
	knightStagingMaxValidationFails = 3 // auto-reject after N consecutive validation failures
	knightPromptIgnoredThreshold    = 5 // shown this many times with zero explicit use → improve trigger copy
	knightPromptOutcomeMinSamples   = 3 // enough weak run outcomes to consider noisy prompt guidance
)

// New creates a new Knight instance.
func New(cfg config.KnightConfig, homeDir, projDir string, store session.Store) *Knight {
	knightDir := filepath.Join(homeDir, ".ggcode")
	return &Knight{
		cfg:              cfg,
		budget:           NewBudget(knightDir, cfg),
		bucketBudget:     newBucketBudget(cfg.DailyTokenBudget),
		index:            NewSkillIndex(homeDir, projDir),
		promoter:         NewPromoter(homeDir, projDir),
		usage:            NewUsageTracker(filepath.Join(projDir, ".ggcode", "skill-usage.json")),
		queue:            NewCandidateQueue(filepath.Join(projDir, ".ggcode", "knight-candidate-queue.json")),
		rejects:          newRejectFeedbackStore(filepath.Join(projDir, ".ggcode", "knight-reject-feedback.jsonl")),
		emitGate:         newEmitThrottle(0),
		store:            store,
		homeDir:          homeDir,
		projDir:          projDir,
		notifiedStaging:  make(map[string]bool),
		stagingFailCount: make(map[string]int),
		analyzedSessions: make(map[string]time.Time),
	}
}

// ErrLockConflict is returned by Start when another ggcode instance in the same
// project directory already holds the Knight lock. Callers can check this to
// show a user-facing hint without treating it as a real error.
var ErrLockConflict = fmt.Errorf("knight: another instance already running in this workspace")

// Start begins the Knight background loop.
func (k *Knight) Start(ctx context.Context) error {
	if !k.cfg.Enabled {
		debug.Log("knight", "disabled, not starting")
		return nil
	}

	// Acquire cross-process lock — only one Knight per project directory.
	lock := tryAcquireLock(k.projDir)
	if lock == nil {
		pid, _ := LockHeldBy(k.projDir)
		debug.Log("knight", "%s", FormatLockMessage(pid))
		return ErrLockConflict
	}
	k.lock = lock

	if err := k.budget.EnsureDir(); err != nil {
		k.lock.release()
		k.lock = nil
		return fmt.Errorf("knight: init budget dir: %w", err)
	}
	if err := k.usage.EnsureDir(); err != nil {
		k.lock.release()
		k.lock = nil
		return fmt.Errorf("knight: init usage dir: %w", err)
	}
	if err := k.queue.EnsureDir(); err != nil {
		k.lock.release()
		k.lock = nil
		return fmt.Errorf("knight: init candidate queue dir: %w", err)
	}

	// Ensure staging directories exist
	for _, dir := range []string{
		filepath.Join(k.homeDir, ".ggcode", "skills-staging"),
		filepath.Join(k.projDir, ".ggcode", "skills-staging"),
		filepath.Join(k.projDir, ".ggcode", "skills-snapshots"),
	} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			k.lock.release()
			k.lock = nil
			return fmt.Errorf("knight: create dir %s: %w", dir, err)
		}
	}
	if !strings.EqualFold(k.cfg.TrustLevel, "readonly") {
		if migrated, err := k.normalizeActiveSkillLayout(); err != nil {
			debug.Log("knight", "normalize active skill layout failed: %v", err)
		} else if migrated > 0 {
			debug.Log("knight", "normalized %d loose active skill files into standard SKILL.md layout", migrated)
		}
	}

	k.mu.Lock()
	if k.running {
		k.mu.Unlock()
		debug.Log("knight", "already running, start skipped")
		return nil
	}
	ctx, cancel := context.WithCancel(ctx)
	k.cancel = cancel
	k.running = true
	k.lastIdle = time.Now()
	k.mu.Unlock()

	safego.Go("knight.runLoop", func() { k.runLoop(ctx) })
	debug.Log("knight", "started (budget=%dM, trust=%s)", k.cfg.DailyTokenBudget/1_000_000, k.cfg.TrustLevel)
	return nil
}

// Stop gracefully shuts down Knight.
func (k *Knight) Stop() {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.cancel != nil {
		k.cancel()
		k.cancel = nil
	}
	k.running = false
	if k.lock != nil {
		k.lock.release()
		k.lock = nil
	}
	debug.Log("knight", "stopped")
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

// SetEventSink sets the event sink for task lifecycle notifications.
func (k *Knight) SetEventSink(s EventSink) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.sink = s
}

// FuncSink is an EventSink that delegates to callback functions.
type FuncSink struct {
	OnStart    func(taskName string)
	OnComplete func(taskName string, report string, duration time.Duration)
}

func (s *FuncSink) OnTaskStart(taskName string) {
	if s.OnStart != nil {
		s.OnStart(taskName)
	}
}

func (s *FuncSink) OnTaskComplete(taskName string, report string, duration time.Duration) {
	if s.OnComplete != nil {
		s.OnComplete(taskName, report, duration)
	}
}

// getEventSink returns the current event sink under lock protection.
func (k *Knight) getEventSink() EventSink {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.sink
}

// getFactory returns the current agent factory under lock protection.
func (k *Knight) getFactory() AgentFactory {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.factory
}

// RecordSkillUse increments the usage counter for a skill.
// Called from the skill tool when a Knight-managed skill is loaded.
func (k *Knight) RecordSkillUse(ref string) {
	if k.usage != nil {
		k.usage.RecordUse(ref)
	}
	k.syncSkillMetadata(ref)
}

// RecordSkillPromptExposure records that one or more active skills were visible
// to the model in the system prompt. This is a weak signal, separate from actual
// skill invocation, and helps distinguish "never shown" from "shown but ignored".
func (k *Knight) RecordSkillPromptExposure(refs []string) {
	if k == nil || k.usage == nil {
		return
	}
	seen := make(map[string]struct{}, len(refs))
	for _, ref := range refs {
		ref = strings.TrimSpace(ref)
		if ref == "" {
			continue
		}
		if _, ok := seen[ref]; ok {
			continue
		}
		seen[ref] = struct{}{}
		k.usage.RecordPromptExposure(ref)
		k.syncSkillMetadata(ref)
	}
}

// RecordPromptSkillOutcome records a weak task-level outcome for prompt-visible
// skills. It should be used only as a soft signal because prompt visibility does
// not prove the skill caused the outcome.
func (k *Knight) RecordPromptSkillOutcome(refs []string, success bool) {
	if k == nil || k.usage == nil {
		return
	}
	seen := make(map[string]struct{}, len(refs))
	for _, ref := range refs {
		ref = strings.TrimSpace(ref)
		if ref == "" {
			continue
		}
		if _, ok := seen[ref]; ok {
			continue
		}
		seen[ref] = struct{}{}
		k.usage.RecordPromptOutcome(ref, success)
		k.syncSkillMetadata(ref)
	}
}

// RecordSkillEffectiveness records a 1-5 effectiveness score for a skill.
func (k *Knight) RecordSkillEffectiveness(ref string, score int) {
	if k.usage != nil {
		k.usage.RecordEffectiveness(ref, score)
	}
	k.syncSkillMetadata(ref)
}

// SkillUsage returns runtime usage stats for a skill.
func (k *Knight) SkillUsage(ref string) (count int, lastUsed time.Time, avgScore float64) {
	if k.usage == nil {
		return 0, time.Time{}, 0
	}
	return k.usage.GetUsage(ref)
}

// SkillPromptExposure returns how often a skill has been visible in the prompt.
func (k *Knight) SkillPromptExposure(ref string) (count int, lastExposed time.Time) {
	if k.usage == nil {
		return 0, time.Time{}
	}
	return k.usage.GetPromptExposure(ref)
}

// SkillPromptOutcome returns weak success/failure counts from runs where a skill
// was visible in the prompt.
func (k *Knight) SkillPromptOutcome(ref string) (successes int, failures int) {
	if k.usage == nil {
		return 0, 0
	}
	return k.usage.GetPromptOutcome(ref)
}

// SkillFeedback returns runtime feedback stats for a skill.
func (k *Knight) SkillFeedback(ref string) (avgScore float64, samples int) {
	if k.usage == nil {
		return 0, 0
	}
	return k.usage.GetFeedback(ref)
}

// BudgetStatus returns current Knight token usage counters.
func (k *Knight) BudgetStatus() (used int, remaining int, limit int) {
	return k.budget.Used(), k.budget.Remaining(), k.budget.DailyLimit()
}

// SetSkillFrozen updates an active skill's frozen flag.
func (k *Knight) SetSkillFrozen(name string, frozen bool) error {
	entry, err := k.FindActiveSkill(name)
	if err != nil {
		return err
	}
	if err := updateSkillFrontmatter(entry.Path, func(fmMap map[string]interface{}) {
		fmMap["frozen"] = frozen
	}); err != nil {
		return fmt.Errorf("update skill %q frontmatter: %w", name, err)
	}
	k.index.Invalidate()
	return nil
}

// RollbackSkill restores the latest snapshot for an active skill.
func (k *Knight) RollbackSkill(name string) error {
	entry, err := k.FindActiveSkill(name)
	if err != nil {
		return err
	}
	if err := k.promoter.Rollback(entry); err != nil {
		return err
	}
	k.index.Invalidate()
	if k.rejects != nil {
		_ = k.rejects.Append(rejectFeedbackEntry{
			Name:     entry.Name,
			Scope:    entry.Scope,
			Action:   "rollback",
			Reporter: "user",
			Reason:   "manual rollback",
		})
	}
	return nil
}

// FindActiveSkill resolves an active skill reference, optionally scoped as project:name or global:name.
func (k *Knight) FindActiveSkill(ref string) (*SkillEntry, error) {
	active, err := k.index.ActiveSkills()
	if err != nil {
		return nil, err
	}
	return findSkillByRef(active, ref, "active")
}

// FindStagingSkill resolves a staging skill reference, optionally scoped as project:name or global:name.
func (k *Knight) FindStagingSkill(ref string) (*SkillEntry, error) {
	staging, err := k.index.StagingSkills()
	if err != nil {
		return nil, err
	}
	return findSkillByRef(staging, ref, "staging")
}

// Index returns the skill index for external access.
func (k *Knight) Index() *SkillIndex {
	return k.index
}

// Queue returns the candidate queue for external queries.
func (k *Knight) Queue() *CandidateQueue {
	return k.queue
}

// Status returns a human-readable status string.
func (k *Knight) Status() string {
	if !k.cfg.Enabled {
		return "disabled"
	}
	if !k.running {
		if k.lock == nil {
			pid, _ := LockHeldBy(k.projDir)
			if pid > 0 {
				return fmt.Sprintf("deferred — instance PID %d holds lock", pid)
			}
		}
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
	s, err := k.FindStagingSkill(skillName)
	if err != nil {
		return err
	}
	result := ValidateSkill(s)
	if !result.Valid {
		return fmt.Errorf("skill %q failed validation: %s", skillName, result.Errors)
	}

	active, _ := k.index.ActiveSkills()
	if CheckDuplicate(s, active) && !stagingUpdatesActiveSkill(s, active) {
		return fmt.Errorf("skill %q duplicates an existing skill", skillName)
	}

	if err := k.promoter.Promote(s); err != nil {
		return err
	}
	k.index.Invalidate()
	k.clearStagingNotification(s.Name)
	if k.emitGate != nil {
		k.emitGate.reset(s.Name)
	}
	_ = k.RecordSemanticMemory("skill-promoted",
		fmt.Sprintf("promoted skill %s — %s", s.Name, s.Meta.Description),
		[]string{s.Scope + ":" + s.Name}, s.Path)
	return nil
}

// RejectStaging removes a staging skill.
func (k *Knight) RejectStaging(skillName string) error {
	s, err := k.FindStagingSkill(skillName)
	if err != nil {
		return err
	}
	if err := k.promoter.Reject(s); err != nil {
		return err
	}
	k.index.Invalidate()
	k.clearStagingNotification(s.Name)
	if k.emitGate != nil {
		k.emitGate.reset(s.Name)
	}
	if k.rejects != nil {
		_ = k.rejects.Append(rejectFeedbackEntry{
			Name:     s.Name,
			Scope:    s.Scope,
			Action:   "reject",
			Reporter: "user",
		})
	}
	_ = k.RecordSemanticMemory("skill-rejected",
		fmt.Sprintf("user rejected staged skill %s — %s", s.Name, s.Meta.Description),
		[]string{s.Scope + ":" + s.Name}, s.Path)
	return nil
}

// runLoop is the main Knight background loop.
func (k *Knight) runLoop(ctx context.Context) {
	// Tick every 5 minutes
	ticker := time.NewTicker(knightTickInterval)
	defer ticker.Stop()

	// Initial delay — wait 1 minute before first task
	select {
	case <-time.After(knightInitialDelay):
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
	if isIdle && k.hasCapability("skill_creation") && shouldRunScheduledTask(now, k.lastAnalysis, k.lastAnalysisAttempt, knightAnalysisEvery, knightRetryBackoffEvery) {
		k.lastAnalysisAttempt = now
		sink := k.getEventSink()
		if sink != nil {
			sink.OnTaskStart("session-analysis")
		}
		analysisStart := time.Now()
		if err := k.analyzeRecentSessions(ctx); err != nil {
			debug.Log("knight", "scheduled analysis failed: %v", err)
		} else {
			k.lastAnalysis = now
		}
		if sink != nil {
			sink.OnTaskComplete("session-analysis", fmt.Sprintf("analyzed sessions (next in %s)", knightAnalysisEvery), time.Since(analysisStart))
		}
	}

	// Every 6 hours: validate all skills
	if k.hasCapability("skill_validation") && shouldRunScheduledTask(now, k.lastValidation, k.lastValidationAttempt, knightValidationEvery, knightRetryBackoffEvery) {
		k.lastValidationAttempt = now
		sink := k.getEventSink()
		if sink != nil {
			sink.OnTaskStart("skill-validation")
		}
		validationStart := time.Now()
		if err := k.validateAllSkills(ctx); err != nil {
			debug.Log("knight", "scheduled validation failed: %v", err)
		} else {
			k.lastValidation = now
		}
		if sink != nil {
			sink.OnTaskComplete("skill-validation", "validated all skills", time.Since(validationStart))
		}
	}

	// Nightly (2 AM): deep maintenance
	if now.Hour() == 2 && shouldRunScheduledTask(now, k.lastMaintenance, k.lastMaintenanceAttempt, knightMaintenanceEvery, knightRetryBackoffEvery) {
		k.lastMaintenanceAttempt = now
		sink := k.getEventSink()
		if sink != nil {
			sink.OnTaskStart("nightly-maintenance")
		}
		maintenanceStart := time.Now()
		if err := k.nightlyMaintenance(ctx); err != nil {
			debug.Log("knight", "scheduled maintenance failed: %v", err)
		} else {
			k.lastMaintenance = now
		}
		if sink != nil {
			sink.OnTaskComplete("nightly-maintenance", "nightly maintenance completed", time.Since(maintenanceStart))
		}
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
			k.stagingFailCount[s.Name]++
			debug.Log("knight", "staging skill %s validation failed (%d/%d): %v",
				s.Name, k.stagingFailCount[s.Name], knightStagingMaxValidationFails, result.Errors)
			if k.stagingFailCount[s.Name] >= knightStagingMaxValidationFails {
				debug.Log("knight", "auto-rejecting staging skill %s after %d consecutive validation failures",
					s.Name, k.stagingFailCount[s.Name])
				if err := k.promoter.Reject(s); err != nil {
					debug.Log("knight", "reject staging skill %s failed: %v", s.Name, err)
				} else {
					k.index.Invalidate()
					k.clearStagingNotification(s.Name)
					delete(k.stagingFailCount, s.Name)
					if k.rejects != nil {
						_ = k.rejects.Append(rejectFeedbackEntry{
							Name:     s.Name,
							Scope:    s.Scope,
							Action:   "auto-reject",
							Reporter: "validator",
							Reason:   fmt.Sprintf("%d consecutive validation failures: %v", knightStagingMaxValidationFails, result.Errors),
						})
					}
					_ = k.RecordSemanticMemory("skill-auto-rejected",
						fmt.Sprintf("auto-rejected staging skill %s after %d validation failures: %v", s.Name, knightStagingMaxValidationFails, result.Errors),
						[]string{s.Scope + ":" + s.Name}, s.Path)
					k.emitReportKeyed(fmt.Sprintf("🗑️ Auto-rejected staging skill %s: failed validation %d times (%v)",
						s.Name, knightStagingMaxValidationFails, result.Errors), s.Name, EmitSeverityNotice)
				}
			}
			continue
		}
		// Valid — reset failure count
		delete(k.stagingFailCount, s.Name)
		isRevision := stagingUpdatesActiveSkill(s, active)
		if CheckDuplicate(s, active) && !isRevision {
			debug.Log("knight", "staging skill %s is duplicate, rejecting", s.Name)
			if err := k.promoter.Reject(s); err != nil {
				debug.Log("knight", "reject staging skill %s failed: %v", s.Name, err)
				continue
			}
			k.index.Invalidate()
			continue
		}

		if k.cfg.TrustLevel == "auto" {
			if allowed, reason := canAutoPromoteStagingSkill(s, result, isRevision); !allowed {
				if k.markStagingNotified(s.Name) {
					k.emitReportKeyed(fmt.Sprintf("📝 Skill candidate requires review: %s\n%s\nReason: %s\n👉 /knight approve %s to promote / /knight reject %s to decline",
						s.Name, s.Meta.Description, reason, s.Name, s.Name), s.Name, EmitSeverityActionRequired)
				}
				continue
			}
			if allowed, reason := k.evaluateAutoPromoteCandidate(ctx, s); !allowed {
				if k.markStagingNotified(s.Name) {
					k.emitReportKeyed(fmt.Sprintf("📝 Skill candidate requires review: %s\n%s\nReason: %s\n👉 /knight approve %s to promote / /knight reject %s to decline",
						s.Name, s.Meta.Description, reason, s.Name, s.Name), s.Name, EmitSeverityActionRequired)
				}
				continue
			}
			debug.Log("knight", "auto-promoting skill %s", s.Name)
			if err := k.promoter.Promote(s); err != nil {
				debug.Log("knight", "auto-promote skill %s failed: %v", s.Name, err)
				continue
			}
			k.index.Invalidate()
			k.clearStagingNotification(s.Name)
			if k.emitGate != nil {
				k.emitGate.reset(s.Name)
			}
			k.emitReport(fmt.Sprintf("✅ Skill auto-promoted: %s (%s)", s.Name, s.Meta.Description))
			_ = k.RecordSemanticMemory("skill-auto-promoted",
				fmt.Sprintf("auto-promoted skill %s — %s", s.Name, s.Meta.Description),
				[]string{s.Scope + ":" + s.Name}, s.Path)
		} else {
			// For "staged" trust level, notify user once per skill
			if k.markStagingNotified(s.Name) {
				k.emitReportKeyed(fmt.Sprintf("📝 New skill candidate: %s\n%s\n👉 /knight approve %s to promote / /knight reject %s to decline",
					s.Name, s.Meta.Description, s.Name, s.Name), s.Name, EmitSeverityActionRequired)
			}
		}
	}
}

func stagingUpdatesActiveSkill(staging *SkillEntry, active []*SkillEntry) bool {
	if staging == nil {
		return false
	}
	for _, entry := range active {
		if entry == nil || entry.Staging {
			continue
		}
		if entry.Name == staging.Name && entry.Scope == staging.Scope {
			return true
		}
	}
	return false
}

func canAutoPromoteStagingSkill(entry *SkillEntry, validation ValidationResult, isRevision bool) (bool, string) {
	if entry == nil {
		return false, "skill entry is unavailable"
	}
	if isRevision {
		return false, "updates to existing active skills require explicit review"
	}
	if !strings.EqualFold(strings.TrimSpace(entry.Meta.CreatedBy), "knight") {
		return false, "only Knight-created staging skills may be auto-promoted"
	}
	if entry.Scope != "project" {
		return false, "only project-scoped skills are eligible for auto-promotion"
	}
	if len(entry.Meta.Requires) > 0 {
		return false, "skills with external command dependencies require explicit review"
	}
	if len(validation.Warnings) > 0 {
		return false, "validation warnings require explicit review"
	}
	return true, ""
}

func (k *Knight) evaluateAutoPromoteCandidate(ctx context.Context, entry *SkillEntry) (bool, string) {
	factory := k.getFactory()
	if factory == nil {
		k.appendAutoPromoteEval(entry, autoPromoteEvalDecision{
			Rationale:   "scenario evaluation unavailable",
			FailureMode: "no_factory",
		})
		return false, "scenario evaluation unavailable"
	}
	content, err := readSkillContent(entry.Path)
	if err != nil {
		reason := fmt.Sprintf("cannot read staging skill for scenario evaluation: %v", err)
		k.appendAutoPromoteEval(entry, autoPromoteEvalDecision{
			Rationale:   reason,
			FailureMode: "read_error",
		})
		return false, fmt.Sprintf("cannot read staging skill for scenario evaluation: %v", err)
	}
	// Deterministic rule-based overlap check. Run before invoking the LLM so
	// that an obviously redundant candidate doesn't burn eval-bucket tokens.
	active, _ := k.index.ActiveSkills()
	overlap := computeRuleBasedOverlap(entry, string(content), active, func(e *SkillEntry) string {
		body, _ := readSkillContent(e.Path)
		return string(body)
	})
	if overlap.HasOverlap {
		decision := autoPromoteEvalDecision{
			RuleOverlap:        true,
			RuleOverlapJaccard: overlap.WorstSimilarity,
			RuleOverlapWith:    overlap.WorstActiveRef,
			Rationale:          formatOverlapRationale(overlap),
		}
		decision.finalizeFailureMode()
		k.appendAutoPromoteEval(entry, decision)
		return false, decision.Rationale
	}
	// Deterministic A/B replay against recent prompt scenarios. Block only when
	// we DO have scenarios and the candidate clearly does not cover any of them
	// — that protects against promoting skills that don't match real usage.
	scenariosForReplay, _ := k.RecentSkillScenarios(40)
	var baselineBody string
	if overlap.WorstActiveRef != "" {
		for _, e := range active {
			if e == nil {
				continue
			}
			if (e.Scope + ":" + e.Name) == overlap.WorstActiveRef {
				if body, err := readSkillContent(e.Path); err == nil {
					baselineBody = string(body)
				}
				break
			}
		}
	}
	replay := computeABReplayScore(entry, string(content), baselineBody, scenariosForReplay)
	if replay.ScenariosConsidered >= 5 && replay.CandidateScore < 0.05 {
		decision := autoPromoteEvalDecision{
			ReplayScenarios:      replay.ScenariosConsidered,
			ReplayCandidateScore: replay.CandidateScore,
			ReplayBaselineScore:  replay.BaselineScore,
			ReplayDelta:          replay.Delta,
			ReplayVerdict:        abReplayVerdict(replay),
			Rationale:            "A/B replay: " + abReplayVerdict(replay),
			FailureMode:          "replay_no_coverage",
		}
		k.appendAutoPromoteEval(entry, decision)
		return false, decision.Rationale
	}
	scenarioContext := k.formatRecentSkillScenariosForEval(8)
	hasSavedScenarios := strings.TrimSpace(scenarioContext) != ""
	if scenarioContext == "" {
		scenarioContext = "No saved project scenarios are available yet."
	}
	baselineContext := k.formatActiveSkillBaselinesForEval(entry, 8)
	hasActiveBaseline := strings.TrimSpace(baselineContext) != ""
	if baselineContext == "" {
		baselineContext = "No active baseline skills are available yet."
	}
	memoryContext := k.formatRecentSemanticMemoryForEval(8)
	if memoryContext == "" {
		memoryContext = "No prior Knight lessons recorded yet."
	}
	prompt := fmt.Sprintf(`Evaluate whether this staged project skill is safe and useful enough for automatic promotion.

This is a conservative gate. The skill will affect future agent behavior on the user's project.

Approve only if all are true:
- The skill is project-facing and practical
- The trigger guidance is specific enough to avoid broad/noisy activation
- The workflow is low-risk and does not require destructive actions, external services, credentials, publishing, or broad code rewrites
- The skill adds concrete value beyond generic advice
- The skill adds distinct value beyond existing active skills and is not just a duplicate or narrower rewrite of an active skill

Replay check:
- Invent two realistic positive user tasks where this skill SHOULD be selected.
- Invent two realistic negative user tasks where this skill SHOULD NOT be selected.
- Compare the skill's description and when_to_use against those scenarios.
- Use the saved project scenarios below as additional real-world examples; do not approve if the skill would be selected for unrelated saved tasks.
- Compare the staged skill against the active baseline skills below; do not approve if existing active skills already cover the same trigger/workflow well enough.
- Approve only if the positives are clearly covered and the negatives are clearly excluded.

Return exactly:
PROMOTE: yes
REPLAY: pass
SAVED_REPLAY: pass
FALSE_POSITIVES: 0
FALSE_NEGATIVES: 0
BASELINE_REPLAY: pass
OVERLAP_COUNT: 0
RATIONALE: one short sentence

or:
PROMOTE: no
REPLAY: fail
SAVED_REPLAY: fail
FALSE_POSITIVES: <number>
FALSE_NEGATIVES: <number>
BASELINE_REPLAY: fail
OVERLAP_COUNT: <number>
RATIONALE: one short sentence

Use SAVED_REPLAY: skip only when no saved project scenarios are available.
Use BASELINE_REPLAY: skip only when no active baseline skills are available.

Saved project scenarios:
%s

Active baseline skills:
%s

Prior Knight lessons (semantic memory; promoted skills + approved proposals):
%s

Staged skill:
%s`, scenarioContext, baselineContext, memoryContext, string(content))
	result := k.RunTaskWithTurns(ctx, "skill-auto-promote-eval", prompt, factory, 3)
	if result.Error != nil {
		reason := fmt.Sprintf("scenario evaluation failed: %v", result.Error)
		k.appendAutoPromoteEval(entry, autoPromoteEvalDecision{
			Rationale:   reason,
			RawOutput:   result.Output,
			FailureMode: "runner_error",
		})
		return false, fmt.Sprintf("scenario evaluation failed: %v", result.Error)
	}
	decision := parseAutoPromoteEvalDecision(result.Output)
	decision.SavedReplayRequired = hasSavedScenarios
	decision.BaselineReplayRequired = hasActiveBaseline
	decision.RuleOverlapJaccard = overlap.WorstSimilarity
	decision.RuleOverlapWith = overlap.WorstActiveRef
	decision.ReplayScenarios = replay.ScenariosConsidered
	decision.ReplayCandidateScore = replay.CandidateScore
	decision.ReplayBaselineScore = replay.BaselineScore
	decision.ReplayDelta = replay.Delta
	decision.ReplayVerdict = abReplayVerdict(replay)
	decision.finalizeFailureMode()
	k.appendAutoPromoteEval(entry, decision)
	if !decision.Allowed() {
		if decision.Rationale == "" {
			decision.Rationale = "scenario evaluation did not approve auto-promotion"
		}
		return false, decision.Rationale
	}
	return true, ""
}

type autoPromoteEvalDecision struct {
	Promote                bool
	ReplayPassed           bool
	SavedReplayRequired    bool
	SavedReplayStatus      string
	FalsePositiveCount     int
	FalseNegativeCount     int
	BaselineReplayRequired bool
	BaselineReplayStatus   string
	OverlapCount           int
	RuleOverlap            bool
	RuleOverlapJaccard     float64
	RuleOverlapWith        string
	ReplayScenarios        int
	ReplayCandidateScore   float64
	ReplayBaselineScore    float64
	ReplayDelta            float64
	ReplayVerdict          string
	Rationale              string
	RawOutput              string
	FailureMode            string
}

func (d autoPromoteEvalDecision) Allowed() bool {
	if d.RuleOverlap {
		return false
	}
	if !d.Promote || !d.ReplayPassed {
		return false
	}
	if !d.SavedReplayRequired {
		return !d.BaselineReplayRequired || (d.BaselineReplayStatus == "pass" && d.OverlapCount == 0)
	}
	if d.SavedReplayStatus != "pass" || d.FalsePositiveCount != 0 || d.FalseNegativeCount != 0 {
		return false
	}
	return !d.BaselineReplayRequired || (d.BaselineReplayStatus == "pass" && d.OverlapCount == 0)
}

func (d *autoPromoteEvalDecision) finalizeFailureMode() {
	if d.RuleOverlap {
		d.FailureMode = "rule_overlap"
		return
	}
	if !d.Promote {
		d.FailureMode = "promote_rejected"
	} else if !d.ReplayPassed {
		d.FailureMode = "replay_failed"
	} else if d.SavedReplayRequired && (d.SavedReplayStatus != "pass" || d.FalsePositiveCount != 0 || d.FalseNegativeCount != 0) {
		d.FailureMode = "saved_replay_failed"
	} else if d.BaselineReplayRequired && (d.BaselineReplayStatus != "pass" || d.OverlapCount != 0) {
		d.FailureMode = "baseline_replay_failed"
	} else {
		d.FailureMode = ""
	}
}

func parseAutoPromoteEvalOutput(output string) (bool, string) {
	decision := parseAutoPromoteEvalDecision(output)
	return decision.Allowed(), decision.Rationale
}

func parseAutoPromoteEvalDecision(output string) autoPromoteEvalDecision {
	decision := autoPromoteEvalDecision{RawOutput: output}
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		lower := strings.ToLower(line)
		switch {
		case strings.HasPrefix(lower, "promote:"):
			value := strings.TrimSpace(strings.TrimPrefix(lower, "promote:"))
			decision.Promote = value == "yes"
		case strings.HasPrefix(lower, "replay:"):
			value := strings.TrimSpace(strings.TrimPrefix(lower, "replay:"))
			decision.ReplayPassed = value == "pass"
		case strings.HasPrefix(lower, "saved_replay:"):
			value := strings.TrimSpace(strings.TrimPrefix(lower, "saved_replay:"))
			decision.SavedReplayStatus = value
		case strings.HasPrefix(lower, "false_positives:"):
			value := strings.TrimSpace(strings.TrimPrefix(lower, "false_positives:"))
			if n, err := strconv.Atoi(value); err == nil && n >= 0 {
				decision.FalsePositiveCount = n
			}
		case strings.HasPrefix(lower, "false_negatives:"):
			value := strings.TrimSpace(strings.TrimPrefix(lower, "false_negatives:"))
			if n, err := strconv.Atoi(value); err == nil && n >= 0 {
				decision.FalseNegativeCount = n
			}
		case strings.HasPrefix(lower, "baseline_replay:"):
			value := strings.TrimSpace(strings.TrimPrefix(lower, "baseline_replay:"))
			decision.BaselineReplayStatus = value
		case strings.HasPrefix(lower, "overlap_count:"):
			value := strings.TrimSpace(strings.TrimPrefix(lower, "overlap_count:"))
			if n, err := strconv.Atoi(value); err == nil && n >= 0 {
				decision.OverlapCount = n
			}
		case strings.HasPrefix(lower, "rationale:"):
			decision.Rationale = strings.TrimSpace(line[len("RATIONALE:"):])
		}
	}
	decision.finalizeFailureMode()
	return decision
}

// analyzeRecentSessions scans recent session history for reusable patterns.
// High-score candidates are refined via LLM and written to staging.
func (k *Knight) analyzeRecentSessions(ctx context.Context) error {
	analyzer := NewSessionAnalyzer(k)
	result := &AnalysisResult{}
	if k.store != nil {
		var err error
		result, err = analyzer.AnalyzeRecent(ctx)
		if err != nil {
			return err
		}
	}
	queued, err := k.queue.List()
	if err != nil {
		return err
	}
	candidates := mergeSkillCandidates(queued, result.SkillCandidates)
	if result.SessionsAnalyzed == 0 && len(candidates) == 0 {
		return nil
	}

	active, _ := k.index.ActiveSkills()
	staging, _ := k.index.StagingSkills()

	// Process candidates: only converged candidates become staging writes.
	var reported []SkillCandidate
	generatedCount := 0
	deferredCount := 0
	for _, c := range candidates {
		if k.isKnownCandidate(c, active, staging) {
			debug.Log("knight", "skip known candidate %s (%s)", c.Name, c.Scope)
			_ = k.queue.Remove(c)
			continue
		}
		if k.shouldStageCandidate(c) && k.getFactory() != nil && !strings.EqualFold(k.cfg.TrustLevel, "readonly") {
			if generatedCount >= knightMaxGeneratedSkills {
				deferredCount++
				_ = k.queue.Upsert(c)
				c.Reason += fmt.Sprintf(" (deferred: per-tick generation cap %d reached)", knightMaxGeneratedSkills)
				reported = append(reported, c)
				continue
			}
			generatedCount++
			// High-value candidate: use LLM to generate a proper skill
			content, genErr := analyzer.GenerateSkillFromAnalysis(ctx, c, k.getFactory())
			if genErr != nil {
				c.GenFailCount++
				if c.GenFailCount >= knightMaxGenFailures {
					debug.Log("knight", "abandoning candidate %s after %d consecutive generation failures", c.Name, c.GenFailCount)
					_ = k.queue.Remove(c)
				} else {
					debug.Log("knight", "LLM skill generation failed for %s (%d/%d): %v", c.Name, c.GenFailCount, knightMaxGenFailures, genErr)
					_ = k.queue.Upsert(c)
				}
				reported = append(reported, c)
				continue
			}
			stagingCandidate, stagingContent, normalizeErr := k.normalizeGeneratedSkillDocument(c, content)
			if normalizeErr != nil {
				c.GenFailCount++
				debug.Log("knight", "generated skill normalization failed for %s (%d/%d): %v", c.Name, c.GenFailCount, knightMaxGenFailures, normalizeErr)
				if c.GenFailCount >= knightMaxGenFailures {
					_ = k.queue.Remove(c)
				} else {
					_ = k.queue.Upsert(c)
				}
				reported = append(reported, c)
				continue
			}
			if k.isKnownCandidate(stagingCandidate, active, staging) {
				debug.Log("knight", "skip generated known candidate %s (%s)", stagingCandidate.Name, stagingCandidate.Scope)
				_ = k.queue.Remove(c)
				continue
			}
			path, writeErr := k.promoter.WriteStaging(stagingCandidate.Name, stagingCandidate.Scope, stagingContent)
			if writeErr != nil {
				debug.Log("knight", "write staging failed for %s: %v", stagingCandidate.Name, writeErr)
				_ = k.queue.Upsert(c)
				reported = append(reported, c)
				continue
			}
			_ = k.queue.Remove(c)
			staging = append(staging, &SkillEntry{
				Name: stagingCandidate.Name,
				Meta: SkillMeta{
					Name:        stagingCandidate.Name,
					Description: stagingCandidate.Description,
					Scope:       stagingCandidate.Scope,
					CreatedBy:   "knight",
				},
				Path:    path,
				Scope:   stagingCandidate.Scope,
				Staging: true,
			})
			stagingCandidate.Reason += fmt.Sprintf(" (refined and staged: %s)", filepath.Base(path))
			reported = append(reported, stagingCandidate)
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
		report.WriteString(fmt.Sprintf("📊 Analyzed %d sessions and considered %d queued candidates; found %d converged patterns",
			result.SessionsAnalyzed, len(queued), len(reported)))
		for _, c := range reported[:maxShown] {
			report.WriteString(fmt.Sprintf("\n• [%.1f] %s (%s, evidence=%d): %s", c.Score, c.Name, c.Scope, c.EvidenceCount, c.Description))
		}
		if len(reported) > maxShown {
			report.WriteString(fmt.Sprintf("\n… and %d more", len(reported)-maxShown))
		}
		if deferredCount > 0 {
			report.WriteString(fmt.Sprintf("\n⏸️ Deferred %d additional high-value candidates because the per-tick generation cap (%d) was reached.", deferredCount, knightMaxGeneratedSkills))
		}
		k.emitReport(report.String())
	}

	return nil
}

// validateAllSkills checks all active skills for validity and freshness.
func (k *Knight) validateAllSkills(ctx context.Context) error {
	active, err := k.index.ActiveSkills()
	if err != nil {
		return err
	}

	for _, skill := range active {
		// Basic validation
		result := ValidateSkill(skill)
		if !result.Valid {
			debug.Log("knight", "skill %s has issues: %v", skill.Name, result.Errors)
		}
		if skill.Meta.Frozen {
			continue
		}
		skillRef := formatSkillRef(skill.Scope, skill.Name)

		// Freshness check: is the skill stale?
		if k.usage.IsStale(skillRef, 30*24*time.Hour) {
			if k.shouldSuppressStaleNotice(skillRef) {
				continue
			}
			count, lastUsed, _ := k.usage.GetUsage(skillRef)
			exposures, lastExposed := k.usage.GetPromptExposure(skillRef)
			debug.Log("knight", "skill %s may be stale: used=%d last=%v", skill.Name, count, lastUsed)
			staleKey := "stale:" + skillRef
			if count == 0 {
				if exposures > 0 {
					k.emitReportKeyed(fmt.Sprintf("⚠️ Skill '%s' has been shown in the prompt %d times but never invoked (last shown: %s). Consider improving its description/when_to_use or rejecting it if it is noise.", skill.Name, exposures, lastExposed.Format("2006-01-02")), staleKey, EmitSeverityNotice)
				} else {
					k.emitReportKeyed(fmt.Sprintf("⚠️ Skill '%s' has never been used and has not been prompt-visible recently. Consider removing it.", skill.Name), staleKey, EmitSeverityNotice)
				}
			} else {
				k.emitReportKeyed(fmt.Sprintf("⚠️ Skill '%s' not used in 30+ days (last: %s). /knight rate %s 5 if it is still valuable, or let it fade out.", skill.Name, lastUsed.Format("2006-01-02"), skillRef), staleKey, EmitSeverityNotice)
			}
		}

		// Effectiveness check: low average score?
		avgScore, samples, shouldWarn := k.shouldWarnLowEffectiveness(skillRef)
		if shouldWarn {
			debug.Log("knight", "skill %s has low effectiveness: %.1f/5", skill.Name, avgScore)
			k.emitReportKeyed(fmt.Sprintf("📉 Skill '%s' effectiveness: %.1f/5 across %d signals. Consider updating or removing.", skill.Name, avgScore, samples), "low-eff:"+skillRef, EmitSeverityNotice)
			k.maybeStageSkillPatch(ctx, skill, avgScore, samples)
		}
		k.maybeStagePromptSignalPatch(ctx, skill, skillRef)
	}
	return nil
}

// nightlyMaintenance runs deep maintenance tasks.
func (k *Knight) nightlyMaintenance(ctx context.Context) error {
	debug.Log("knight", "running nightly maintenance")
	factory := k.getFactory()
	if factory == nil {
		k.emitReport("Knight nightly maintenance skipped: no task runner configured")
		return fmt.Errorf("no task runner configured")
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
		return nil
	}
	if report, err := k.RunSelfReflection(ctx, 7*24*time.Hour); err == nil {
		sections = append(sections, "🪞 Self-reflection\n"+report.FormatHuman())
	} else {
		debug.Log("knight", "self-reflection failed: %v", err)
	}
	k.emitReport("🌙 Knight nightly maintenance\n\n" + strings.Join(sections, "\n\n"))
	return nil
}

// emitReport sends a Knight report via IM if an emitter is configured.
// If an EventSink is configured, the report is also forwarded as a
// task-complete event for display in TUI/daemon terminal.
func (k *Knight) emitReport(report string) {
	k.emitReportKeyed(report, "", EmitSeverityInfo)
}

// emitReportKeyed adds optional throttling. When key is non-empty, the same
// (key, severity) combination is suppressed for emitThrottle.window after the
// last successful emit. This prevents staging/stale notifications from
// flooding when scheduler ticks every 5 minutes.
func (k *Knight) emitReportKeyed(report, key string, severity EmitSeverity) {
	if k.inQuietHours(time.Now()) {
		return
	}
	if !k.emitGate.allow(key, severity, time.Now()) {
		return
	}
	k.mu.Lock()
	em := k.emitter
	sink := k.sink
	k.mu.Unlock()
	if em != nil && em.HasTargets() {
		em.EmitKnightReport(report)
	}
	if sink != nil {
		sink.OnTaskComplete("", report, 0)
	}
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
	sink := k.getEventSink()
	if sink != nil {
		sink.OnTaskStart(taskName)
	}
	start := time.Now()
	result := k.RunTask(ctx, taskName, prompt, factory)
	dur := time.Since(start)
	output := formatKnightTaskOutput(result.Output)
	if result.Error != nil {
		errMsg := fmt.Sprintf("task failed: %v", result.Error)
		if sink != nil {
			sink.OnTaskComplete(taskName, errMsg, dur)
		}
		return errMsg
	}
	if sink != nil {
		sink.OnTaskComplete(taskName, output, dur)
	}
	return output
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

func findSkillByRef(entries []*SkillEntry, ref string, kind string) (*SkillEntry, error) {
	scope, name := parseSkillRef(ref)
	var matches []*SkillEntry
	for _, entry := range entries {
		if entry.Name != name {
			continue
		}
		if scope != "" && entry.Scope != scope {
			continue
		}
		matches = append(matches, entry)
	}
	switch len(matches) {
	case 0:
		return nil, fmt.Errorf("%s skill %q not found", kind, ref)
	case 1:
		return matches[0], nil
	default:
		return nil, fmt.Errorf("multiple %s skills named %q found; use project:%s or global:%s", kind, name, name, name)
	}
}

func parseSkillRef(ref string) (scope string, name string) {
	ref = strings.TrimSpace(ref)
	parts := strings.SplitN(ref, ":", 2)
	if len(parts) == 2 {
		switch strings.ToLower(strings.TrimSpace(parts[0])) {
		case "project", "global":
			return strings.ToLower(strings.TrimSpace(parts[0])), strings.TrimSpace(parts[1])
		}
	}
	return "", ref
}

func formatSkillRef(scope, name string) string {
	name = strings.TrimSpace(name)
	scope = strings.TrimSpace(scope)
	if scope == "" {
		return name
	}
	return scope + ":" + name
}

// FormatSkillRefForDisplay returns the canonical scope-qualified skill reference.
func FormatSkillRefForDisplay(scope, name string) string {
	return formatSkillRef(scope, name)
}

func shouldRunScheduledTask(now, lastSuccess, lastAttempt time.Time, interval, retryBackoff time.Duration) bool {
	if !lastSuccess.IsZero() && now.Sub(lastSuccess) < interval {
		return false
	}
	if !lastAttempt.IsZero() && now.Sub(lastAttempt) < retryBackoff {
		return false
	}
	return true
}

func (k *Knight) syncSkillMetadata(ref string) {
	if k.usage == nil {
		return
	}
	entry, err := k.FindActiveSkill(ref)
	if err != nil {
		return
	}
	snapshot, ok := k.usage.Snapshot(formatSkillRef(entry.Scope, entry.Name))
	if !ok {
		return
	}
	if err := updateSkillFrontmatter(entry.Path, func(fmMap map[string]interface{}) {
		fmMap["usage_count"] = snapshot.UsageCount
		if snapshot.LastUsed.IsZero() {
			fmMap["last_used"] = nil
		} else {
			fmMap["last_used"] = snapshot.LastUsed.Format(time.RFC3339)
		}
		fmMap["prompt_exposure_count"] = snapshot.PromptExposureCount
		if snapshot.LastPromptExposure.IsZero() {
			fmMap["last_prompt_exposure"] = nil
		} else {
			fmMap["last_prompt_exposure"] = snapshot.LastPromptExposure.Format(time.RFC3339)
		}
		fmMap["prompt_success_count"] = snapshot.PromptSuccessCount
		fmMap["prompt_failure_count"] = snapshot.PromptFailureCount
		fmMap["effectiveness_scores"] = append([]int(nil), snapshot.Effectiveness...)
	}); err == nil {
		k.index.Invalidate()
	} else {
		debug.Log("knight", "sync skill metadata for %s failed: %v", entry.Name, err)
	}
}

func (k *Knight) maybeStageSkillPatch(ctx context.Context, skill *SkillEntry, avgScore float64, samples int) {
	if skill == nil || strings.EqualFold(k.cfg.TrustLevel, "readonly") || !k.hasCapability("skill_creation") {
		return
	}
	content, err := readSkillContent(skill.Path)
	if err != nil {
		debug.Log("knight", "read skill %s for patch failed: %v", skill.Name, err)
		return
	}
	prompt := fmt.Sprintf(`Revise the following existing SKILL.md to improve its usefulness.

Skill name: %s
Scope: %s
Observed effectiveness: %.1f/5 across %d signals

Requirements:
- Keep the same skill name and scope
- Improve clarity, ordering, and guardrails based on the weak effectiveness
- Preserve it as a complete SKILL.md document with valid YAML frontmatter
- Add clearer "When to Use", "When Not to Use", "Steps", and "Gotchas" guidance if missing
- Output only the revised SKILL.md content

	Current skill:
	%s`, skill.Name, skill.Scope, avgScore, samples, string(content))

	report := fmt.Sprintf("🛠️ Staged updated skill '%s' after low-effectiveness signals (%.1f/5 over %d ratings). Review with /knight approve %s or /knight reject %s.", skill.Name, avgScore, samples, skill.Name, skill.Name)
	k.stageSkillRevision(ctx, skill, "skill-patch", prompt, report)
}

func (k *Knight) maybeStagePromptSignalPatch(ctx context.Context, skill *SkillEntry, skillRef string) {
	if skill == nil || k.usage == nil || strings.EqualFold(k.cfg.TrustLevel, "readonly") || !k.hasCapability("skill_creation") {
		return
	}
	uses, _, _ := k.usage.GetUsage(skillRef)
	exposures, _ := k.usage.GetPromptExposure(skillRef)
	successes, failures := k.usage.GetPromptOutcome(skillRef)
	totalOutcomes := successes + failures

	reason := ""
	switch {
	case uses == 0 && exposures >= knightPromptIgnoredThreshold:
		reason = fmt.Sprintf("shown in the prompt %d times but never explicitly invoked", exposures)
	case uses == 0 && totalOutcomes >= knightPromptOutcomeMinSamples && failures >= successes:
		reason = fmt.Sprintf("shown in %d runs with weak outcomes +%d/-%d and never explicitly invoked", totalOutcomes, successes, failures)
	default:
		return
	}

	content, err := readSkillContent(skill.Path)
	if err != nil {
		debug.Log("knight", "read skill %s for prompt-signal patch failed: %v", skill.Name, err)
		return
	}
	prompt := fmt.Sprintf(`Revise the following existing SKILL.md because it is visible to the model but is not being selected reliably.

Skill name: %s
Scope: %s
Prompt signal: %s

Requirements:
- Keep the same skill name and scope
- Do not add risky commands or broaden permissions
- Improve the description and when_to_use so the model can decide when this skill actually applies
- Add or tighten "When Not to Use" if the skill is too broad or noisy
- Keep the workflow concise and project/user-facing
- Preserve it as a complete SKILL.md document with valid YAML frontmatter
- Output only the revised SKILL.md content

Current skill:
%s`, skill.Name, skill.Scope, reason, string(content))

	report := fmt.Sprintf("🛠️ Staged updated skill '%s' after prompt-selection signals (%s). Review with /knight approve %s or /knight reject %s.", skill.Name, reason, skill.Name, skill.Name)
	k.stageSkillRevision(ctx, skill, "skill-prompt-tuning", prompt, report)
}

func (k *Knight) stageSkillRevision(ctx context.Context, skill *SkillEntry, taskName string, prompt string, report string) {
	factory := k.getFactory()
	if factory == nil {
		return
	}
	staging, err := k.index.StagingSkills()
	if err == nil {
		for _, candidate := range staging {
			if candidate.Name == skill.Name {
				return
			}
		}
	}

	result := k.RunTask(ctx, taskName, prompt, factory)
	if result.Error != nil {
		debug.Log("knight", "skill patch generation failed for %s: %v", skill.Name, result.Error)
		return
	}
	revised := strings.TrimSpace(result.Output)
	if revised == "" {
		return
	}
	path, err := k.promoter.WriteStaging(skill.Name, skill.Scope, revised)
	if err != nil {
		debug.Log("knight", "write patched staging skill %s failed: %v", skill.Name, err)
		return
	}
	meta, err := parseSkillFile(path)
	if err != nil {
		_ = os.Remove(path)
		debug.Log("knight", "patched skill %s frontmatter invalid: %v", skill.Name, err)
		return
	}
	validation := ValidateSkill(&SkillEntry{
		Name:    skill.Name,
		Meta:    meta,
		Path:    path,
		Scope:   skill.Scope,
		Staging: true,
	})
	if !validation.Valid {
		_ = os.Remove(path)
		debug.Log("knight", "patched skill %s validation failed: %v", skill.Name, validation.Errors)
		return
	}
	k.index.Invalidate()
	k.emitReport(report)
}

func (k *Knight) shouldStageCandidate(c SkillCandidate) bool {
	// If the user (or auto-reject) recently rejected/rolled back a same-name
	// candidate, suppress regeneration so the next analysis tick doesn't
	// produce the same churn.
	if k.rejects != nil {
		if entry, blocked := k.rejects.coolDownActive(c.Scope, c.Name, time.Now()); blocked {
			debug.Log("knight", "skip candidate %s (cool-down: last %s by %s)", c.Name, entry.Action, entry.Reporter)
			return false
		}
	}
	// Corrections are the highest-value signal — always worth staging
	if c.Category == "correction" && c.Score >= 1.5 {
		return true
	}
	// Failure-fix patterns need at least 2 occurrences across sessions to be reliable
	if c.Category == "failure-fix" && c.EvidenceCount >= 2 {
		return true
	}
	// Fallback for any future category
	return c.Score >= 3.0
}

func (k *Knight) normalizeGeneratedSkillDocument(candidate SkillCandidate, content string) (SkillCandidate, string, error) {
	meta, err := parseSkillFrontmatter(content)
	if err != nil {
		return candidate, "", err
	}
	name := strings.TrimSpace(meta.Name)
	if name == "" {
		name = strings.TrimSpace(candidate.Name)
	}
	if err := validateSkillName(name); err != nil {
		return candidate, "", err
	}
	scope := strings.TrimSpace(meta.Scope)
	if scope == "" {
		scope = strings.TrimSpace(candidate.Scope)
	}
	if scope == "" {
		scope = "project"
	}
	if scope != "global" && scope != "project" {
		return candidate, "", fmt.Errorf("invalid generated skill scope %q", scope)
	}
	if scope == "global" {
		if reason := scopeDowngradeReason(k.projDir, content); reason != "" {
			debug.Log("knight", "scope downgrade global->project for %s: %s", name, reason)
			scope = "project"
			_ = k.RecordSemanticMemory("scope-downgraded",
				fmt.Sprintf("downgraded global skill %s to project: %s", name, reason),
				[]string{"project:" + name}, "")
		}
	}
	normalized, err := mutateSkillFrontmatter(content, func(fmMap map[string]interface{}) {
		fmMap["name"] = name
		fmMap["scope"] = scope
		fmMap["created_by"] = "knight"
	})
	if err != nil {
		return candidate, "", err
	}
	candidate.Name = name
	candidate.Scope = scope
	if strings.TrimSpace(meta.Description) != "" {
		candidate.Description = strings.TrimSpace(meta.Description)
	}
	return candidate, normalized, nil
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
	candFP := skillSimilarityFingerprint(c.Name, c.Description, "")
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
		// Semantic dedup via name+description token Jaccard. 0.6 is empirically
		// the threshold above which two candidates describe the same workflow
		// in this repo's existing skills.
		if jaccardSimilarity(candFP, skillSimilarityFingerprint(s.Name, s.Meta.Description, "")) >= 0.6 {
			return true
		}
	}
	for _, a := range active {
		if a == nil {
			continue
		}
		if jaccardSimilarity(candFP, skillSimilarityFingerprint(a.Name, a.Meta.Description, "")) >= 0.6 {
			return true
		}
	}
	return false
}

func (k *Knight) normalizeActiveSkillLayout() (int, error) {
	loose, err := k.index.LooseActiveSkillFiles()
	if err != nil {
		return 0, err
	}
	migrated := 0
	for _, entry := range loose {
		if !strings.EqualFold(strings.TrimSpace(entry.Meta.CreatedBy), "knight") {
			debug.Log("knight", "loose active skill %s is not created_by=knight; leaving untouched", entry.Path)
			k.emitReportKeyed(
				fmt.Sprintf("Skill file %s sits at the top of a skills directory and will NOT be loaded. Move it to %s/SKILL.md to activate it.", entry.Path, strings.TrimSuffix(entry.Path, ".md")),
				"loose-skill:"+entry.Path,
				EmitSeverityNotice,
			)
			continue
		}
		if entry.Meta.Scope != "" && entry.Meta.Scope != entry.Scope {
			debug.Log("knight", "loose active skill %s scope corrected from %s to %s", entry.Path, entry.Scope, entry.Meta.Scope)
			entry.Scope = entry.Meta.Scope
		}
		if err := k.promoter.MigrateLooseActive(entry); err != nil {
			debug.Log("knight", "migrate loose active skill %s failed: %v", entry.Path, err)
			continue
		}
		migrated++
	}
	if migrated > 0 {
		k.index.Invalidate()
	}
	return migrated, nil
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
