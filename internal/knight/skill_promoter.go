package knight

import (
	"encoding/json"
	"fmt"
	"github.com/topcheer/ggcode/internal/debug"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/util"
	"gopkg.in/yaml.v3"
)

type Promoter struct {
	homeDir    string
	projectDir string
}

var safeSkillNamePattern = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

func NewPromoter(homeDir, projectDir string) *Promoter {
	return &Promoter{homeDir: homeDir, projectDir: projectDir}
}

func (p *Promoter) Promote(entry *SkillEntry) error {
	if entry == nil {
		return fmt.Errorf("skill entry is nil")
	}
	if !entry.Staging {
		return fmt.Errorf("skill %q is not in staging", entry.Name)
	}

	name, err := validateSkillName(entry.Name)
	if err != nil {
		return err
	}

	stagingRoot := p.stagingDir(entry.Scope)
	if stagingRoot == "" {
		return fmt.Errorf("unknown scope %q for staging skill %q", entry.Scope, name)
	}
	if !isSubPath(stagingRoot, entry.Path) {
		return fmt.Errorf("promote %q aborted: staging path %s is outside the %q staging directory (crossed index entry?)", name, entry.Path, entry.Scope)
	}

	targetDir := p.activeDir(entry)
	if targetDir == "" {
		return fmt.Errorf("cannot determine target directory for scope %q", entry.Scope)
	}

	skillDir := filepath.Join(targetDir, name)
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		return fmt.Errorf("create skill dir: %w", err)
	}

	targetPath := filepath.Join(skillDir, "SKILL.md")

	if _, err := os.Stat(targetPath); err == nil {
		if snapErr := p.createSnapshot(name, targetPath); snapErr != nil {

			return fmt.Errorf("promote %q aborted: snapshot of existing active version failed (refusing to overwrite without a rollback point): %w", entry.Name, snapErr)
		}
	}

	content, err := os.ReadFile(entry.Path)
	if err != nil {
		return fmt.Errorf("read staging skill: %w", err)
	}

	updated := updateTimestamps(string(content), time.Now())

	if err := util.AtomicWriteFile(targetPath, []byte(updated), 0644); err != nil {
		return fmt.Errorf("write active skill: %w", err)
	}

	if err := removeStagingSkill(entry.Path); err != nil {
		return err
	}

	p.appendChangelog("promote", name, entry.Scope, entry.Path)

	return nil
}

func isSubPath(root, path string) bool {
	cleanRoot := filepath.Clean(root)
	cleanPath := filepath.Clean(path)
	return strings.HasPrefix(cleanPath, cleanRoot+string(filepath.Separator)) && cleanPath != cleanRoot
}

func removeStagingSkill(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove staging skill: %w", err)
	}
	return nil
}

func (p *Promoter) Reject(entry *SkillEntry) error {

	if entry == nil {
		return fmt.Errorf("reject: nil skill entry")
	}
	if !entry.Staging {
		return fmt.Errorf("skill %q is not in staging", entry.Name)
	}

	if err := os.Remove(entry.Path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove staging skill: %w", err)
	}

	p.appendChangelog("reject", entry.Name, entry.Scope, entry.Path)
	return nil
}

func (p *Promoter) MigrateLooseActive(entry *SkillEntry) error {
	if entry == nil {
		return fmt.Errorf("skill entry is nil")
	}
	if entry.Staging {
		return fmt.Errorf("staging skill %q cannot be migrated as active", entry.Name)
	}
	if !strings.HasSuffix(entry.Path, ".md") || filepath.Base(entry.Path) == "SKILL.md" {
		return fmt.Errorf("skill %q is not a loose active markdown file", entry.Name)
	}
	name, err := validateSkillName(entry.Name)
	if err != nil {
		return err
	}
	targetDir := p.activeDir(entry)
	if targetDir == "" {
		return fmt.Errorf("cannot determine target directory for scope %q", entry.Scope)
	}
	skillDir := filepath.Join(targetDir, name)
	targetPath := filepath.Join(skillDir, "SKILL.md")
	if _, err := os.Stat(targetPath); err == nil {
		return fmt.Errorf("standard skill already exists at %s", targetPath)
	} else if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("check standard skill path: %w", err)
	}
	content, err := os.ReadFile(entry.Path)
	if err != nil {
		return fmt.Errorf("read loose skill: %w", err)
	}
	updated := updateTimestamps(string(content), time.Now())
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		return fmt.Errorf("create skill dir: %w", err)
	}
	if err := util.AtomicWriteFile(targetPath, []byte(updated), 0644); err != nil {
		return fmt.Errorf("write standard skill: %w", err)
	}
	if err := os.Remove(entry.Path); err != nil {
		debug.Log("knight", "remove loose skill %s: %v", entry.Path, err)
	}
	p.appendChangelog("migrate", entry.Name, entry.Scope, entry.Path)
	return nil
}

func (p *Promoter) Rollback(entry *SkillEntry) error {
	if entry == nil {
		return fmt.Errorf("skill entry is nil")
	}
	if entry.Staging {
		return fmt.Errorf("skill %q is in staging and cannot be rolled back", entry.Name)
	}
	name, err := validateSkillName(entry.Name)
	if err != nil {
		return err
	}

	snapshots, err := p.listSnapshots(name)
	if err != nil {
		return err
	}
	if len(snapshots) == 0 {
		return fmt.Errorf("no snapshots available for skill %q", entry.Name)
	}
	latest := snapshots[len(snapshots)-1]

	if _, err := os.Stat(entry.Path); err == nil {
		if snapErr := p.createSnapshot(entry.Name, entry.Path); snapErr != nil {

			return fmt.Errorf("rollback %q aborted: pre-rollback snapshot of current version failed (refusing to overwrite without a backup): %w", entry.Name, snapErr)
		}
	}

	data, err := os.ReadFile(latest)
	if err != nil {
		return fmt.Errorf("read snapshot: %w", err)
	}
	restored := updateTimestamps(string(data), time.Now())
	if err := util.AtomicWriteFile(entry.Path, []byte(restored), 0644); err != nil {
		return fmt.Errorf("write restored skill: %w", err)
	}
	p.appendChangelog("rollback", entry.Name, entry.Scope, latest)
	return nil
}

func (p *Promoter) WriteStaging(name, scope, content string) (string, error) {

	name, err := validateSkillName(name)
	if err != nil {
		return "", err
	}
	stagingDir := p.stagingDir(scope)
	if stagingDir == "" {
		return "", fmt.Errorf("unknown scope: %q", scope)
	}

	if err := os.MkdirAll(stagingDir, 0755); err != nil {
		return "", fmt.Errorf("create staging dir: %w", err)
	}

	filename := fmt.Sprintf("knight-%s-%s.md", time.Now().Format("20060102"), name)
	path := filepath.Join(stagingDir, filename)
	for i := 2; ; i++ {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			break
		} else if err != nil {
			return "", fmt.Errorf("check staging skill path: %w", err)
		}
		filename = fmt.Sprintf("knight-%s-%s-%d.md", time.Now().Format("20060102"), name, i)
		path = filepath.Join(stagingDir, filename)
	}

	if err := util.AtomicWriteFile(path, []byte(content), 0644); err != nil {
		return "", fmt.Errorf("write staging skill: %w", err)
	}

	p.appendChangelog("create", name, scope, path)
	return path, nil
}

func (p *Promoter) activeDir(entry *SkillEntry) string {
	if entry.Scope == "global" {
		return filepath.Join(p.homeDir, ".ggcode", "skills")
	}
	return filepath.Join(p.projectDir, ".ggcode", "skills")
}

func (p *Promoter) stagingDir(scope string) string {
	if scope == "global" {
		return filepath.Join(p.homeDir, ".ggcode", "skills-staging")
	}
	return filepath.Join(p.projectDir, ".ggcode", "skills-staging")
}

func (p *Promoter) createSnapshot(name, activePath string) error {
	name, err := validateSkillName(name)
	if err != nil {
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

	t := time.Now()
	base := name + "." + t.Format("20060102-150405")
	micros := t.Nanosecond() / 1000
	for i := 0; ; i++ {
		cand := fmt.Sprintf("%s%06d", base, (micros+i)%1_000_000)
		snapPath := filepath.Join(snapDir, cand+".md")
		if _, err := os.Stat(snapPath); os.IsNotExist(err) {
			return util.AtomicWriteFile(snapPath, data, 0644)
		}
		if i >= 64 {
			return fmt.Errorf("snapshot path collision storm for %s", name)
		}
	}
}

type changelogEntry struct {
	Time   time.Time `json:"time"`
	Action string    `json:"action"`
	Name   string    `json:"name"`
	Scope  string    `json:"scope"`
	Path   string    `json:"path"`
}

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
	if _, err := f.Write(append(line, '\n')); err != nil {
		debug.Log("knight-promoter", "failed to append changelog entry for %s: %v", name, err)
	}
}

func (p *Promoter) listSnapshots(name string) ([]string, error) {
	name, err := validateSkillName(name)
	if err != nil {
		return nil, err
	}
	snapDir := filepath.Join(p.projectDir, ".ggcode", "skills-snapshots")

	snapPattern := regexp.MustCompile(`^` + regexp.QuoteMeta(name) + `\.\d{8}-\d{6,12}\.md$`)
	des, err := os.ReadDir(snapDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	matches := make([]string, 0, len(des))
	for _, de := range des {
		if de.IsDir() || !snapPattern.MatchString(de.Name()) {
			continue
		}
		matches = append(matches, filepath.Join(snapDir, de.Name()))
	}
	sort.Strings(matches)
	return matches, nil
}

func validateSkillName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("skill name is empty")
	}
	if strings.Contains(name, "..") || strings.ContainsAny(name, `/\`) || !safeSkillNamePattern.MatchString(name) {
		return "", fmt.Errorf("unsafe skill name %q", name)
	}
	return name, nil
}

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

func splitFrontmatter(content string) int {
	if !strings.HasPrefix(content, "---") {
		return -1
	}
	rest := content[3:]

	if len(rest) > 0 && rest[0] == '\n' {
		rest = rest[1:]
	}
	idx := strings.Index(rest, "\n---")
	if idx < 0 {
		return -1
	}

	bodyStart := 3 + len(content[3:]) - len(rest) + idx + 4
	if bodyStart < len(content) && content[bodyStart] == '\n' {
		bodyStart++
	}
	return bodyStart
}

func extractFrontmatterText(content string, bodyStart int) string {

	openEnd := 3
	if openEnd < len(content) && content[openEnd] == '\n' {
		openEnd++
	}

	closeStart := bodyStart - 4
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
	return util.AtomicWriteFile(path, []byte(updated), info.Mode().Perm())
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

	return "---\n" + strings.TrimRight(string(newFM), "\n") + "\n---\n" + bodyText, nil
}
