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

type FileWatchTrigger struct {
	triggers   []config.WatchTriggerConfig
	workingDir string
	emit       func(prompt string, queueIfBusy bool)

	interval time.Duration
	cooldown time.Duration

	mu            sync.Mutex
	stopped       bool
	snapshots     []map[string]fileFingerprint
	pending       []map[string]bool
	primed        []bool
	cooldownUntil []time.Time
}

type fileFingerprint struct {
	mtime time.Time
	size  int64
}

const (
	DefaultWatchInterval = 2 * time.Second
	DefaultWatchCooldown = 10 * time.Second
	maxFilesInPrompt     = 10
)

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

			if !now.Before(t.cooldownUntil[i]) {
				cd := trig.CooldownSec
				if cd <= 0 {
					cd = int(DefaultWatchCooldown / time.Second)
				}
				t.cooldownUntil[i] = now.Add(time.Duration(cd) * time.Second)
				t.pending[i] = nil
				t.snapshots[i] = current
				t.mu.Unlock()
				t.fire(trig, changed)
				t.mu.Lock()
			} else {
				debug.Log("watch", "[watch] trigger %d change settled but cooldown active, deferring until cooldown ends", i)
			}
		default:

			t.pending[i] = changed
		}
		t.mu.Unlock()
	}
}

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

func diffChanged(baseline, current map[string]fileFingerprint) map[string]bool {
	changed := make(map[string]bool)
	for p, fp := range current {
		if old, ok := baseline[p]; !ok || old != fp {
			changed[p] = true
		}
	}
	for p := range baseline {
		if _, ok := current[p]; !ok {
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
