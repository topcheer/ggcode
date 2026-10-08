package agent

// sa-109 PARTIAL: Harness Tuning tier A - a component-level self-tuning
// loop (propose -> acceptance gate -> budget -> persist) for guidance
// detector tiers. It closes the gap between guidance_stale.go's
// deliberately visibility-only stale analysis and detector_sampling.go's
// compile-time tier constants: a twice-confirmed stale tag whose delivered
// history shows NO post-delivery error-rate rise may be overridden to a
// lower tier at runtime, persisted to ~/.ggcode/harness-overrides.json
// (atomic temp+rename), and rolled back by deleting that file. Controlled
// landing of the Self-Harness propose-evaluate-accept loop
// (arXiv:2606.09498); budget and re-tune lock modeled on RRSI guardrails.
//
// guidance_stale.go stays read-only; this file only consumes its output
// (staleHeuristicSummary). Tool-description overlays (tool_usage_hints.go)
// are a separate mechanism and untouched.

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
)

const (
	// tuningDeliveredWindow bounds the "recent" delivered runs judged by
	// the acceptance gate; delivered evidence before the window is the
	// baseline pool.
	tuningDeliveredWindow = 20
	// tuningFollowupRuns is how many run records after a delivered run are
	// inspected for error/fail-signature records.
	tuningFollowupRuns = 3
	// tuningMinFollowups: fewer post-delivery records than this means "not
	// enough data" and the gate refuses (conservative).
	tuningMinFollowups = 5
	// tuningDefaultBaseline is the error-share threshold when no baseline
	// pool exists yet.
	tuningDefaultBaseline = 0.3
	// tuningMaxOverrides caps total overrides per model (RRSI-style budget).
	tuningMaxOverrides = 3
	// tuningRelockRuns forbids re-lowering the same tag's tier within this
	// many runs after the last applied override.
	tuningRelockRuns = 20
	// tuningConfirmations requires a stale tag to be seen on this many
	// separate tuning passes before an override may be applied.
	tuningConfirmations = 2
	// tuningSuppressTier is the lowest tier (suppress entirely).
	tuningSuppressTier = 0
)

// tierOverride pins a guidance tag to a detector tier until rolled back.
type tierOverride struct {
	Tier      int       `json:"tier"`
	Version   int       `json:"version"`
	Model     string    `json:"model,omitempty"`
	AppliedAt time.Time `json:"applied_at"`
}

// harnessOverride maps guidance tag -> pinned tier.
type harnessOverride map[string]tierOverride

var (
	harnessMu        sync.Mutex
	harnessOverrides harnessOverride    // nil = not yet loaded this process
	harnessConfirmed = map[string]int{} // in-process confirmation counts
	harnessPathFn    = defaultHarnessOverridesPath
)

func defaultHarnessOverridesPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".ggcode", "harness-overrides.json")
}

// loadHarnessOverrides reads the override store. Missing file -> empty map.
// Corrupt JSON -> backup to .bak and start fresh (a tuning store must never
// brick guidance delivery).
func loadHarnessOverrides(path string) harnessOverride {
	o := harnessOverride{}
	b, err := os.ReadFile(path)
	if err != nil {
		return o // missing or unreadable: empty, non-fatal
	}
	if err := json.Unmarshal(b, &o); err != nil {
		debug.Log("harness-tuning", "corrupt override store %s (%v), backed up", path, err)
		_ = os.Rename(path, path+".bak")
		return harnessOverride{}
	}
	if o == nil {
		o = harnessOverride{}
	}
	return o
}

// storeHarnessOverrides persists atomically: temp file in the same dir,
// then rename over the target.
func storeHarnessOverrides(path string, o harnessOverride) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(o, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "harness-overrides-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Chmod(tmpName, 0644); err != nil {
		os.Remove(tmpName)
		return err
	}
	return os.Rename(tmpName, path)
}

// tuningRun collapses the records sharing one flush timestamp (one agent
// run) into the facts the acceptance gate needs: did the target tag get
// delivered in this run, and does the run carry an error/fail-signature
// record (run-failure proxy: any record whose tag mentions error/fail).
type tuningRun struct {
	deliv   bool
	errMark bool
}

func tuningRunsFor(tag string, recs []guidanceStatRecord) []tuningRun {
	var runs []tuningRun
	idx := map[string]int{}
	for _, r := range recs {
		if r.Type == "stale_heuristic" || r.TS == "" {
			continue // report lines are not run events
		}
		i, ok := idx[r.TS]
		if !ok {
			i = len(runs)
			idx[r.TS] = i
			runs = append(runs, tuningRun{})
		}
		lower := strings.ToLower(r.Tag)
		if strings.Contains(lower, "error") || strings.Contains(lower, "fail") {
			runs[i].errMark = true
		}
		if r.Tag == tag && r.Delivered > 0 {
			runs[i].deliv = true
		}
	}
	return runs
}

// followupError returns the share of delivered runs whose next
// tuningFollowupRuns runs contain an error-marked record, plus the total
// number of followup run records behind that share.
func followupError(runs []tuningRun, delivered []int) (float64, int) {
	if len(delivered) == 0 {
		return 0, 0
	}
	hits, total := 0, 0
	for _, i := range delivered {
		hit := false
		for j := i + 1; j < len(runs) && j <= i+tuningFollowupRuns; j++ {
			total++
			if runs[j].errMark {
				hit = true
			}
		}
		if hit {
			hits++
		}
	}
	return float64(hits) / float64(len(delivered)), total
}

// proposeTierChange is the acceptance gate: the tag may be lowered only if,
// over its most recent tuningDeliveredWindow delivered runs, the
// post-delivery error share did not RISE versus the earlier-delivered
// baseline (default threshold when no baseline exists). Fewer than
// tuningMinFollowups followup records means insufficient evidence -> refuse.
// Delivered evidence is intentionally cross-model: a tag stale for the
// current model was by definition delivered by OTHER models, and that
// history is exactly the safety evidence we have.
func proposeTierChange(tag string, stats []guidanceStatRecord) bool {
	runs := tuningRunsFor(tag, stats)
	var delivered []int
	for i, r := range runs {
		if r.deliv {
			delivered = append(delivered, i)
		}
	}
	if len(delivered) == 0 {
		return false
	}
	var recent, earlier []int
	if len(delivered) > tuningDeliveredWindow {
		recent = delivered[len(delivered)-tuningDeliveredWindow:]
		earlier = delivered[:len(delivered)-tuningDeliveredWindow]
	} else {
		recent = delivered
	}
	ratio, total := followupError(runs, recent)
	if total < tuningMinFollowups {
		return false
	}
	baseline := tuningDefaultBaseline
	if b, n := followupError(runs, earlier); n >= tuningMinFollowups {
		baseline = b
	}
	return ratio <= baseline+1e-9
}

// runsSinceOverride counts distinct run timestamps recorded after an
// override was applied (for the re-downgrade lock).
func runsSinceOverride(appliedAt time.Time, recs []guidanceStatRecord) int {
	seen := map[string]bool{}
	n := 0
	for _, r := range recs {
		if r.Type == "stale_heuristic" || r.TS == "" || seen[r.TS] {
			continue
		}
		seen[r.TS] = true
		if t, err := time.Parse(time.RFC3339, r.TS); err == nil && t.After(appliedAt) {
			n++
		}
	}
	return n
}

// tuningModelFor attributes overrides to a model: prefer the model of the
// most recent stale_heuristic report, else the last run record's model.
func tuningModelFor(recs []guidanceStatRecord) string {
	model := ""
	for i := len(recs) - 1; i >= 0; i-- {
		if recs[i].Model != "" {
			model = recs[i].Model
			if recs[i].Type == "stale_heuristic" {
				break
			}
		}
	}
	return model
}

func tuningCountModel(o harnessOverride, model string) int {
	n := 0
	for _, ov := range o {
		if ov.Model == model {
			n++
		}
	}
	return n
}

// MaybeTuneHarness is the sa-109 tuning entry point, wired from
// flushGuidanceStats (guidance_stats.go) right after analyzeStaleGuidance:
// staleTags is staleHeuristicSummary(path, model). For each stale tag it
// double-confirms across tuning passes, runs the acceptance gate, enforces
// the per-model budget and re-downgrade lock, then persists the override.
// Best-effort and fully rolled back by deleting the override file.
func MaybeTuneHarness(staleTags []string, statsPath string) error {
	if len(staleTags) == 0 {
		return nil
	}
	recs := readGuidanceStatTail(statsPath)
	if len(recs) == 0 {
		return nil
	}
	model := tuningModelFor(recs)
	if model == "" {
		return nil
	}
	overridesPath := harnessPathFn()
	if overridesPath == "" {
		return nil
	}
	harnessMu.Lock()
	defer harnessMu.Unlock()
	cur := loadHarnessOverrides(overridesPath)
	changed := false
	for _, tag := range staleTags {
		if tag == "" {
			continue
		}
		if ov, ok := cur[tag]; ok {
			// Re-downgrade lock (RRSI): never lower the same tag again within
			// tuningRelockRuns runs of the last applied override.
			if ov.Tier <= tuningSuppressTier {
				continue
			}
			if runsSinceOverride(ov.AppliedAt, recs) < tuningRelockRuns {
				continue
			}
		}
		harnessConfirmed[tag]++
		if harnessConfirmed[tag] < tuningConfirmations {
			continue
		}
		if !proposeTierChange(tag, recs) {
			continue
		}
		if tuningCountModel(cur, model) >= tuningMaxOverrides {
			continue // per-model budget exhausted
		}
		cur[tag] = tierOverride{Tier: tuningSuppressTier, Version: 1, Model: model, AppliedAt: time.Now().UTC()}
		changed = true
		debug.Log("harness-tuning", "override accepted: %q -> tier %d (model %s)", tag, tuningSuppressTier, model)
	}
	if changed {
		if err := storeHarnessOverrides(overridesPath, cur); err != nil {
			return err
		}
		harnessOverrides = cur // refresh process cache
	}
	return nil
}

// harnessOverrideSuppresses is the single-tag form of ApplyOverridesToAllow
// for the injectGuidance hot path: true when the tuning store pins tag to
// tier <= 0 (suppress).
func harnessOverrideSuppresses(tag string) bool {
	return !ApplyOverridesToAllow(func(string) bool { return true })(tag)
}

// ApplyOverridesToAllow wraps a tag-allow predicate with the override store:
// a tag pinned to tier <= 0 (suppress) is denied regardless of the wrapped
// allow. The store is loaded lazily and cached per process; deleting the
// override file rolls back on the next process start.
func ApplyOverridesToAllow(allow func(tag string) bool) func(tag string) bool {
	return func(tag string) bool {
		if path := harnessPathFn(); path != "" {
			harnessMu.Lock()
			if harnessOverrides == nil {
				harnessOverrides = loadHarnessOverrides(path)
			}
			ov, ok := harnessOverrides[tag]
			harnessMu.Unlock()
			if ok && ov.Tier <= tuningSuppressTier {
				return false
			}
		}
		if allow == nil {
			return true
		}
		return allow(tag)
	}
}

// SetHarnessSuppressed (r16) is the manual half of the
// observability→controllability loop: /guidance suppress <tag> lands here.
// The sa-109 auto channel only reacts to double-confirmed stale tags;
// until now a user who drilled into a misfiring detector via
// /guidance <tag> had no action channel other than hand-editing
// harness-overrides.json and restarting. Mirrors the MaybeTuneHarness
// write pattern (lock, load, mutate, atomic store, refresh process
// cache). Model is pinned to "manual" so manual entries never count
// against the per-model auto budget (tuningCountModel matches real
// model names) and remain distinguishable in the JSONL provenance.
func SetHarnessSuppressed(tag string) error {
	if tag == "" {
		return errors.New("harness override: empty tag")
	}
	path := harnessPathFn()
	if path == "" {
		return errors.New("harness override: store path unavailable")
	}
	harnessMu.Lock()
	defer harnessMu.Unlock()
	cur := loadHarnessOverrides(path)
	cur[tag] = tierOverride{Tier: tuningSuppressTier, Version: 1, Model: "manual", AppliedAt: time.Now().UTC()}
	if err := storeHarnessOverrides(path, cur); err != nil {
		return err
	}
	harnessOverrides = cur
	debug.Log("harness-tuning", "manual suppress: %q", tag)
	return nil
}

// ClearHarnessOverride (r16) removes one tag's override (/guidance reset
// <tag>), restoring detector default behavior immediately (the process
// cache is refreshed in the same critical section). Returns false when no
// override existed for the tag - a no-op the TUI surfaces instead of
// pretending a reset happened.
func ClearHarnessOverride(tag string) (bool, error) {
	if tag == "" {
		return false, errors.New("harness override: empty tag")
	}
	path := harnessPathFn()
	if path == "" {
		return false, errors.New("harness override: store path unavailable")
	}
	harnessMu.Lock()
	defer harnessMu.Unlock()
	cur := loadHarnessOverrides(path)
	if _, ok := cur[tag]; !ok {
		return false, nil
	}
	delete(cur, tag)
	if err := storeHarnessOverrides(path, cur); err != nil {
		return false, err
	}
	harnessOverrides = cur
	debug.Log("harness-tuning", "manual reset: %q", tag)
	return true, nil
}
