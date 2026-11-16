package intentions

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/cron"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/safego"
)

const (
	// DefaultPollInterval mirrors FileWatchTrigger's DefaultWatchInterval.
	DefaultPollInterval = 2 * time.Second
	// DefaultCooldown mirrors FileWatchTrigger's DefaultWatchCooldown.
	DefaultCooldown = 10 * time.Second
)

type fileFingerprint struct {
	mtime time.Time
	size  int64
}

// Engine activates pending intentions (#5260 AC-2/AC-3). File and time cue
// families are evaluated on a poll loop with FileWatchTrigger semantics
// (prime-then-fire, per-intention cooldown); the event family is push-based
// via Observe. Firing surfaces the intention in-session through the injected
// emit path with full utterance provenance — the engine never acts
// autonomously.
type Engine struct {
	store      *Store
	workingDir string
	emit       func(prompt string, queueIfBusy bool)
	interval   time.Duration

	mu        sync.Mutex
	stopped   bool
	snapshots map[string]map[string]fileFingerprint // intention ID -> path -> fingerprint
	primed    map[string]bool
	nextFire  map[string]time.Time // time-family scheduled fires
}

// NewEngine builds an engine over store. emit enqueues a prompt into the
// owning session (use the session cron scheduler's Emit).
func NewEngine(store *Store, workingDir string, emit func(prompt string, queueIfBusy bool)) *Engine {
	return &Engine{
		store:      store,
		workingDir: workingDir,
		emit:       emit,
		interval:   DefaultPollInterval,
		snapshots:  make(map[string]map[string]fileFingerprint),
		primed:     make(map[string]bool),
		nextFire:   make(map[string]time.Time),
	}
}

// Start launches the poll loop (nil/empty-store safe: a nil engine or a
// store with no active file/time intentions does nothing).
func (e *Engine) Start() {
	if e == nil || e.store == nil {
		return
	}
	e.mu.Lock()
	if e.stopped {
		e.mu.Unlock()
		return
	}
	e.mu.Unlock()
	safego.Go("intentions-engine", func() {
		ticker := time.NewTicker(e.interval)
		defer ticker.Stop()
		for range ticker.C {
			e.mu.Lock()
			stopped := e.stopped
			e.mu.Unlock()
			if stopped {
				return
			}
			e.Poll()
		}
	})
}

// Stop terminates the poll loop. Safe on nil and double-stop.
func (e *Engine) Stop() {
	if e == nil {
		return
	}
	e.mu.Lock()
	e.stopped = true
	e.mu.Unlock()
}

// Poll runs one activation pass. Exported for tests and manual ticks.
func (e *Engine) Poll() {
	if e == nil || e.store == nil {
		return
	}
	now := time.Now()
	for _, in := range e.store.List(false) {
		if !in.Active() {
			continue
		}
		switch in.CueFamily {
		case CueFile:
			e.pollFile(in, now)
		case CueTime:
			e.pollTime(in, now)
		case CueEvent:
			// push-based: see Observe
		}
	}
}

// Observe feeds one event observation to event-family intentions (kind is
// a short surface tag such as "tool_result" or "im_message"; text is the
// observed content). Matching is case-insensitive substring on CuePattern.
func (e *Engine) Observe(kind, text string) {
	if e == nil || e.store == nil {
		return
	}
	hay := strings.ToLower(kind + ": " + text)
	for _, in := range e.store.List(false) {
		if !in.Active() || in.CueFamily != CueEvent {
			continue
		}
		if strings.Contains(hay, strings.ToLower(in.CuePattern)) {
			e.fire(in)
		}
	}
}

func (e *Engine) pollFile(in Intention, now time.Time) {
	matchSet := e.scan(in)
	e.mu.Lock()
	snap, hadSnap := e.snapshots[in.ID]
	if !hadSnap {
		snap = make(map[string]fileFingerprint)
		e.snapshots[in.ID] = snap
	}
	changed := false
	for path, fp := range matchSet {
		old, ok := snap[path]
		snap[path] = fp
		if ok && (old.mtime != fp.mtime || old.size != fp.size) {
			changed = true
		}
	}
	primed := e.primed[in.ID]
	if !primed {
		e.primed[in.ID] = true
		e.mu.Unlock()
		return // first sight: record baseline, never fire
	}
	e.mu.Unlock()
	if changed {
		e.fire(in)
	}
}

func (e *Engine) scan(in Intention) map[string]fileFingerprint {
	pattern := in.CuePattern
	if !filepath.IsAbs(pattern) && e.workingDir != "" {
		pattern = filepath.Join(e.workingDir, pattern)
	}
	matches, err := filepath.Glob(pattern)
	if err != nil {
		debug.Log("intentions", "scan: bad glob %q: %v", in.CuePattern, err)
		return nil
	}
	out := make(map[string]fileFingerprint, len(matches))
	for _, m := range matches {
		info, err := os.Stat(m)
		if err != nil {
			continue
		}
		out[m] = fileFingerprint{mtime: info.ModTime(), size: info.Size()}
	}
	return out
}

func (e *Engine) pollTime(in Intention, now time.Time) {
	e.mu.Lock()
	next, ok := e.nextFire[in.ID]
	e.mu.Unlock()
	if !ok {
		next2, err := cron.NextTime(in.CuePattern, now)
		if err != nil {
			debug.Log("intentions", "time: invalid cron expr %q on %s: %v", in.CuePattern, in.ID, err)
			return
		}
		e.mu.Lock()
		e.nextFire[in.ID] = next2
		e.mu.Unlock()
		return
	}
	if now.Before(next) {
		return
	}
	e.fire(in)
	e.mu.Lock()
	if n2, err := cron.NextTime(in.CuePattern, now); err == nil {
		e.nextFire[in.ID] = n2
	} else {
		delete(e.nextFire, in.ID)
	}
	e.mu.Unlock()
}

// fire applies the lifecycle transition and surfaces the intention
// in-session with provenance (AC-3: no silent autonomous action).
func (e *Engine) fire(in Intention) {
	if e.emit == nil {
		debug.Log("intentions", "fire: no emit path, intention %s not surfaced", in.ID)
	}
	var prompt string
	err := e.store.Mutate(in.ID, func(cur *Intention) error {
		// Re-check under the store's cross-process discipline: another seat
		// may have fired this intention between our List() and now.
		if !cur.Active() {
			return fmt.Errorf("already inactive")
		}
		if cur.Repeat && cur.CooldownUntil.After(time.Now()) {
			return fmt.Errorf("cooldown")
		}
		cur.FireCount++
		cur.LastFiredAt = time.Now().UTC()
		if cur.Repeat {
			cd := DefaultCooldown
			if cur.CooldownSec > 0 {
				cd = time.Duration(cur.CooldownSec) * time.Second
			}
			cur.CooldownUntil = time.Now().Add(cd)
		} else {
			cur.State = StateFired
		}
		prompt = firedPrompt(*cur)
		return nil
	})
	if err != nil {
		debug.Log("intentions", "fire: %s skipped: %v", in.ID, err)
		return
	}
	if e.emit != nil {
		e.emit(prompt, true)
	}
}

func firedPrompt(in Intention) string {
	return fmt.Sprintf("[intention %s] Condition met — deferred task due now.\n"+
		"Task: %s\n"+
		"Captured from (session %s, %s): %q\n"+
		"Act on it in this turn, then confirm via the intention tool (action=realize, id=%s) — or retire it if obsolete (action=retire, id=%s).",
		in.ID, in.Statement, in.OriginSession, in.CreatedAt.Format(time.RFC3339), in.OriginExcerpt, in.ID, in.ID)
}
