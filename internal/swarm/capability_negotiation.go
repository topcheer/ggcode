package swarm

import (
	"fmt"
	"sort"
)

// CapabilityNegotiation answers, BEFORE a task is dispatched, whether a
// teammate's tool allowlist can serve the task's required tools. Missing
// lists the gaps (sorted); an empty result means the dispatch is safe.
// Inform-only: callers decide whether to block, warn, or re-route.
func (m *Manager) NegotiateCapability(teamID, tmID string, requiredTools []string) (missing []string, err error) {
	m.mu.Lock()
	team, ok := m.teams[teamID]
	if !ok {
		m.mu.Unlock()
		return nil, fmt.Errorf("team %q not found", teamID)
	}
	team.mu.RLock()
	tm, ok := team.Teammates[tmID]
	if !ok {
		team.mu.RUnlock()
		m.mu.Unlock()
		return nil, fmt.Errorf("teammate %q not found in team %q", tmID, teamID)
	}
	tm.mu.Lock()
	allowed := tm.AllowedTools
	tm.mu.Unlock()
	team.mu.RUnlock()
	m.mu.Unlock()

	if len(allowed) == 0 {
		return nil, nil // empty allowlist inherits the full tool surface
	}
	allowedSet := make(map[string]bool, len(allowed))
	for _, t := range allowed {
		allowedSet[t] = true
	}
	for _, t := range requiredTools {
		if !allowedSet[t] {
			missing = append(missing, t)
		}
	}
	sort.Strings(missing)
	return missing, nil
}
