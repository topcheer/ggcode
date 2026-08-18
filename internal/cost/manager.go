package cost

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/util"
)

// Manager tracks cost across all sessions.
type Manager struct {
	mu       sync.RWMutex
	trackers map[string]*Tracker // sessionID → Tracker
	pricing  PricingTable
	dataDir  string // directory for persistent cost data
}

// NewManager creates a cost manager with the given pricing table.
func NewManager(pricing PricingTable, dataDir string) *Manager {
	return &Manager{
		trackers: make(map[string]*Tracker),
		pricing:  pricing,
		dataDir:  dataDir,
	}
}

// GetOrCreateTracker returns the tracker for a session, creating one if needed.
func (m *Manager) GetOrCreateTracker(sessionID, providerName, model string) *Tracker {
	m.mu.Lock()
	defer m.mu.Unlock()

	if t, ok := m.trackers[sessionID]; ok {
		return t
	}
	t := NewTracker(providerName, model, m.pricing)
	m.trackers[sessionID] = t
	return t
}

// SessionCost returns cost for a specific session.
func (m *Manager) SessionCost(sessionID string) (SessionCost, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	t, ok := m.trackers[sessionID]
	if !ok {
		return SessionCost{}, false
	}
	return t.SessionCost(), true
}

// AllCosts returns costs for all sessions, sorted by total cost descending.
func (m *Manager) AllCosts() []SessionCost {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var costs []SessionCost
	for _, t := range m.trackers {
		costs = append(costs, t.SessionCost())
	}
	sort.Slice(costs, func(i, j int) bool {
		return costs[i].TotalCostUSD > costs[j].TotalCostUSD
	})
	return costs
}

// TotalCost returns the sum of all session costs.
func (m *Manager) TotalCost() float64 {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var total float64
	for _, t := range m.trackers {
		sc := t.SessionCost()
		total += sc.TotalCostUSD
	}
	return total
}

// Save persists session cost data to disk.
func (m *Manager) Save(sessionID string) error {
	// Sanitize sessionID to prevent path traversal.
	cleanID := filepath.Base(sessionID)
	if cleanID != sessionID || cleanID == "." || cleanID == ".." {
		return fmt.Errorf("invalid session ID: %q", sessionID)
	}

	m.mu.RLock()
	t, ok := m.trackers[sessionID]
	m.mu.RUnlock()
	if !ok {
		return nil
	}

	if err := os.MkdirAll(m.dataDir, 0700); err != nil {
		return err
	}

	sc := t.SessionCost()
	data, err := json.Marshal(sc)
	if err != nil {
		return err
	}

	path := filepath.Join(m.dataDir, cleanID+".cost.json")
	return util.AtomicWriteFile(path, data, 0600)
}

// Load restores session cost data from disk.
func (m *Manager) Load(sessionID, providerName, model string) {
	cleanID := filepath.Base(sessionID)
	if cleanID != sessionID || cleanID == "." || cleanID == ".." {
		return
	}
	path := filepath.Join(m.dataDir, cleanID+".cost.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}

	var sc SessionCost
	if err := json.Unmarshal(data, &sc); err != nil {
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	t := NewTracker(providerName, model, m.pricing)
	t.cost = sc
	// #683: snapshots persisted while pricing was unknown never recomputed,
	// even after the pricing table learned the model. Recalculate only those;
	// snapshots that already carry a computed cost are trusted as-is (a blind
	// recalculation would zero manually persisted totals when pricing is
	// still unknown).
	if !sc.HasPricing {
		t.recalculate()
	}
	m.trackers[sessionID] = t
}

// LoadAllFromDisk scans the data directory for all .cost.json files and loads
// them into the manager. This enables cross-session cost aggregation.
func (m *Manager) LoadAllFromDisk() int {
	entries, err := os.ReadDir(m.dataDir)
	if err != nil {
		return 0
	}
	loaded := 0
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".cost.json") {
			continue
		}
		sessionID := strings.TrimSuffix(name, ".cost.json")
		path := filepath.Join(m.dataDir, name)
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var sc SessionCost
		if err := json.Unmarshal(data, &sc); err != nil {
			continue // skip corrupt files
		}
		m.mu.Lock()
		t := NewTracker(sc.Provider, sc.Model, m.pricing)
		t.cost = sc
		// #683: recalculate only no-pricing snapshots — see Load().
		if !sc.HasPricing {
			t.recalculate()
		}
		m.trackers[sessionID] = t
		m.mu.Unlock()
		loaded++
	}
	return loaded
}

// AggregateAllCosts returns the summed totals across all sessions.
func (m *Manager) AggregateAllCosts() SessionCost {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var agg SessionCost
	sessions := 0
	for _, t := range m.trackers {
		sc := t.SessionCost()
		agg.InputTokens += sc.InputTokens
		agg.OutputTokens += sc.OutputTokens
		agg.CacheReadTokens += sc.CacheReadTokens
		agg.CacheWriteTokens += sc.CacheWriteTokens
		agg.TotalCostUSD += sc.TotalCostUSD
		sessions++
	}
	agg.Provider = fmt.Sprintf("%d sessions", sessions)
	return agg
}

// FormatCost returns a human-readable cost string.
// #683: negative amounts previously rendered as "$-1.50"; use the conventional
// "-$1.50" placement so callers composing "net loss: $x (-y%)" can't produce
// a "--" double-minus.
func FormatCost(usd float64) string {
	sign := ""
	if usd < 0 {
		sign = "-"
		usd = -usd
	}
	if usd < 0.01 {
		return fmt.Sprintf("%s$%.4f", sign, usd)
	}
	return fmt.Sprintf("%s$%.2f", sign, usd)
}

// FormatTokens returns a human-readable token count.
func FormatTokens(n int64) string {
	if n < 1000 {
		return fmt.Sprintf("%d", n)
	}
	return fmt.Sprintf("%d,%03d", n/1000, n%1000)
}

// FormatSessionCost returns a formatted summary for a session.
func FormatSessionCost(sc SessionCost, now time.Time) string {
	// #683: an unknown model previously showed a false-precise "$0.0000",
	// indistinguishable from a genuinely free session. Honor the
	// "(no pricing data)" display promised by PricingUnknown.
	costStr := FormatCost(sc.TotalCostUSD)
	if !sc.HasPricing {
		costStr = "(no pricing data)"
	}
	return fmt.Sprintf(
		"  %s (%s) — in: %s  out: %s  cost: %s",
		sc.Model,
		sc.Provider,
		FormatTokens(sc.InputTokens),
		FormatTokens(sc.OutputTokens),
		costStr,
	)
}

// FormatAgentCostBreakdown returns a multi-line breakdown of per-agent costs.
// This gives visibility into which sub-agents, teammates, or delegations
// consumed the most resources within a session — enabling cost optimization
// decisions (e.g., "spawn:research-1 used 60% of session cost").
//
// The total line is always included; individual agents are listed only if
// per-agent tracking data exists (i.e., RecordForAgent was used).
func FormatAgentCostBreakdown(breakdown AgentCostBreakdown) string {
	if len(breakdown.Entries) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("  Per-agent cost breakdown:\n")
	for _, e := range breakdown.Entries {
		label := e.AgentID
		if label == "" {
			label = "main"
		}
		// Compute percentage of total cost.
		pct := 0.0
		if breakdown.Total.TotalCostUSD > 0 {
			pct = e.TotalCostUSD / breakdown.Total.TotalCostUSD * 100
		}
		b.WriteString(fmt.Sprintf(
			"    %-24s in: %s  out: %s  cost: %s (%.0f%%)\n",
			label,
			FormatTokens(e.InputTokens),
			FormatTokens(e.OutputTokens),
			FormatCost(e.TotalCostUSD),
			pct,
		))
	}
	return strings.TrimRight(b.String(), "\n")
}

// GetAgentCostBreakdown returns the per-agent cost breakdown for a session.
func (m *Manager) GetAgentCostBreakdown(sessionID string) (AgentCostBreakdown, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	t, ok := m.trackers[sessionID]
	if !ok {
		return AgentCostBreakdown{}, false
	}
	return t.AgentCostBreakdown(), true
}
