// Package agentreliability maintains cross-session reliability profiles for
// external delegate agents (ACP CLI agents such as claude/codex/gemini).
//
// Research basis (2026 frontier):
//   - Epistemic Context Learning (2026): multi-agent systems build peer
//     reliability profiles from interaction history so the orchestrator learns
//     which peers to trust when uncertain.
//   - Learning to Recommend Multi-Agent Subgraphs from Calling Trees (2026):
//     historical call outcomes are the input for selecting the best agent per
//     subtask in multi-agent orchestration.
//   - Agentic Confidence Calibration (2026): agents should ground their
//     expectations of success in observed outcomes, not in vibes.
//
// Gap in this codebase: the delegate tool lets the model pick among several
// external agents, but nothing records how those agents actually performed in
// past delegations. Every delegate call is a blind pick. This package records
// each delegation outcome (completed / failed / cancelled) with duration, and
// renders compact summaries that are surfaced in delegate results and in
// list_agents output, so the next selection is evidence-based.
//
// Design constraints:
//   - zero LLM cost: pure bookkeeping, no inference
//   - best-effort persistence: a corrupt or unwritable store degrades to an
//     empty in-memory ledger and never blocks delegation
//   - bounded memory: at most maxRecordsPerAgent records per agent
package agentreliability

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/config"
)

// Outcome classifies how a delegated run ended.
type Outcome string

const (
	OutcomeCompleted Outcome = "completed"
	OutcomeFailed    Outcome = "failed"
	OutcomeCancelled Outcome = "cancelled"
)

// maxRecordsPerAgent bounds the per-agent ring buffer.
const maxRecordsPerAgent = 50

// recentWindow is how many most-recent records feed the summary stats.
const recentWindow = 10

// Record is one observed delegation outcome.
type Record struct {
	Outcome    Outcome   `json:"outcome"`
	StartedAt  time.Time `json:"started_at"`
	DurationMs int64     `json:"duration_ms"`
}

type storeFile struct {
	Records map[string][]Record `json:"records"`
}

// Reliability is a concurrency-safe, persisted ledger of delegation outcomes
// keyed by agent name. A nil *Reliability is a valid no-op.
type Reliability struct {
	mu      sync.Mutex
	path    string
	records map[string][]Record // most recent last
}

// New builds a Reliability ledger backed by path, loading any existing store.
// A corrupt store is ignored (ledger starts empty).
func New(path string) *Reliability {
	r := &Reliability{path: path, records: make(map[string][]Record)}
	r.load()
	return r
}

// NewDefault builds a ledger at the default user-level path
// (~/.ggcode/agent-reliability.json); agent CLI reliability is a machine-wide
// property more than a per-project one.
func NewDefault() *Reliability { return New(DefaultPath()) }

// DefaultPath returns the default persistence path.
func DefaultPath() string {
	home := config.HomeDir()
	if strings.TrimSpace(home) == "" {
		return filepath.Join(".ggcode", "agent-reliability.json")
	}
	return filepath.Join(home, ".ggcode", "agent-reliability.json")
}

// Record appends one outcome for agent and persists best-effort. Unknown
// outcomes are recorded verbatim but count as neither completed nor failed in
// summaries. A nil receiver or empty agent name is a no-op.
func (r *Reliability) Record(agent string, outcome Outcome, started time.Time, d time.Duration) {
	if r == nil || strings.TrimSpace(agent) == "" || outcome == "" {
		return
	}
	if started.IsZero() {
		started = time.Now()
	}

	r.mu.Lock()
	recs := append(r.records[agent], Record{
		Outcome:    outcome,
		StartedAt:  started.UTC(),
		DurationMs: d.Milliseconds(),
	})
	if len(recs) > maxRecordsPerAgent {
		recs = recs[len(recs)-maxRecordsPerAgent:]
	}
	r.records[agent] = recs
	snapshot := r.cloneLocked()
	r.mu.Unlock()

	r.save(snapshot)
}

// AgentSummary renders a one-line stat for agent over the recent window, e.g.
// "codex: 7/10 completed recently (avg 4m12s)". Empty string when no data.
func (r *Reliability) AgentSummary(agent string) string {
	if r == nil {
		return ""
	}
	r.mu.Lock()
	recs := r.records[agent]
	r.mu.Unlock()
	if len(recs) == 0 {
		return ""
	}
	return formatSummary(agent, recs)
}

// Overview renders every tracked agent on one line, e.g.
// "claude 9/10 ok · codex 3/7 ok". Empty string when nothing is tracked.
func (r *Reliability) Overview() string {
	if r == nil {
		return ""
	}
	r.mu.Lock()
	agents := make([]string, 0, len(r.records))
	for a, recs := range r.records {
		if len(recs) > 0 {
			agents = append(agents, a)
		}
	}
	parts := make([]string, 0, len(agents))
	for _, a := range agents {
		parts = append(parts, formatSummaryPlain(a, r.records[a]))
	}
	r.mu.Unlock()

	sort.Strings(agents)
	sort.Strings(parts)
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, " · ")
}

// Agents returns the tracked agent names, sorted.
func (r *Reliability) Agents() []string {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	agents := make([]string, 0, len(r.records))
	for a, recs := range r.records {
		if len(recs) > 0 {
			agents = append(agents, a)
		}
	}
	r.mu.Unlock()
	sort.Strings(agents)
	return agents
}

func formatSummary(agent string, recs []Record) string {
	base := formatSummaryPlain(agent, recs)
	if base == "" {
		return ""
	}
	// formatSummaryPlain is "name ok/total ok"; this variant adds detail.
	recent := recentRecords(recs)
	var totalDur time.Duration
	for _, rec := range recent {
		totalDur += time.Duration(rec.DurationMs) * time.Millisecond
	}
	avg := totalDur / time.Duration(len(recent))
	completed := 0
	for _, rec := range recent {
		if rec.Outcome == OutcomeCompleted {
			completed++
		}
	}
	return fmt.Sprintf("%s: %d/%d completed recently (avg %s)", agent, completed, len(recent), avg.Truncate(time.Second))
}

func formatSummaryPlain(agent string, recs []Record) string {
	recent := recentRecords(recs)
	if len(recent) == 0 {
		return ""
	}
	completed := 0
	for _, rec := range recent {
		if rec.Outcome == OutcomeCompleted {
			completed++
		}
	}
	return fmt.Sprintf("%s %d/%d ok", agent, completed, len(recent))
}

func recentRecords(recs []Record) []Record {
	if len(recs) > recentWindow {
		return recs[len(recs)-recentWindow:]
	}
	return recs
}

func (r *Reliability) cloneLocked() map[string][]Record {
	out := make(map[string][]Record, len(r.records))
	for a, recs := range r.records {
		out[a] = append([]Record(nil), recs...)
	}
	return out
}

func (r *Reliability) load() {
	if r.path == "" {
		return
	}
	data, err := os.ReadFile(r.path)
	if err != nil {
		return // missing or unreadable: start empty
	}
	var sf storeFile
	if err := json.Unmarshal(data, &sf); err != nil {
		return // corrupt: start empty
	}
	for a, recs := range sf.Records {
		clean := make([]Record, 0, len(recs))
		for _, rec := range recs {
			if rec.Outcome == "" {
				continue
			}
			clean = append(clean, rec)
		}
		if len(clean) > maxRecordsPerAgent {
			clean = clean[len(clean)-maxRecordsPerAgent:]
		}
		if len(clean) > 0 {
			r.records[a] = clean
		}
	}
}

func (r *Reliability) save(records map[string][]Record) {
	if r.path == "" {
		return
	}
	data, err := json.Marshal(storeFile{Records: records})
	if err != nil {
		return
	}
	if dir := filepath.Dir(r.path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return
		}
	}
	_ = os.WriteFile(r.path, data, 0o644) // best-effort
}
