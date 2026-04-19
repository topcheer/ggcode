package cost

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
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
	m.mu.RLock()
	t, ok := m.trackers[sessionID]
	m.mu.RUnlock()
	if !ok {
		return nil
	}

	if err := os.MkdirAll(m.dataDir, 0755); err != nil {
		return err
	}

	sc := t.SessionCost()
	data, err := json.Marshal(sc)
	if err != nil {
		return err
	}

	path := filepath.Join(m.dataDir, sessionID+".cost.json")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Load restores session cost data from disk.
func (m *Manager) Load(sessionID, providerName, model string) {
	path := filepath.Join(m.dataDir, sessionID+".cost.json")
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
	m.trackers[sessionID] = t
}

// FormatCost returns a human-readable cost string.
func FormatCost(usd float64) string {
	if usd < 0.01 {
		return fmt.Sprintf("$%.4f", usd)
	}
	return fmt.Sprintf("$%.2f", usd)
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
	return fmt.Sprintf(
		"  %s (%s) — in: %s  out: %s  cost: %s",
		sc.Model,
		sc.Provider,
		FormatTokens(sc.InputTokens),
		FormatTokens(sc.OutputTokens),
		FormatCost(sc.TotalCostUSD),
	)
}
