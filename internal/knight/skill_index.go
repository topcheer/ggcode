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

type SkillMeta struct {
	Name                string   `yaml:"name"`
	Description         string   `yaml:"description"`
	Scope               string   `yaml:"scope"`
	Platforms           []string `yaml:"platforms"`
	Requires            []string `yaml:"requires"`
	CreatedBy           string   `yaml:"created_by"`
	CreatedFrom         string   `yaml:"created_from"`
	CreatedAt           string   `yaml:"created_at"`
	UpdatedAt           string   `yaml:"updated_at"`
	UsageCount          int      `yaml:"usage_count"`
	LastUsed            string   `yaml:"last_used"`
	PromptExposureCount int      `yaml:"prompt_exposure_count"`
	LastPromptExposure  string   `yaml:"last_prompt_exposure"`
	PromptSuccessCount  int      `yaml:"prompt_success_count"`
	PromptFailureCount  int      `yaml:"prompt_failure_count"`
	EffectivenessScores []int    `yaml:"effectiveness_scores"`
	Frozen              bool     `yaml:"frozen"`
}

type SkillEntry struct {
	Name     string
	Meta     SkillMeta
	Path     string
	Scope    string
	Staging  bool
	LoadedAt time.Time
}

type SkillIndex struct {
	globalDir      string
	globalStaging  string
	projectDir     string
	projectStaging string

	mu        sync.RWMutex
	cache     []*SkillEntry
	cacheTime time.Time
	cacheTTL  time.Duration

	epoch uint64
}

const defaultCacheTTL = 30 * time.Second

func NewSkillIndex(homeDir, projectDir string) *SkillIndex {
	return &SkillIndex{
		globalDir:      filepath.Join(homeDir, ".ggcode", "skills"),
		globalStaging:  filepath.Join(homeDir, ".ggcode", "skills-staging"),
		projectDir:     filepath.Join(projectDir, ".ggcode", "skills"),
		projectStaging: filepath.Join(projectDir, ".ggcode", "skills-staging"),
		cacheTTL:       defaultCacheTTL,
	}
}

func (si *SkillIndex) Invalidate() {
	si.mu.Lock()
	defer si.mu.Unlock()
	si.cache = nil
	si.cacheTime = time.Time{}
	si.epoch++
}

func (si *SkillIndex) Scan() ([]*SkillEntry, error) {
	si.mu.RLock()
	if si.cache != nil && time.Since(si.cacheTime) < si.cacheTTL {
		cached := si.cache
		si.mu.RUnlock()
		return cached, nil
	}

	scanEpoch := si.epoch
	si.mu.RUnlock()

	var entries []*SkillEntry
	var firstErr error
	seen := make(map[string]struct{}, 4)
	scan := func(dir, scope string, staging bool) {
		if !markSkillDirSeen(seen, dir) {
			return
		}
		found, err := si.scanDir(dir, scope, staging)
		if err != nil {

			if firstErr == nil {
				firstErr = err
			}
			return
		}
		entries = append(entries, found...)
	}

	scan(si.globalDir, "global", false)

	scan(si.globalStaging, "global", true)

	scan(si.projectDir, "project", false)

	scan(si.projectStaging, "project", true)

	if firstErr != nil {
		return entries, firstErr
	}

	si.mu.Lock()
	if si.epoch == scanEpoch {
		si.cache = entries
		si.cacheTime = time.Now()
	}
	si.mu.Unlock()

	return entries, nil
}

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

func (si *SkillIndex) LooseActiveSkillFiles() ([]*SkillEntry, error) {
	var entries []*SkillEntry
	seen := make(map[string]struct{}, 2)
	if !markSkillDirSeen(seen, si.globalDir) {
		return entries, nil
	}
	globals, err := si.scanLooseActiveDir(si.globalDir, "global")
	if err != nil {
		return nil, err
	}
	entries = append(entries, globals...)
	if !markSkillDirSeen(seen, si.projectDir) {
		return entries, nil
	}
	projects, err := si.scanLooseActiveDir(si.projectDir, "project")
	if err != nil {
		return nil, err
	}
	entries = append(entries, projects...)
	return entries, nil
}

func (si *SkillIndex) FindActiveByName(name string) *SkillEntry {
	if canonicalSkillDir(si.projectDir) != canonicalSkillDir(si.globalDir) {

		if e := si.findInDir(si.projectDir, "project", false, name); e != nil {
			return e
		}
	}
	return si.findInDir(si.globalDir, "global", false, name)
}

func (si *SkillIndex) Directories() []string {
	dirs := []string{
		si.globalDir,
		si.globalStaging,
		si.projectDir,
		si.projectStaging,
	}
	out := make([]string, 0, len(dirs))
	seen := make(map[string]struct{}, len(dirs))
	for _, dir := range dirs {
		if !markSkillDirSeen(seen, dir) {
			continue
		}
		out = append(out, dir)
	}
	return out
}

func markSkillDirSeen(seen map[string]struct{}, dir string) bool {
	key := canonicalSkillDir(dir)
	if _, ok := seen[key]; ok {
		return false
	}
	seen[key] = struct{}{}
	return true
}

func canonicalSkillDir(dir string) string {
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		dir = resolved
	} else if abs, absErr := filepath.Abs(dir); absErr == nil {
		dir = abs
	}
	return filepath.Clean(dir)
}

func (si *SkillIndex) scanDir(dir, scope string, staging bool) ([]*SkillEntry, error) {
	var entries []*SkillEntry

	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return entries, nil
	}

	if staging {

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

func (si *SkillIndex) scanLooseActiveDir(dir, scope string) ([]*SkillEntry, error) {
	var entries []*SkillEntry
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return entries, nil
	}
	items, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	for _, item := range items {
		if item.IsDir() || !strings.HasSuffix(item.Name(), ".md") {
			continue
		}
		path := filepath.Join(dir, item.Name())
		meta, err := parseSkillFile(path)
		if err != nil {
			continue
		}
		name := strings.TrimSuffix(item.Name(), ".md")
		if meta.Name != "" {
			name = meta.Name
		}
		entryScope := scope
		if meta.Scope == "global" || meta.Scope == "project" {
			entryScope = meta.Scope
		}
		entries = append(entries, &SkillEntry{
			Name:    name,
			Meta:    meta,
			Path:    path,
			Scope:   entryScope,
			Staging: false,
		})
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

func parseSkillFile(path string) (SkillMeta, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return SkillMeta{}, err
	}
	return parseSkillFrontmatter(string(data))
}

func parseSkillFrontmatter(content string) (SkillMeta, error) {
	var meta SkillMeta
	content = strings.ReplaceAll(content, "\r\n", "\n")

	if len(content) < 7 {
		return meta, fmt.Errorf("content too short for frontmatter")
	}

	if !strings.HasPrefix(content, "---") {
		return meta, fmt.Errorf("no frontmatter found")
	}

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

func skillNameFromFile(filename string) string {
	name := strings.TrimSuffix(filename, ".md")

	if strings.HasPrefix(name, "knight-") {
		parts := strings.SplitN(name, "-", 3)
		if len(parts) >= 3 && len(parts[1]) == 8 {
			name = parts[2]
		}
	}
	return name
}
