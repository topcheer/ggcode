package knight

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Promoter handles moving skills from staging to active directories.
type Promoter struct {
	homeDir    string
	projectDir string
}

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

// WriteStaging writes a new skill to the appropriate staging directory.
func (p *Promoter) WriteStaging(name, scope, content string) (string, error) {
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

// updateTimestamps updates created_at and updated_at in frontmatter.
func updateTimestamps(content string, now time.Time) string {
	dateStr := now.Format("2006-01-02")
	lines := strings.Split(content, "\n")
	inFrontmatter := false
	fmEnd := 0

	for i, line := range lines {
		if i == 0 && strings.TrimSpace(line) == "---" {
			inFrontmatter = true
			continue
		}
		if inFrontmatter && strings.TrimSpace(line) == "---" {
			fmEnd = i
			break
		}
	}

	if fmEnd == 0 {
		return content // no frontmatter, return as-is
	}

	// Update or add updated_at, add created_at if missing
	hasCreated := false
	hasUpdated := false
	for i := 1; i < fmEnd; i++ {
		if strings.HasPrefix(lines[i], "updated_at:") {
			lines[i] = "updated_at: " + dateStr
			hasUpdated = true
		}
		if strings.HasPrefix(lines[i], "created_at:") {
			hasCreated = true
		}
	}

	// Insert missing fields before the closing ---
	insertAt := fmEnd
	if !hasUpdated {
		lines = append(lines[:insertAt], append([]string{"updated_at: " + dateStr}, lines[insertAt:]...)...)
		insertAt++
	}
	if !hasCreated {
		// Insert created_at before the closing ---
		lines = append(lines[:fmEnd], append([]string{"created_at: " + dateStr}, lines[fmEnd:]...)...)
	}

	return strings.Join(lines, "\n")
}
