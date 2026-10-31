// Package agentruntime: best-of-N ensemble admission ledger (sa-64).
//
// Research basis: "The Generator-Verifier Gap: Predicting When Multi-Agent
// LLM Architectures Beat a Compute-Matched Single Agent" (Shahi & Chaudhary,
// MIT, 2026) - ensemble benefit decomposes as
//
//	A(k) = p1 + eta * (C(k) - p1)
//
// where C(k) is coverage headroom (how often ANY of the k candidates
// succeeds) and eta is selection efficiency (how often the consensus winner
// is actually a success). Their measurement study found 47% of multi-agent
// configurations are NET-NEGATIVE under a token-matched baseline: users pay
// k times the tokens for less than nothing. Their actionable contribution is
// cheap diagnostics that predict held-out lift (R^2=0.84) BEFORE building
// the ensemble - a build/don't-build rule.
//
// ggcode's best_of_n already HAS the ensembles (heterogeneous candidates,
// cross-family verifiers, shared-error sentinel) but every run is judged in
// isolation: BestOfNReport is consumed by the caller and discarded. Nothing
// remembers, across runs of a recurring task family, whether the ensemble
// ever pays for itself. This ledger is that memory: per task fingerprint it
// keeps a rolling window of (successes/N, winner-was-usable, degraded) and
// answers the paper's admission question before the next run spends k times
// the budget again - advisory only, mirroring the budget-guard style.
package agentruntime

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/debug"
)

const (
	// bonLedgerWindow is the rolling per-task-family window size (recent
	// runs only; old runs age out so a task family that once looked
	// hopeless can re-qualify after tooling/model changes).
	bonLedgerWindow = 10
	// bonLedgerMinSamples is the minimum records before the ledger will
	// voice an admission hint (below this the statistics are noise).
	bonLedgerMinSamples = 5
	// bonHeadroomWarn: mean successes/N below this means the candidate
	// pool has almost no coverage headroom - ensemble gain is bounded by
	// headroom, so best_of_n cannot beat a single focused agent here.
	bonHeadroomWarn = 0.2
	// bonEtaWarn: fraction of any-success runs that still failed to pick
	// a usable winner below this means selection is the bottleneck.
	bonEtaWarn = 0.6
	// bonDegradedWarn: degraded (no-consensus) rate above this, combined
	// with weak eta, indicts the selector rather than the pool.
	bonDegradedWarn = 0.4
	// bonFingerprintClip bounds the hashed task text so trivially
	// different suffixes (timestamps in a task body) do not shard one
	// recurring task into unrelated families.
	bonFingerprintClip = 512
)

// bonRunRecord is one completed best-of-N run's admission-relevant outcome.
type bonRunRecord struct {
	N            int       `json:"n"`
	Successes    int       `json:"successes"` // candidates whose process completed AND verdict succeeded (#4136: matches the winner-gate verdict basis, not the process-status basis)
	WinnerUsable bool      `json:"winner_usable"`
	Degraded     bool      `json:"degraded"`
	At           time.Time `json:"at"`
}

// BONGapLedger keeps a rolling window of best-of-N outcomes per task
// fingerprint and answers ensemble-admission questions from it. Safe for
// concurrent use. A ledger with an empty path is memory-only (tests).
type BONGapLedger struct {
	mu     sync.Mutex
	path   string
	byTask map[string][]bonRunRecord
}

// NewBONGapLedger opens (or creates) the ledger at path. path == "" gives a
// memory-only ledger. A corrupt file is quarantined by ignoring it (fresh
// start) rather than failing the caller - the ledger is advisory state.
func NewBONGapLedger(path string) *BONGapLedger {
	l := &BONGapLedger{path: path, byTask: make(map[string][]bonRunRecord)}
	if path == "" {
		return l
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return l // absent file: normal first run
	}
	var m map[string][]bonRunRecord
	if err := json.Unmarshal(data, &m); err != nil {
		debug.Log("agentruntime", "bon gap ledger: corrupt state %s ignored: %v", path, err)
		return l
	}
	if len(m) > 0 {
		l.byTask = m
	}
	return l
}

// bonTaskFingerprint bins tasks into families: FNV-1a over the clipped,
// whitespace-normalized task text. Recurring cron/patrol tasks repeat
// verbatim and bin exactly; one-off tasks get their own (never-revisited)
// bin which simply never reaches the minimum sample count.
func bonTaskFingerprint(task string) string {
	t := strings.Join(strings.Fields(clip(task, bonFingerprintClip)), " ")
	h := fnv.New64a()
	h.Write([]byte(strings.ToLower(t)))
	return fmt.Sprintf("%016x", h.Sum64())
}

// Record ingests one completed run. Runs that never reached a full verdict
// (caller-context expiry, launch abort, validation error) are skipped -
// they carry no admission signal.
func (l *BONGapLedger) Record(task string, rep BestOfNReport) {
	if l == nil || rep.Partial || rep.Err != "" || rep.N <= 0 {
		return
	}
	successes := 0
	for _, c := range rep.Candidates {
		// #4136: admission headroom must measure task success, not process
		// survival. Status=="completed" only says the subagent didn't crash;
		// completed-but-verdict-failed is the most common outcome on hard
		// task families - exactly when the admission hint must speak up.
		// Align with the winner gate's verdict basis so both admission
		// signals measure the same thing.
		if c.Status == "completed" && c.Verdict == "succeeded" {
			successes++
		}
	}
	rec := bonRunRecord{
		N:            rep.N,
		Successes:    successes,
		WinnerUsable: rep.WinnerIndex >= 0,
		Degraded:     rep.Degraded,
		At:           time.Now(),
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	key := bonTaskFingerprint(task)
	w := append(l.byTask[key], rec)
	if len(w) > bonLedgerWindow {
		w = w[len(w)-bonLedgerWindow:]
	}
	l.byTask[key] = w
	l.persistLocked()
}

// persistLocked writes the ledger state best-effort; failures are logged
// and swallowed (advisory state must never break a run).
func (l *BONGapLedger) persistLocked() {
	if l.path == "" {
		return
	}
	data, err := json.MarshalIndent(l.byTask, "", " ")
	if err != nil {
		debug.Log("agentruntime", "bon gap ledger: marshal: %v", err)
		return
	}
	if err := os.MkdirAll(filepath.Dir(l.path), 0o755); err != nil {
		debug.Log("agentruntime", "bon gap ledger: mkdir: %v", err)
		return
	}
	if err := os.WriteFile(l.path, data, 0o644); err != nil {
		debug.Log("agentruntime", "bon gap ledger: write %s: %v", l.path, err)
	}
}

// AdmissionHint answers the generator-verifier-gap build/don't-build
// question for a recurring task family, using the rolling window:
//
//   - mean headroom (successes/N) < bonHeadroomWarn: the pool almost never
//     succeeds - ensemble gain is bounded by headroom, spend the k-times
//     budget on one focused agent + sequential retry instead.
//   - eta < bonEtaWarn AND degraded rate > bonDegradedWarn: successes
//     exist but the selector keeps failing to pick them - configure
//     cross-family verifier_models or drop the ensemble.
//
// Returns ("", false) when there is not enough evidence to speak.
func (l *BONGapLedger) AdmissionHint(task string) (string, bool) {
	if l == nil {
		return "", false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	w := l.byTask[bonTaskFingerprint(task)]
	if len(w) < bonLedgerMinSamples {
		return "", false
	}
	var (
		headroomSum float64
		anySuccess  int
		usable      int
		degraded    int
	)
	for _, r := range w {
		headroomSum += float64(r.Successes) / float64(r.N)
		if r.Successes > 0 {
			anySuccess++
			if r.WinnerUsable {
				usable++
			}
		}
		if r.Degraded {
			degraded++
		}
	}
	runs := float64(len(w))
	meanHeadroom := headroomSum / runs
	if meanHeadroom < bonHeadroomWarn {
		return fmt.Sprintf("ensemble admission: last %d runs of this task family averaged only %.0f%% candidate success (coverage headroom low) - ensemble gain is bounded by headroom, a single focused agent + sequential retry likely spends the budget better than %d parallel candidates.", len(w), meanHeadroom*100, w[len(w)-1].N), true
	}
	if anySuccess > 0 {
		eta := float64(usable) / float64(anySuccess)
		degradedRate := float64(degraded) / runs
		if eta < bonEtaWarn && degradedRate > bonDegradedWarn {
			return fmt.Sprintf("ensemble admission: last %d runs - selector picked a usable winner in only %.0f%% of runs that had successes (eta low) and %.0f%% degraded with no consensus; configure verifier_models with a cross-family model or reconsider the ensemble.", len(w), eta*100, degradedRate*100), true
		}
	}
	return "", false
}

// --- package-level default ---------------------------------------------

var (
	bonLedgerOnce   sync.Once
	bonLedgerShared *BONGapLedger
)

// DefaultBONGapLedger lazily opens the process-wide ledger under
// ~/.ggcode/bon-gap-ledger.json. It returns nil while running under `go
// test` so unit tests never touch real user state.
func DefaultBONGapLedger() *BONGapLedger {
	if testing.Testing() {
		return nil
	}
	bonLedgerOnce.Do(func() {
		bonLedgerShared = NewBONGapLedger(filepath.Join(config.HomeDir(), ".ggcode", "bon-gap-ledger.json"))
	})
	return bonLedgerShared
}

// RecordBestOfNGap records a run into the default ledger (nil-safe no-op).
func RecordBestOfNGap(task string, rep BestOfNReport) {
	DefaultBONGapLedger().Record(task, rep)
}

// BestOfNAdmissionHint consults the default ledger (nil-safe no-op).
func BestOfNAdmissionHint(task string) (string, bool) {
	return DefaultBONGapLedger().AdmissionHint(task)
}
