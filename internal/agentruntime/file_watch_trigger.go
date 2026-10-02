package agentruntime

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/safego"
)

// FileWatchTrigger implements ambient, event-driven agent firing (r372):
// watch user-configured glob patterns and enqueue a prompt when a change
// settles, instead of waiting for a human to ask. This is the
// drift-detection trigger pattern from the 2026 ambient-agents literature
// (fire on change, not on schedule) layered on the same enqueue channel
// cron already uses, so routing, busy handling and persistence semantics
// stay in one place.
//
// Polling, not fsnotify: the repo convention (mcp_hotreload.go, config
// hot reload) is dependency-free polling - identical behavior on
// macOS/Linux/Windows (#574 lesson) at the cost of a 2s detection window.
//
// Debounce: a change set only fires after it is observed IDENTICAL on two
// consecutive ticks (editor temp-file+rename storms coalesce), and each
// trigger has a cooldown (default 10s) so save-storms cannot spam the
// agent. Globs use filepath.Glob semantics (no ** recursion; list
// directory-scoped patterns instead).
type FileWatchTrigger struct {
	triggers   []config.WatchTriggerConfig
	workingDir string
	emit       func(prompt string, queueIfBusy bool)

	interval time.Duration
	cooldown time.Duration

	mu      sync.Mutex
	stopped bool
	// snapshots[i] is the per-trigger baseline: path -> file fingerprint.
	snapshots []map[string]fileFingerprint
	// pending[i] is the change set seen on the previous tick, awaiting a
	// stable (identical) second tick before firing.
	pending []map[string]bool
	// primed[i]: the first poll only establishes the baseline - files
	// existing at startup are NOT a change (else every ggcode start with
	// a watch: section would fire all triggers once).
	primed []bool
	// cooldownUntil[i] blocks re-firing until the wall clock passes it.
	cooldownUntil []time.Time
}

type fileFingerprint struct {
	mtime time.Time
	size  int64
}

// DefaultWatchInterval / DefaultWatchCooldown match the polling
// convention (2s) and a conservative re-fire cadence.
const (
	DefaultWatchInterval = 2 * time.Second
	DefaultWatchCooldown = 10 * time.Second
	maxFilesInPrompt     = 10
)

// NewFileWatchTrigger creates a watcher for the configured triggers. emit
// may be nil-safe through the cron scheduler's no-op default; firing
// before an enqueue is wired is dropped with a debug log.
func NewFileWatchTrigger(triggers []config.WatchTriggerConfig, workingDir string, emit func(prompt string, queueIfBusy bool)) *FileWatchTrigger {
	t := &FileWatchTrigger{
		triggers:      triggers,
		workingDir:    workingDir,
		emit:          emit,
		interval:      DefaultWatchInterval,
		snapshots:     make([]map[string]fileFingerprint, len(triggers)),
		pending:       make([]map[string]bool, len(triggers)),
		cooldownUntil: make([]time.Time, len(triggers)),
		primed:        make([]bool, len(triggers)),
	}
	for i := range t.snapshots {
		t.snapshots[i] = make(map[string]fileFingerprint)
	}
	return t
}

// Start launches the polling loop in the background. Safe to call once;
// a nil-receiver or empty trigger list is a no-op (zero behavior change
// for users without a watch: config section).
func (t *FileWatchTrigger) Start() {
	if t == nil || len(t.triggers) == 0 {
		return
	}
	safego.Go("file-watch-trigger", func() {
		tick := time.NewTicker(t.interval)
		defer tick.Stop()
		for {
			t.mu.Lock()
			if t.stopped {
				t.mu.Unlock()
				return
			}
			t.mu.Unlock()
			t.poll()
			<-tick.C
		}
	})
}

// Stop terminates the polling loop and waits for it to observe the flag.
func (t *FileWatchTrigger) Stop() {
	if t == nil {
		return
	}
	t.mu.Lock()
	t.stopped = true
	t.mu.Unlock()
}

func (t *FileWatchTrigger) poll() {
	now := time.Now()
	for i, trig := range t.triggers {
		current := t.scan(trig)
		t.mu.Lock()
		if !t.primed[i] {
			t.primed[i] = true
			t.snapshots[i] = current
			t.mu.Unlock()
			continue
		}
		baseline := t.snapshots[i]
		changed := diffChanged(baseline, current)
		pending := t.pending[i]
		switch {
		case len(changed) == 0:
			t.pending[i] = nil
		case pending != nil && sameSet(pending, changed):
			// Stable across two ticks: fire. The baseline is only advanced
			// HERE - refreshing it in the pending branch would erase the
			// pending change from the next diff and the fire would never
			// happen.
			t.pending[i] = nil
			t.snapshots[i] = current
			if !now.Before(t.cooldownUntil[i]) {
				cd := trig.CooldownSec
				if cd <= 0 {
					cd = int(DefaultWatchCooldown / time.Second)
				}
				t.cooldownUntil[i] = now.Add(time.Duration(cd) * time.Second)
				t.mu.Unlock()
				t.fire(trig, changed)
				t.mu.Lock()
			} else {
				debug.Log("watch", "[watch] trigger %d change settled but cooldown active, dropping", i)
			}
		default:
			// Storm still in flight: remember the latest change set and wait
			// for a quiet (identical) tick. Baseline deliberately NOT touched.
			t.pending[i] = changed
		}
		t.mu.Unlock()
	}
}

// scan expands a trigger's globs relative to the working directory and
// fingerprints every match. Unreadable files are skipped (treated as
// unchanged) - watching must never crash the runtime.
func (t *FileWatchTrigger) scan(trig config.WatchTriggerConfig) map[string]fileFingerprint {
	out := make(map[string]fileFingerprint)
	for _, pattern := range trig.Globs {
		var matches []string
		if filepath.IsAbs(pattern) {
			m, err := filepath.Glob(pattern)
			if err != nil {
				continue
			}
			matches = m
		} else {
			m, err := filepath.Glob(filepath.Join(t.workingDir, pattern))
			if err != nil {
				continue
			}
			matches = m
		}
		for _, p := range matches {
			fi, err := os.Stat(p)
			if err != nil || fi.IsDir() {
				continue
			}
			out[p] = fileFingerprint{mtime: fi.ModTime(), size: fi.Size()}
		}
	}
	return out
}

func (t *FileWatchTrigger) fire(trig config.WatchTriggerConfig, changed map[string]bool) {
	if t.emit == nil {
		debug.Log("watch", "[watch] change settled but no enqueue wired yet, dropping: %.60s", trig.Prompt)
		return
	}
	files := make([]string, 0, len(changed))
	for p := range changed {
		files = append(files, p)
	}
	sort.Strings(files)
	if len(files) > maxFilesInPrompt {
		files = append(files[:maxFilesInPrompt], fmt.Sprintf("... and %d more", len(changed)-maxFilesInPrompt))
	}
	prompt := trig.Prompt
	if strings.Contains(prompt, "{files}") {
		prompt = strings.ReplaceAll(prompt, "{files}", strings.Join(files, ", "))
	} else {
		prompt = prompt + "\n\nChanged files: " + strings.Join(files, ", ")
	}
	debug.Log("watch", "[watch] firing prompt (queue_if_busy=%v): %.80s", trig.QueueIfBusy, prompt)
	t.emit(prompt, trig.QueueIfBusy)
}

// diffChanged returns paths whose fingerprint differs or that are new.
func diffChanged(baseline, current map[string]fileFingerprint) map[string]bool {
	changed := make(map[string]bool)
	for p, fp := range current {
		if old, ok := baseline[p]; !ok || old != fp {
			changed[p] = true
		}
	}
	return changed
}

func sameSet(a, b map[string]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if !b[k] {
			return false
		}
	}
	return true
}
