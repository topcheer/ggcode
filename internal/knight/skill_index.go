package knight

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

// SkillMeta is the parsed frontmatter from a SKILL.md or staging skill file.
type SkillMeta struct {
	Name                string   `yaml:"name"`
	Description         string   `yaml:"description"`
	Scope               string   `yaml:"scope"` // "global" or "project"
	Platforms           []string `yaml:"platforms"`
	Requires            []string `yaml:"requires"`
	CreatedBy           string   `yaml:"created_by"`   // "user", "knight", "agent"
	CreatedFrom         string   `yaml:"created_from"` // source session ID
	CreatedAt           string   `yaml:"created_at"`
	UpdatedAt           string   `yaml:"updated_at"`
	UsageCount          int      `yaml:"usage_count"`
	LastUsed            string   `yaml:"last_used"`
	EffectivenessScores []int    `yaml:"effectiveness_scores"`
	Frozen              bool     `yaml:"frozen"`
}

// SkillEntry is a discovered skill with its metadata and file location.
type SkillEntry struct {
	Name     string
	Meta     SkillMeta
	Path     string // full path to the skill file
	Scope    string // "global" or "project"
	Staging  bool   // true if in staging directory
	LoadedAt time.Time
}

// SkillIndex scans and indexes skills from both global and project directories.
// Results are cached with a TTL to avoid repeated disk reads.
type SkillIndex struct {
	globalDir      string // ~/.ggcode/skills/
	globalStaging  string // ~/.ggcode/skills-staging/
	projectDir     string // .ggcode/skills/
	projectStaging string // .ggcode/skills-staging/

	mu        sync.RWMutex
	cache     []*SkillEntry
	cacheTime time.Time
	cacheTTL  time.Duration
}

// Default index cache TTL — avoid re-scanning disk on every call.
const defaultCacheTTL = 30 * time.Second

// NewSkillIndex creates a skill index for the given home and project dirs.
func NewSkillIndex(homeDir, projectDir string) *SkillIndex {
	return &SkillIndex{
		globalDir:      filepath.Join(homeDir, ".ggcode", "skills"),
		globalStaging:  filepath.Join(homeDir, ".ggcode", "skills-staging"),
		projectDir:     filepath.Join(projectDir, ".ggcode", "skills"),
		projectStaging: filepath.Join(projectDir, ".ggcode", "skills-staging"),
		cacheTTL:       defaultCacheTTL,
	}
}

// Invalidate clears the cache, forcing a fresh scan on next access.
func (si *SkillIndex) Invalidate() {
	si.mu.Lock()
	defer si.mu.Unlock()
	si.cache = nil
	si.cacheTime = time.Time{}
}

// Scan returns all discovered skills (active + staging), using cache when fresh.
func (si *SkillIndex) Scan() ([]*SkillEntry, error) {
	si.mu.RLock()
	if si.cache != nil && time.Since(si.cacheTime) < si.cacheTTL {
		cached := si.cache
		si.mu.RUnlock()
		return cached, nil
	}
	si.mu.RUnlock()

	var entries []*SkillEntry

	// Global active skills
	globals, _ := si.scanDir(si.globalDir, "global", false)
	entries = append(entries, globals...)

	// Global staging skills
	globalStaging, _ := si.scanDir(si.globalStaging, "global", true)
	entries = append(entries, globalStaging...)

	// Project active skills
	projects, _ := si.scanDir(si.projectDir, "project", false)
	entries = append(entries, projects...)

	// Project staging skills
	projectStaging, _ := si.scanDir(si.projectStaging, "project", true)
	entries = append(entries, projectStaging...)

	// Cache result
	si.mu.Lock()
	si.cache = entries
	si.cacheTime = time.Now()
	si.mu.Unlock()

	return entries, nil
}

// ActiveSkills returns only active (non-staging) skills.
func (si *SkillIndex) ActiveSkills() ([]*SkillEntry, error) {
	all, err := si.Scan()
	if err != nil {
		return nil, err
	}
	var active []*SkillEntry
	for _, e := range all {
		if !e.Staging {
			active = append(active, e)
		}
	}
	return active, nil
}

// StagingSkills returns only staging skills.
func (si *SkillIndex) StagingSkills() ([]*SkillEntry, error) {
	all, err := si.Scan()
	if err != nil {
		return nil, err
	}
	var staging []*SkillEntry
	for _, e := range all {
		if e.Staging {
			staging = append(staging, e)
		}
	}
	return staging, nil
}

// FindActiveByName finds an active skill by name.
func (si *SkillIndex) FindActiveByName(name string) *SkillEntry {
	// Project-level takes priority
	if e := si.findInDir(si.projectDir, "project", false, name); e != nil {
		return e
	}
	return si.findInDir(si.globalDir, "global", false, name)
}

// Directories returns all skill directories for display purposes.
func (si *SkillIndex) Directories() []string {
	return []string{
		si.globalDir,
		si.globalStaging,
		si.projectDir,
		si.projectStaging,
	}
}

// scanDir scans a directory for SKILL.md files (active) or .md files (staging).
func (si *SkillIndex) scanDir(dir, scope string, staging bool) ([]*SkillEntry, error) {
	var entries []*SkillEntry

	// Ensure directory exists
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return entries, nil
	}

	if staging {
		// Staging: flat .md files
		items, err := os.ReadDir(dir)
		if err != nil {
			return nil, err
		}
		for _, item := range items {
			if item.IsDir() || !strings.HasSuffix(item.Name(), ".md") {
				continue
			}
			path := filepath.Join(dir, item.Name())
			name := skillNameFromFile(item.Name())
			meta, err := parseSkillFile(path)
			if err != nil {
				continue
			}
			// Prefer frontmatter name over filename-derived name
			if meta.Name != "" {
				name = meta.Name
			}
			entries = append(entries, &SkillEntry{
				Name:    name,
				Meta:    meta,
				Path:    path,
				Scope:   scope,
				Staging: true,
			})
		}
	} else {
		// Active: subdirectories with SKILL.md
		items, err := os.ReadDir(dir)
		if err != nil {
			return nil, err
		}
		for _, item := range items {
			if !item.IsDir() {
				continue
			}
			path := filepath.Join(dir, item.Name(), "SKILL.md")
			meta, err := parseSkillFile(path)
			if err != nil {
				continue
			}
			name := item.Name()
			if meta.Name != "" {
				name = meta.Name
			}
			entries = append(entries, &SkillEntry{
				Name:    name,
				Meta:    meta,
				Path:    path,
				Scope:   scope,
				Staging: false,
			})
		}
	}

	return entries, nil
}

func (si *SkillIndex) findInDir(dir, scope string, staging bool, name string) *SkillEntry {
	entries, _ := si.scanDir(dir, scope, staging)
	for _, e := range entries {
		if e.Name == name {
			return e
		}
	}
	return nil
}

// parseSkillFile reads a skill file and extracts its frontmatter.
func parseSkillFile(path string) (SkillMeta, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return SkillMeta{}, err
	}
	return parseSkillFrontmatter(string(data))
}

// parseSkillFrontmatter extracts YAML frontmatter from a skill markdown file.
func parseSkillFrontmatter(content string) (SkillMeta, error) {
	var meta SkillMeta
	content = strings.ReplaceAll(content, "\r\n", "\n")

	// Content too short to have valid frontmatter
	if len(content) < 7 { // minimum: ---\nx\n---\n
		return meta, fmt.Errorf("content too short for frontmatter")
	}

	// Must start with ---
	if !strings.HasPrefix(content, "---") {
		return meta, fmt.Errorf("no frontmatter found")
	}

	// Find the closing ---
	// Skip the opening --- and optional whitespace
	rest := content[3:]
	if len(rest) > 0 && rest[0] == '\n' {
		rest = rest[1:]
	}
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return meta, fmt.Errorf("unclosed frontmatter")
	}
	fm := rest[:end]
	fm = strings.TrimSpace(fm)

	if err := yaml.Unmarshal([]byte(fm), &meta); err != nil {
		return meta, fmt.Errorf("parse frontmatter: %w", err)
	}
	return meta, nil
}

// skillNameFromFile derives a skill name from a staging filename.
// e.g. "knight-20260419-build-flow.md" → "build-flow"
func skillNameFromFile(filename string) string {
	name := strings.TrimSuffix(filename, ".md")
	// Strip "knight-YYYYMMDD-" prefix if present
	if strings.HasPrefix(name, "knight-") {
		parts := strings.SplitN(name, "-", 3)
		if len(parts) >= 3 && len(parts[1]) == 8 {
			name = parts[2]
		}
	}
	return name
}
