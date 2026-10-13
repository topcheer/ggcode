// Package bom builds a learning-artifact bill of materials: a read-only,
// deterministic inventory of every persisted artifact that currently shapes
// agent behavior - skills, staged skills, memories, and trajectory insights -
// with content hash, source location, and the promotion gate each artifact
// passed on its way in.
//
// Rationale (sa-78, AgentBOM / arXiv:2606.04990 execution-provenance survey):
// self-evolving agents accumulate learned state across many subsystems whose
// stores live in scattered files (.ggcode/skills/, skills-staging/, memory/,
// trajectory-learnings.jsonl, disabled_skills.json). None of those stores
// answers the audit question "what is the agent made of right now, and through
// which gate did each piece enter?". This package aggregates them so one
// command can. Deliberately lexical: no LLM, no locks, no writes anywhere.
package bom

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Artifact type names (Entry.Type).
const (
	TypeSkill       = "skill"        // active skill (SKILL.md present under skills/)
	TypeSkillStaged = "skill-staged" // skill awaiting knight promotion (skills-staging/)
	TypeMemory      = "memory"       // persisted memory entry (memory/*.md)
	TypeTrajInsight = "traj-insight" // trajectory learnings ledger (trajectory-learnings.jsonl)
)

// Promotion gates (Entry.PromotionGate): the mechanism that admitted the
// artifact. Kept per-class because that is how the pipelines are factored.
const (
	GateKnight        = "knight"         // skill lifecycle: knight index + enable/disable state
	GateKnightStaging = "knight-staging" // staged, awaiting dawn promotion gate
	GateHoldout       = "holdout"        // traj insight: shadow-holdout counterfactual Δ gate
	GateAuto          = "auto"           // auto-saved memory (research-/build-/release-/preference-)
	GateManual        = "manual"         // hand-written memory or skill
)

// autoMemoryPrefixes are the memory key prefixes that the agent itself writes
// (distill/consolidation/release pipelines). Everything else in memory/ was
// saved by a human or by explicit instruction, i.e. manual.
var autoMemoryPrefixes = []string{"research-", "build-", "release-", "preference-"}

// Entry is one learning artifact in the bill of materials.
type Entry struct {
	Name          string    `json:"name"`
	Type          string    `json:"type"`
	Scope         string    `json:"scope"` // "project" | "global"
	Path          string    `json:"path"`
	SHA256        string    `json:"sha256"` // first 12 hex chars of content hash
	Bytes         int64     `json:"bytes"`
	Modified      time.Time `json:"modified"`
	Enabled       bool      `json:"enabled"`         // skills only; memories/insights are always true
	PromotionGate string    `json:"promotion_gate"`  // gate that admitted the artifact
	Items         int       `json:"items,omitempty"` // jsonl line count (traj-insight only)
}

// Report is the full bill of materials.
type Report struct {
	GeneratedAt time.Time      `json:"generated_at"`
	Workspace   string         `json:"workspace"`
	Home        string         `json:"home"`
	Counts      map[string]int `json:"counts"`
	Entries     []Entry        `json:"entries"`
	Warnings    []string       `json:"warnings,omitempty"`
}

// Build scans the workspace (<ws>/.ggcode) and the user home (<home>/.ggcode)
// for persisted learning artifacts and returns the aggregated BOM. It never
// returns an error for missing stores - an absent store simply contributes no
// entries - only for unusful inputs (empty paths).
func Build(workspace, home string) (*Report, error) {
	if workspace == "" || home == "" {
		return nil, os.ErrInvalid
	}
	rep := &Report{
		GeneratedAt: time.Now().UTC(),
		Workspace:   workspace,
		Home:        home,
		Counts:      map[string]int{},
	}
	disabled := loadDisabledNames(filepath.Join(home, ".ggcode", "disabled_skills.json"))

	scanSkills(rep, filepath.Join(workspace, ".ggcode", "skills"), "project", disabled)
	scanSkills(rep, filepath.Join(home, ".ggcode", "skills"), "global", disabled)
	scanSkills(rep, filepath.Join(workspace, ".ggcode", "skills-staging"), "project", nil)
	scanSkills(rep, filepath.Join(home, ".ggcode", "skills-staging"), "global", nil)
	scanMemories(rep, filepath.Join(workspace, ".ggcode", "memory"), "project")
	scanMemories(rep, filepath.Join(home, ".ggcode", "memory"), "global")
	scanTraj(rep, filepath.Join(workspace, ".ggcode", "trajectory-learnings.jsonl"), "project")
	scanTraj(rep, filepath.Join(home, ".ggcode", "trajectory-learnings.jsonl"), "global")

	for _, e := range rep.Entries {
		rep.Counts[e.Type]++
	}
	sort.Slice(rep.Entries, func(i, j int) bool {
		a, b := rep.Entries[i], rep.Entries[j]
		if a.Type != b.Type {
			return a.Type < b.Type
		}
		if a.Scope != b.Scope {
			return a.Scope < b.Scope
		}
		return a.Name < b.Name
	})
	return rep, nil
}

// scanSkills adds one entry per directory containing a SKILL.md under dir.
// When disabled is non-nil the entry's Enabled flag reflects the persisted
// disable state (staged skills are never "enabled" - they are not injectable
// until promoted).
func scanSkills(rep *Report, dir, scope string, disabled map[string]bool) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return // no such store: fine
	}
	for _, de := range entries {
		if !de.IsDir() {
			continue
		}
		skillMD := filepath.Join(dir, de.Name(), "SKILL.md")
		info, err := os.Stat(skillMD)
		if err != nil || info.IsDir() {
			continue
		}
		hash, size, err := hashFile(skillMD)
		if err != nil {
			rep.Warnings = append(rep.Warnings, "skill unreadable: "+skillMD)
			continue
		}
		e := Entry{
			Name:     de.Name(),
			Type:     TypeSkill,
			Scope:    scope,
			Path:     skillMD,
			SHA256:   hash,
			Bytes:    size,
			Modified: info.ModTime().UTC(),
		}
		if disabled != nil {
			e.Enabled = !disabled[strings.ToLower(strings.TrimSpace(de.Name()))]
		} else {
			e.Type = TypeSkillStaged
			e.Enabled = false
		}
		switch e.Type {
		case TypeSkill:
			e.PromotionGate = GateKnight
		default:
			e.PromotionGate = GateKnightStaging
		}
		rep.Entries = append(rep.Entries, e)
	}
}

// scanMemories adds one entry per *.md memory file.
func scanMemories(rep *Report, dir, scope string) {
	files, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, f := range files {
		if f.IsDir() || !strings.HasSuffix(f.Name(), ".md") {
			continue
		}
		path := filepath.Join(dir, f.Name())
		info, err := os.Stat(path)
		if err != nil || info.IsDir() {
			continue
		}
		hash, size, err := hashFile(path)
		if err != nil {
			rep.Warnings = append(rep.Warnings, "memory unreadable: "+path)
			continue
		}
		key := strings.TrimSuffix(f.Name(), ".md")
		gate := GateManual
		for _, p := range autoMemoryPrefixes {
			if strings.HasPrefix(key, p) {
				gate = GateAuto
				break
			}
		}
		rep.Entries = append(rep.Entries, Entry{
			Name:          key,
			Type:          TypeMemory,
			Scope:         scope,
			Path:          path,
			SHA256:        hash,
			Bytes:         size,
			Modified:      info.ModTime().UTC(),
			Enabled:       true,
			PromotionGate: gate,
		})
	}
}

// scanTraj adds a single entry for the trajectory-learnings ledger. The ledger
// is a whole store rather than independent artifacts, so it is inventoried as
// one entry with an item (line) count.
func scanTraj(rep *Report, path, scope string) {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return
	}
	hash, size, err := hashFile(path)
	if err != nil {
		rep.Warnings = append(rep.Warnings, "ledger unreadable: "+path)
		return
	}
	items := 0
	if data, err := os.ReadFile(path); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			if strings.TrimSpace(line) != "" {
				items++
			}
		}
	}
	rep.Entries = append(rep.Entries, Entry{
		Name:          "trajectory-learnings",
		Type:          TypeTrajInsight,
		Scope:         scope,
		Path:          path,
		SHA256:        hash,
		Bytes:         size,
		Modified:      info.ModTime().UTC(),
		Enabled:       true,
		PromotionGate: GateHoldout,
		Items:         items,
	})
}

// loadDisabledNames reads the persisted disabled-skill list
// (~/.ggcode/disabled_skills.json, a JSON array of skill names). Mirrors the
// normalization used by the writer side: trim + lowercase. Any read or parse
// problem yields an empty set - the BOM stays useful with partial state.
func loadDisabledNames(path string) map[string]bool {
	out := map[string]bool{}
	data, err := os.ReadFile(path)
	if err != nil {
		return out
	}
	var names []string
	if err := json.Unmarshal(data, &names); err != nil {
		return out
	}
	for _, n := range names {
		out[strings.ToLower(strings.TrimSpace(n))] = true
	}
	return out
}

func hashFile(path string) (string, int64, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", 0, err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])[:12], int64(len(data)), nil
}
