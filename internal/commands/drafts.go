package commands

// Skill draft lifecycle: discovery and activation of auto-distilled draft
// skills. The distiller (internal/agent/skill_distill.go) writes drafts to
// <projectDir>/.ggcode/skill-drafts/ as flat SKILL.md-style files that the
// live skill registry deliberately does NOT load - machine-generated skills
// must be human-reviewed before activation (Agent Skills open standard,
// Dec 2025: "install skills only from trusted sources; audit before use").
//
// This file closes that loop: ListSkillDrafts surfaces pending drafts (used
// by the /skills panel), and ActivateSkillDraft relocates a reviewed draft
// into the live project registry (.ggcode/skills/<name>/SKILL.md) where the
// normal loader picks it up on the next refresh.

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

// SkillDraftsDirName is the project-local directory holding draft skills.
// Must match skillDraftDirName in internal/agent/skill_distill.go.
const SkillDraftsDirName = "skill-drafts"

// SkillDraft describes one pending draft skill file.
type SkillDraft struct {
	Name        string // frontmatter name, or file base name as fallback
	Description string
	Status      string
	Path        string
}

// SkillDraftsDir returns the draft directory for a project.
func SkillDraftsDir(projectDir string) string {
	return filepath.Join(projectDir, ".ggcode", SkillDraftsDirName)
}

// ListSkillDrafts scans the project draft directory and returns pending
// drafts sorted by path. A missing directory yields nil (no drafts).
func ListSkillDrafts(projectDir string) []SkillDraft {
	dir := SkillDraftsDir(projectDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var drafts []SkillDraft
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			debug.Log("skills", "read draft %s: %v", path, err)
			continue
		}
		_, meta := parseCommandMarkdown(string(data))
		name := strings.TrimSpace(meta.Name)
		if name == "" {
			name = strings.TrimSuffix(e.Name(), ".md")
		}
		drafts = append(drafts, SkillDraft{
			Name:        name,
			Description: strings.TrimSpace(meta.Description),
			Status:      strings.TrimSpace(meta.Status),
			Path:        path,
		})
	}
	if len(drafts) == 0 {
		return nil
	}
	sort.Slice(drafts, func(i, j int) bool {
		return drafts[i].Path < drafts[j].Path
	})
	return drafts
}

// ActivateSkillDraft moves a reviewed draft into the live project skill
// registry: <projectDir>/.ggcode/skills/<name>/SKILL.md. The draft content
// is preserved byte-for-byte (the review edit happened in the draft file);
// unknown frontmatter keys such as procedure-hash/status are ignored by the
// loader. The draft file is removed after a successful move. Activation
// never overwrites an existing skill.
func ActivateSkillDraft(projectDir, draftPath string) (string, error) {
	data, err := os.ReadFile(draftPath)
	if err != nil {
		return "", fmt.Errorf("read draft: %w", err)
	}
	_, meta := parseCommandMarkdown(string(data))
	name := sanitizeSkillDirName(meta.Name)
	if name == "" {
		name = sanitizeSkillDirName(strings.TrimSuffix(filepath.Base(draftPath), ".md"))
	}
	if name == "" {
		return "", fmt.Errorf("draft %s has no usable skill name", filepath.Base(draftPath))
	}

	targetDir := filepath.Join(projectDir, ".ggcode", "skills", name)
	if _, err := os.Stat(targetDir); err == nil {
		return name, fmt.Errorf("skill %q already exists in .ggcode/skills", name)
	} else if !os.IsNotExist(err) {
		return name, fmt.Errorf("stat %s: %w", targetDir, err)
	}
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return name, fmt.Errorf("create %s: %w", targetDir, err)
	}
	// tmp+rename so the hot-reload watcher never observes a partial skill.
	tmp := filepath.Join(targetDir, "SKILL.md.tmp")
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return name, fmt.Errorf("write skill: %w", err)
	}
	if err := os.Rename(tmp, filepath.Join(targetDir, "SKILL.md")); err != nil {
		_ = os.Remove(tmp)
		return name, fmt.Errorf("finalize skill: %w", err)
	}
	if err := os.Remove(draftPath); err != nil {
		// The skill is live; a leftover draft only means a retry reports
		// "already exists". Log it and keep the success.
		debug.Log("skills", "activated %s but draft cleanup failed: %v", name, err)
	}
	return name, nil
}

// sanitizeSkillDirName restricts a skill name to a safe single path element
// (the loader uses the directory name as the skill name).
func sanitizeSkillDirName(name string) string {
	name = strings.TrimSpace(name)
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	return strings.Trim(b.String(), "-.")
}
