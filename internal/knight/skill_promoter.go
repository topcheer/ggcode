package knight

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Promoter handles moving skills from staging to active directories.
type Promoter struct {
	homeDir    string
	projectDir string
}

var safeSkillNamePattern = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// NewPromoter creates a skill promoter.
func NewPromoter(homeDir, projectDir string) *Promoter {
	return &Promoter{homeDir: homeDir, projectDir: projectDir}
}

// Promote moves a skill from staging to the active skills directory.
// It creates a snapshot of any existing skill with the same name for rollback.
func (p *Promoter) Promote(entry *SkillEntry) error {
	if !entry.Staging {
		return fmt.Errorf("skill %q is not in staging", entry.Name)
	}
	if err := validateSkillName(entry.Name); err != nil {
		return err
	}

	// Determine target directory
	targetDir := p.activeDir(entry)
	if targetDir == "" {
		return fmt.Errorf("cannot determine target directory for scope %q", entry.Scope)
	}

	// Ensure target directory exists
	skillDir := filepath.Join(targetDir, entry.Name)
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		return fmt.Errorf("create skill dir: %w", err)
	}

	targetPath := filepath.Join(skillDir, "SKILL.md")

	// Snapshot existing skill if present
	if _, err := os.Stat(targetPath); err == nil {
		if snapErr := p.createSnapshot(entry.Name, targetPath); snapErr != nil {
			// Non-fatal: log but continue
			fmt.Fprintf(os.Stderr, "knight: snapshot warning: %v\n", snapErr)
		}
	}

	// Read staging content
	content, err := os.ReadFile(entry.Path)
	if err != nil {
		return fmt.Errorf("read staging skill: %w", err)
	}

	// Update frontmatter timestamps
	updated := updateTimestamps(string(content), time.Now())

	// Write to active location
	if err := os.WriteFile(targetPath, []byte(updated), 0644); err != nil {
		return fmt.Errorf("write active skill: %w", err)
	}

	// Remove staging file
	if err := os.Remove(entry.Path); err != nil {
		// Non-fatal
		fmt.Fprintf(os.Stderr, "knight: remove staging: %v\n", err)
	}

	// Write changelog entry
	p.appendChangelog("promote", entry.Name, entry.Scope, entry.Path)

	return nil
}

// Reject removes a skill from staging.
func (p *Promoter) Reject(entry *SkillEntry) error {
	if !entry.Staging {
		return fmt.Errorf("skill %q is not in staging", entry.Name)
	}

	if err := os.Remove(entry.Path); err != nil {
		return fmt.Errorf("remove staging skill: %w", err)
	}

	p.appendChangelog("reject", entry.Name, entry.Scope, entry.Path)
	return nil
}

// Rollback restores the most recent snapshot for an active skill.
func (p *Promoter) Rollback(entry *SkillEntry) error {
	if entry == nil {
		return fmt.Errorf("skill entry is nil")
	}
	if entry.Staging {
		return fmt.Errorf("skill %q is in staging and cannot be rolled back", entry.Name)
	}
	if err := validateSkillName(entry.Name); err != nil {
		return err
	}

	snapshots, err := p.listSnapshots(entry.Name)
	if err != nil {
		return err
	}
	if len(snapshots) == 0 {
		return fmt.Errorf("no snapshots available for skill %q", entry.Name)
	}
	latest := snapshots[len(snapshots)-1]

	if _, err := os.Stat(entry.Path); err == nil {
		if snapErr := p.createSnapshot(entry.Name, entry.Path); snapErr != nil {
			fmt.Fprintf(os.Stderr, "knight: rollback snapshot warning: %v\n", snapErr)
		}
	}

	data, err := os.ReadFile(latest)
	if err != nil {
		return fmt.Errorf("read snapshot: %w", err)
	}
	restored := updateTimestamps(string(data), time.Now())
	if err := os.WriteFile(entry.Path, []byte(restored), 0644); err != nil {
		return fmt.Errorf("write restored skill: %w", err)
	}
	p.appendChangelog("rollback", entry.Name, entry.Scope, latest)
	return nil
}

// WriteStaging writes a new skill to the appropriate staging directory.
func (p *Promoter) WriteStaging(name, scope, content string) (string, error) {
	if err := validateSkillName(name); err != nil {
		return "", err
	}
	stagingDir := p.stagingDir(scope)
	if stagingDir == "" {
		return "", fmt.Errorf("unknown scope: %q", scope)
	}

	if err := os.MkdirAll(stagingDir, 0755); err != nil {
		return "", fmt.Errorf("create staging dir: %w", err)
	}

	// Generate filename with date prefix
	filename := fmt.Sprintf("knight-%s-%s.md", time.Now().Format("20060102"), name)
	path := filepath.Join(stagingDir, filename)

	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return "", fmt.Errorf("write staging skill: %w", err)
	}

	p.appendChangelog("create", name, scope, path)
	return path, nil
}

// activeDir returns the active skills directory for a given scope.
func (p *Promoter) activeDir(entry *SkillEntry) string {
	if entry.Scope == "global" {
		return filepath.Join(p.homeDir, ".ggcode", "skills")
	}
	return filepath.Join(p.projectDir, ".ggcode", "skills")
}

// stagingDir returns the staging directory for a given scope.
func (p *Promoter) stagingDir(scope string) string {
	if scope == "global" {
		return filepath.Join(p.homeDir, ".ggcode", "skills-staging")
	}
	return filepath.Join(p.projectDir, ".ggcode", "skills-staging")
}

// createSnapshot copies the current active skill to the snapshots directory.
func (p *Promoter) createSnapshot(name, activePath string) error {
	if err := validateSkillName(name); err != nil {
		return err
	}
	snapDir := filepath.Join(p.projectDir, ".ggcode", "skills-snapshots")
	if err := os.MkdirAll(snapDir, 0755); err != nil {
		return err
	}

	data, err := os.ReadFile(activePath)
	if err != nil {
		return err
	}

	snapPath := filepath.Join(snapDir, name+"."+time.Now().Format("20060102-150405")+".md")
	return os.WriteFile(snapPath, data, 0644)
}

// changelogEntry is one record in the skills changelog.
type changelogEntry struct {
	Time   time.Time `json:"time"`
	Action string    `json:"action"` // "create", "promote", "reject"
	Name   string    `json:"name"`
	Scope  string    `json:"scope"`
	Path   string    `json:"path"`
}

// appendChangelog adds an entry to the skills changelog.
func (p *Promoter) appendChangelog(action, name, scope, path string) {
	clPath := filepath.Join(p.projectDir, ".ggcode", "skills-changelog.jsonl")

	entry := changelogEntry{
		Time:   time.Now(),
		Action: action,
		Name:   name,
		Scope:  scope,
		Path:   path,
	}

	line, err := json.Marshal(entry)
	if err != nil {
		return
	}

	f, err := os.OpenFile(clPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return
	}
	defer f.Close()
	f.Write(append(line, '\n'))
}

func (p *Promoter) listSnapshots(name string) ([]string, error) {
	if err := validateSkillName(name); err != nil {
		return nil, err
	}
	pattern := filepath.Join(p.projectDir, ".ggcode", "skills-snapshots", name+".*.md")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return nil, err
	}
	sort.Strings(matches)
	return matches, nil
}

func validateSkillName(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("skill name is empty")
	}
	if strings.Contains(name, "..") || strings.ContainsAny(name, `/\`) || !safeSkillNamePattern.MatchString(name) {
		return fmt.Errorf("unsafe skill name %q", name)
	}
	return nil
}

// updateTimestamps updates the skill's YAML frontmatter with proper timestamps.
// Uses yaml.Unmarshal/Marshal for safe handling of multi-line and special characters.
func updateTimestamps(content string, now time.Time) string {
	dateStr := now.Format("2006-01-02")
	updated, err := mutateSkillFrontmatter(content, func(fmMap map[string]interface{}) {
		fmMap["updated_at"] = dateStr
		if _, ok := fmMap["created_at"]; !ok {
			fmMap["created_at"] = dateStr
		}
	})
	if err != nil {
		return content
	}
	return updated
}

// splitFrontmatter returns the byte offset where the body starts (after closing ---).
// Returns -1 if no valid frontmatter found.
func splitFrontmatter(content string) int {
	if !strings.HasPrefix(content, "---") {
		return -1
	}
	rest := content[3:]
	// Skip optional newline after opening ---
	if len(rest) > 0 && rest[0] == '\n' {
		rest = rest[1:]
	}
	idx := strings.Index(rest, "\n---")
	if idx < 0 {
		return -1
	}
	// Body starts after the closing --- and its trailing newline
	bodyStart := 3 + len(content[3:]) - len(rest) + idx + 4 // 4 = len("\n---")
	if bodyStart < len(content) && content[bodyStart] == '\n' {
		bodyStart++
	}
	return bodyStart
}

// extractFrontmatterText extracts the YAML text between --- markers.
func extractFrontmatterText(content string, bodyStart int) string {
	// Find opening --- end
	openEnd := 3
	if openEnd < len(content) && content[openEnd] == '\n' {
		openEnd++
	}
	// Find closing --- start
	closeStart := bodyStart - 4 // 4 = len("\n---")
	if closeStart > openEnd {
		return strings.TrimSpace(content[openEnd:closeStart])
	}
	return ""
}

func updateSkillFrontmatter(path string, mutate func(map[string]interface{})) error {
	content, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	updated, err := mutateSkillFrontmatter(string(content), mutate)
	if err != nil {
		return err
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, []byte(updated), info.Mode().Perm()); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

func mutateSkillFrontmatter(content string, mutate func(map[string]interface{})) (string, error) {
	bodyStart := splitFrontmatter(content)
	if bodyStart < 0 {
		return "", fmt.Errorf("no valid frontmatter found")
	}

	fmText := extractFrontmatterText(content, bodyStart)
	bodyText := content[bodyStart:]

	var fmMap map[string]interface{}
	if err := yaml.Unmarshal([]byte(fmText), &fmMap); err != nil {
		return "", err
	}
	if fmMap == nil {
		fmMap = make(map[string]interface{})
	}
	mutate(fmMap)

	newFM, err := yaml.Marshal(fmMap)
	if err != nil {
		return "", err
	}
	return "---\n" + strings.TrimRight(string(newFM), "\n") + "\n---" + bodyText, nil
}
