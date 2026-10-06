package agent

// r22: stale-heuristic detection (harness assumption expiry). Anthropic's
// Managed Agents post (2026-04-08, "Scaling Managed Agents") documents the
// pattern: a harness workaround for one model's behavior ("context anxiety"
// resets) becomes dead weight once a newer model drops the behavior. ggcode
// has ~100 detectors encoding model-behavior assumptions, and guidance-stats
// (r402) only records what FIRED - a detector that never fires on the new
// model is invisible to the data pipeline.
//
// This analyzer makes that expiry visible by cross-model comparison alone
// (no static registry to rot when a detector's heading changes): a tag that
// other models still trigger (delivered) but the current model has not
// delivered in its last N runs is a per-model dead weight. Reports are
// appended to the same JSONL as {"type":"stale_heuristic",...} entries and
// logged via debug.Log - deliberately visibility-only, no auto-disabling
// (same conservative philosophy as r402: "premature before misfire data
// exists").

import (
	"bufio"
	"encoding/json"
	"os"
	"sort"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

const (
	// staleMinRuns is the minimum per-model run count before a zero-delivery
	// verdict is trusted: a model tried twice says nothing.
	staleMinRuns = 10
	// staleTailBytes bounds how much of the JSONL is read per analysis:
	// ~400 lines of per-tag records, far beyond staleMinRuns runs.
	staleTailBytes = 64 * 1024
	// staleOtherModelDelivered is the cross-model contrast bar: the tag must
	// still be delivering for SOME other model (>=2) to call the current
	// model's silence a behavior change rather than a project trait (a docs
	// workspace never trips build-idempotency on ANY model).
	staleOtherModelDelivered = 2
)

// guidanceStatRecord is one JSONL line from flushGuidanceStats (plus this
// file's stale_heuristic report lines, discriminated by Type).
type guidanceStatRecord struct {
	Type       string `json:"type,omitempty"`
	TS         string `json:"ts"`
	Model      string `json:"model,omitempty"`
	Tag        string `json:"tag"`
	Delivered  int    `json:"delivered"`
	Suppressed int    `json:"suppressed"`
}

// analyzeStaleGuidance scans the tail of guidance-stats.jsonl and appends one
// stale_heuristic report line per newly-detected dead-weight tag. Called from
// flushGuidanceStats after the run's own records land, so the fresh run
// participates in the verdict. Best-effort: any read/parse failure is a
// silent no-op (observability must never break a run).
func analyzeStaleGuidance(path, currentModel string) {
	if currentModel == "" {
		return
	}
	recs := readGuidanceStatTail(path)
	if len(recs) == 0 {
		return
	}

	// runs[model] = ordered distinct run timestamps (a run's tag records
	// share the flush timestamp). tagStat[(model,tag)] aggregates delivery.
	runSet := map[string]map[string]bool{}
	type tagKey struct{ model, tag string }
	agg := map[tagKey]*guidanceTagStat{}
	reported := map[tagKey]bool{} // dedup vs earlier stale_heuristic lines
	for _, r := range recs {
		if r.Type == "stale_heuristic" {
			reported[tagKey{r.Model, r.Tag}] = true
			continue
		}
		if r.Tag == "" || r.Model == "" {
			// Pre-model-field legacy records cannot anchor a cross-model
			// contrast (they may BE the current model's own history), so
			// they are parsed for forward-compat but skipped entirely.
			continue
		}
		if runSet[r.Model] == nil {
			runSet[r.Model] = map[string]bool{}
		}
		runSet[r.Model][r.TS] = true
		k := tagKey{r.Model, r.Tag}
		st := agg[k]
		if st == nil {
			st = &guidanceTagStat{}
			agg[k] = st
		}
		st.Delivered += r.Delivered
		st.Suppressed += r.Suppressed
	}

	// Per-model tag delivery, only for models with enough runs to judge.
	// #3459: the aggregate keeps Suppressed alongside Delivered - a tag with
	// delivered:0 but suppressed>0 is budget-starved, NOT stale (it is still
	// firing; guidance budget just suppresses delivery). Reporting it stale
	// would mislabel live detectors as dead weight.
	deliveredPerModel := map[string]map[string]*guidanceTagStat{}
	for k, st := range agg {
		if len(runSet[k.model]) >= staleMinRuns {
			if deliveredPerModel[k.model] == nil {
				deliveredPerModel[k.model] = map[string]*guidanceTagStat{}
			}
			deliveredPerModel[k.model][k.tag] = st
		}
	}

	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		debug.Log("guidance-stale", "open failed: %v", err)
		return
	}
	defer f.Close()

	for tag, zero := range deliveredPerModel[currentModel] {
		// #3459: suppressed-only tags are excluded from stale reporting
		// (budget starvation is not staleness).
		if zero.Delivered != 0 || zero.Suppressed > 0 || reported[tagKey{currentModel, tag}] {
			continue
		}
		others := 0
		for m, tags := range deliveredPerModel {
			if m != currentModel && tags[tag].Delivered >= staleOtherModelDelivered {
				others += tags[tag].Delivered
			}
		}
		if others == 0 {
			continue
		}
		b, err := json.Marshal(guidanceStatRecord{
			Type:  "stale_heuristic",
			TS:    recs[len(recs)-1].TS,
			Model: currentModel,
			Tag:   tag,
		})
		if err != nil {
			continue
		}
		f.Write(append(b, '\n'))
		debug.Log("guidance-stale", "stale heuristic on %s: %q never delivered in %d runs (other models delivered %d)",
			currentModel, tag, len(runSet[currentModel]), others)
	}
}

// readGuidanceStatTail reads at most staleTailBytes from the end of the
// JSONL, dropping the first (likely partial) line. Malformed lines are
// skipped, not fatal.
func readGuidanceStatTail(path string) []guidanceStatRecord {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	var start int64
	if fi, err := f.Stat(); err == nil && fi.Size() > staleTailBytes {
		start = fi.Size() - staleTailBytes
	}
	if _, err := f.Seek(start, 0); err != nil {
		return nil
	}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 64*1024)
	var recs []guidanceStatRecord
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || line[0] != '{' {
			continue
		}
		var r guidanceStatRecord
		if json.Unmarshal([]byte(line), &r) == nil && r.Tag != "" {
			recs = append(recs, r)
		}
	}
	return recs
}

// staleHeuristicSummary returns the tags reported as stale for a model, most
// recent last. Exposed for tooling/tests; reads the same JSONL.
func staleHeuristicSummary(path, model string) []string {
	recs := readGuidanceStatTail(path)
	var out []string
	for _, r := range recs {
		if r.Type == "stale_heuristic" && (model == "" || r.Model == model) {
			out = append(out, r.Tag)
		}
	}
	sort.Strings(out)
	return out
}
