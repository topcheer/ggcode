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

const (
	TypeSkill       = "skill"
	TypeSkillStaged = "skill-staged"
	TypeMemory      = "memory"
	TypeTrajInsight = "traj-insight"
)

const (
	GateKnight        = "knight"
	GateKnightStaging = "knight-staging"
	GateHoldout       = "holdout"
	GateAuto          = "auto"
	GateManual        = "manual"
)

var autoMemoryPrefixes = []string{"research-", "build-", "release-", "preference-"}

type Entry struct {
	Name          string    `json:"name"`
	Type          string    `json:"type"`
	Scope         string    `json:"scope"`
	Path          string    `json:"path"`
	SHA256        string    `json:"sha256"`
	Bytes         int64     `json:"bytes"`
	Modified      time.Time `json:"modified"`
	Enabled       bool      `json:"enabled"`
	PromotionGate string    `json:"promotion_gate"`
	Items         int       `json:"items,omitempty"`
}

type Report struct {
	GeneratedAt time.Time      `json:"generated_at"`
	Workspace   string         `json:"workspace"`
	Home        string         `json:"home"`
	Counts      map[string]int `json:"counts"`
	Entries     []Entry        `json:"entries"`
	Warnings    []string       `json:"warnings,omitempty"`
}

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

func scanSkills(rep *Report, dir, scope string, disabled map[string]bool) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
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
