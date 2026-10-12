package agent

// r402 P0: observability foundation for detector guidance (arXiv:2604.25850
// - Agentic Harness Engineering: "voluminous trajectories bury actionable
// signal"). ~100 detectors inject guidance via the single injectGuidance
// funnel, but NOTHING records which detectors fire, how often, or how much
// the budget suppresses - the data foundation for any data-driven tuning of
// detector tiers/thresholds literally did not exist (detector_sampling.go
// tiers are compile-time constants with zero historical feedback).
//
// Scope deliberately excludes: automatic threshold rewriting (premature
// before misfire data exists) and per-detector "adopted" attribution (a
// behavior-shift signal cannot be honestly measured yet). This file only
// makes firing visible, per run, appended to a project-local JSONL.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
)

const (
	// guidanceTagMaxRunes caps the tag at the first line of the guidance
	// text (detectors open with a stable title line) - enough to identify
	// the source detector without leaking prompt bytes into the log.
	guidanceTagMaxRunes = 48
	// guidanceStatsPath is project-local: detector firing profiles differ
	// per project (a docs workspace never trips build-idempotency).
	guidanceStatsPath = ".ggcode/memory/guidance-stats.jsonl"
	// guidanceHintsPath (r15) stores tag → last delivered hint text so
	// /guidance <tag> can drill from aggregate counts to the exact prose
	// injected at the model (introspection span-payload layer).
	guidanceHintsPath = ".ggcode/memory/guidance-hints.jsonl"
)

// guidanceHintRec is the drill-down payload for one detector tag.
type guidanceHintRec struct {
	TS   string `json:"ts"`
	Tag  string `json:"tag"`
	Text string `json:"text"`
}

type guidanceTagStat struct {
	Delivered  int `json:"delivered"`
	Suppressed int `json:"suppressed"`
}

// guidanceRunStats counts guidance injections per source tag for one run.
// Touched only from the agent run loop goroutine (same concurrency scope as
// guidanceBudget itself), so no internal locking.
type guidanceRunStats map[string]*guidanceTagStat

func (g guidanceRunStats) record(tag string, delivered bool) {
	st := g[tag]
	if st == nil {
		st = &guidanceTagStat{}
		g[tag] = st
	}
	if delivered {
		st.Delivered++
	} else {
		st.Suppressed++
	}
}

// guidanceTag derives a stable identity for a guidance message: the first
// line, stripped of markdown decoration. Detectors open with a stable
// heading ("## Attention Fragmentation", "ACT NOW:"), so first-line keys
// cluster repeated firings of the same detector.
func guidanceTag(text string) string {
	line := text
	if i := strings.IndexByte(line, '\n'); i >= 0 {
		line = line[:i]
	}
	line = strings.TrimSpace(line)
	line = strings.TrimLeft(line, "#=-* \t")
	r := []rune(line)
	if len(r) > guidanceTagMaxRunes {
		r = r[:guidanceTagMaxRunes]
	}
	return string(r)
}

// recordGuidanceHint (r15) remembers the most recent delivered text for a
// tag; called from the injectGuidance funnel alongside guidanceStats.record.
func (a *Agent) recordGuidanceHint(tag, text, ts string) {
	if tag == "" || text == "" {
		return
	}
	if a.guidanceHints == nil {
		a.guidanceHints = make(map[string]guidanceHintRec)
	}
	a.guidanceHints[tag] = guidanceHintRec{TS: ts, Tag: tag, Text: text}
}

// writeGuidanceHints rewrites guidance-hints.jsonl from the in-memory
// tag→text map. Rewrite (not append) semantics keep the file bounded:
// cardinality equals live tag count (tens), each tag keeps only its most
// recent delivered text — an append jsonl would grow unboundedly and
// duplicate near-identical template text every run.
func (a *Agent) writeGuidanceHints(workingDir string) {
	if len(a.guidanceHints) == 0 || workingDir == "" {
		return
	}
	path := filepath.Join(workingDir, filepath.FromSlash(guidanceHintsPath))
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		debug.Log("guidance-hints", "mkdir failed: %v", err)
		return
	}
	var b []byte
	for _, rec := range a.guidanceHints {
		line, err := json.Marshal(rec)
		if err != nil {
			continue
		}
		b = append(b, line...)
		b = append(b, '\n')
	}
	if err := os.WriteFile(path, b, 0644); err != nil {
		debug.Log("guidance-hints", "write failed: %v", err)
	}
}

// flushGuidanceStats appends one JSONL line per tag to the project's
// guidance-stats file. Append-only single-line writes keep multi-process
// safety cheap (POSIX O_APPEND small-write atomicity); volume is bounded by
// the per-run tag cardinality (tens at most). Silent no-op when nothing
// fired or the working dir is unknown.
func (a *Agent) flushGuidanceStats() {
	if len(a.guidanceStats) == 0 {
		return
	}
	workingDir := a.WorkingDir()
	if workingDir == "" {
		return
	}
	path := filepath.Join(workingDir, filepath.FromSlash(guidanceStatsPath))
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		debug.Log("guidance-stats", "mkdir failed: %v", err)
		return
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		debug.Log("guidance-stats", "open failed: %v", err)
		return
	}
	defer f.Close()
	ts := time.Now().UTC().Format(time.RFC3339)
	model := ""
	if m, ok := a.provider.(interface{ ModelName() string }); ok {
		model = m.ModelName()
	}
	for tag, st := range a.guidanceStats {
		rec := struct {
			TS         string `json:"ts"`
			Model      string `json:"model,omitempty"`
			Tag        string `json:"tag"`
			Delivered  int    `json:"delivered"`
			Suppressed int    `json:"suppressed"`
		}{ts, model, tag, st.Delivered, st.Suppressed}
		b, err := json.Marshal(rec)
		if err != nil {
			continue
		}
		f.Write(append(b, '\n'))
	}
	debug.Log("guidance-stats", "flushed %d tag(s) to %s", len(a.guidanceStats), path)
	// r15: tag→text drill-down sidecar (best-effort, same working dir).
	a.writeGuidanceHints(workingDir)
	// r22: harness-assumption expiry check - cross-model dead-weight
	// detection over the file we just flushed into. Best-effort.
	analyzeStaleGuidance(path, model)
	// sa-109 Tier A: Self-Harness-style component self-tuning. Stale tags
	// (double-confirmed) go through the acceptance gate + per-model budget;
	// passing tags get a suppress override in ~/.ggcode/harness-overrides.json
	// (delete the file to roll back). Best-effort.
	if err := MaybeTuneHarness(staleHeuristicSummary(path, model), path); err != nil {
		debug.Log("guidance-stats", "harness tuning skipped: %v", err)
	}
}
