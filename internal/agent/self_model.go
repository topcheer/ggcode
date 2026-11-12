package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Persistent agent self-model (#feat-self-model, P1). Research basis:
// SARSI arXiv:2607.12254 - agents keep a durable self-model (capabilities /
// limitations / dependencies) that drives scope routing and structured
// handoff; SEAA arXiv:2609.17331 - a self-model must be updated from
// experience, not frozen in a static prompt. ggcode previously had only
// session-scoped reflexes (capability_boundary warnings, in-memory tool
// effectiveness); this file makes the signal durable and queryable.

type SkillStat struct {
	Success  int    `json:"success"`
	Fail     int    `json:"fail"`
	LastFail string `json:"lastFail,omitempty"`
}

type AgentSelfModel struct {
	mu             sync.Mutex
	Skills         map[string]*SkillStat `json:"skills"`
	DeclaredLimits []string              `json:"declaredLimits,omitempty"`
	UpdatedAt      time.Time             `json:"updatedAt"`
}

// SelfModelPath returns the persistence path under the given workspace.
func SelfModelPath(workspace string) string {
	return filepath.Join(workspace, ".ggcode", "self_model.json")
}

func LoadSelfModel(path string) *AgentSelfModel {
	m := &AgentSelfModel{Skills: make(map[string]*SkillStat)}
	data, err := os.ReadFile(path)
	if err != nil {
		return m
	}
	var loaded AgentSelfModel
	if json.Unmarshal(data, &loaded) != nil || loaded.Skills == nil {
		return m
	}
	// Merge-on-load (SEAA: experience must accumulate, not reset).
	m.Skills = loaded.Skills
	m.DeclaredLimits = loaded.DeclaredLimits
	m.UpdatedAt = loaded.UpdatedAt
	return m
}

func (m *AgentSelfModel) save(path string) {
	if m == nil || path == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.UpdatedAt = time.Now()
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return
	}
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	_ = os.WriteFile(path, data, 0o600)
}

// record upserts one tool outcome and persists.
func (m *AgentSelfModel) Record(path, tool string, ok bool, failExcerpt string) {
	if m == nil || tool == "" {
		return
	}
	m.mu.Lock()
	st := m.Skills[tool]
	if st == nil {
		st = &SkillStat{}
		m.Skills[tool] = st
	}
	if ok {
		st.Success++
	} else {
		st.Fail++
		st.LastFail = truncateSelfModel(failExcerpt, 80)
	}
	m.mu.Unlock()
	m.save(path)
}

// addLimit appends a declared limitation (capability-boundary escalations)
// and persists. Capped to keep the card small.
func (m *AgentSelfModel) AddLimit(path, line string) {
	if m == nil || strings.TrimSpace(line) == "" {
		return
	}
	m.mu.Lock()
	const maxLimits = 20
	if len(m.DeclaredLimits) >= maxLimits {
		m.DeclaredLimits = m.DeclaredLimits[1:]
	}
	m.DeclaredLimits = append(m.DeclaredLimits, truncateSelfModel(line, 120))
	m.mu.Unlock()
	m.save(path)
}

// DelegationContext renders the card for delegation prompts / handoffs:
// top-3 strengths, declared limits, so the dispatcher (or receiving agent)
// knows what this agent is actually good at.
func (m *AgentSelfModel) DelegationContext() string {
	if m == nil || len(m.Skills) == 0 {
		return ""
	}
	type ranked struct {
		name  string
		rate  float64
		tries int
	}
	var rs []ranked
	for name, st := range m.Skills {
		tries := st.Success + st.Fail
		if tries >= 3 {
			rs = append(rs, ranked{name, float64(st.Success) / float64(tries), tries})
		}
	}
	if len(rs) == 0 {
		return ""
	}
	sort.Slice(rs, func(i, j int) bool { return rs[i].rate > rs[j].rate })
	var b strings.Builder
	b.WriteString("[agent self-model] proven strengths: ")
	for i, r := range rs {
		if i == 3 {
			break
		}
		fmt.Fprintf(&b, "%s %.0f%%(%d) ", r.name, r.rate*100, r.tries)
	}
	if len(m.DeclaredLimits) > 0 {
		fmt.Fprintf(&b, "\nknown limits (last %d): ", len(m.DeclaredLimits))
		for i, l := range m.DeclaredLimits {
			if i == 3 {
				break
			}
			b.WriteString("; " + l)
		}
	}
	return b.String()
}

func truncateSelfModel(s string, n int) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	if len(s) <= n {
		return s
	}
	return s[:n]
}
