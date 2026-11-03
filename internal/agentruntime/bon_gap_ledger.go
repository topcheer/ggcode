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
	bonLedgerWindow     = 10
	bonLedgerMinSamples = 5
	bonHeadroomWarn     = 0.2
	bonEtaWarn          = 0.6
	bonDegradedWarn     = 0.4
	bonFingerprintClip  = 512
)

type bonRunRecord struct {
	N            int       `json:"n"`
	Successes    int       `json:"successes"`
	WinnerUsable bool      `json:"winner_usable"`
	Degraded     bool      `json:"degraded"`
	At           time.Time `json:"at"`
}

type BONGapLedger struct {
	mu     sync.Mutex
	path   string
	byTask map[string][]bonRunRecord
}

func NewBONGapLedger(path string) *BONGapLedger {
	l := &BONGapLedger{path: path, byTask: make(map[string][]bonRunRecord)}
	if path == "" {
		return l
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return l
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

func bonTaskFingerprint(task string) string {
	t := strings.Join(strings.Fields(clip(task, bonFingerprintClip)), " ")
	h := fnv.New64a()
	h.Write([]byte(strings.ToLower(t)))
	return fmt.Sprintf("%016x", h.Sum64())
}

func (l *BONGapLedger) Record(task string, rep BestOfNReport) {
	if l == nil || rep.Partial || rep.Err != "" || rep.N <= 0 {
		return
	}
	successes := 0
	for _, c := range rep.Candidates {

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

var (
	bonLedgerOnce   sync.Once
	bonLedgerShared *BONGapLedger
)

func DefaultBONGapLedger() *BONGapLedger {
	if testing.Testing() {
		return nil
	}
	bonLedgerOnce.Do(func() {
		bonLedgerShared = NewBONGapLedger(filepath.Join(config.HomeDir(), ".ggcode", "bon-gap-ledger.json"))
	})
	return bonLedgerShared
}

func RecordBestOfNGap(task string, rep BestOfNReport) {
	DefaultBONGapLedger().Record(task, rep)
}

func BestOfNAdmissionHint(task string) (string, bool) {
	return DefaultBONGapLedger().AdmissionHint(task)
}
