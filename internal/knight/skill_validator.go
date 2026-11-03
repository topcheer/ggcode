package knight

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
)

type ValidationResult struct {
	Valid    bool     `json:"valid"`
	Errors   []string `json:"errors,omitempty"`
	Warnings []string `json:"warnings,omitempty"`
}

func ValidateSkill(entry *SkillEntry) ValidationResult {
	var result ValidationResult
	if entry == nil {

		result.Errors = append(result.Errors, "skill entry is nil")
		return result
	}

	result.checkFormat(entry)

	result.checkDependencies(entry)

	result.checkContentQuality(entry)

	result.Valid = len(result.Errors) == 0
	return result
}

func (r *ValidationResult) checkFormat(entry *SkillEntry) {
	if entry == nil {
		r.Errors = append(r.Errors, "skill entry is nil")
		return
	}
	m := entry.Meta

	if m.Name == "" {
		r.Errors = append(r.Errors, "missing required field: name")
	}
	if m.Description == "" {
		r.Errors = append(r.Errors, "missing required field: description")
	}
	if m.Scope == "" {
		r.Warnings = append(r.Warnings, "scope not set, defaulting to project")
	} else if m.Scope != "global" && m.Scope != "project" {
		r.Errors = append(r.Errors, fmt.Sprintf("invalid scope %q (expected global or project)", m.Scope))
	}
	if entry.Name != "" && m.Name != "" && entry.Name != m.Name {
		r.Errors = append(r.Errors, fmt.Sprintf("frontmatter name %q does not match skill name %q", m.Name, entry.Name))
	}
	if entry.Scope != "" && m.Scope != "" && entry.Scope != m.Scope {
		r.Errors = append(r.Errors, fmt.Sprintf("frontmatter scope %q does not match indexed scope %q", m.Scope, entry.Scope))
	}
	if shouldCheckActiveSkillDir(entry.Path, entry.Staging) && entry.Name != "" {
		dirName := filepath.Base(filepath.Dir(entry.Path))
		if dirName != entry.Name {
			r.Errors = append(r.Errors, fmt.Sprintf("active skill directory %q does not match skill name %q", dirName, entry.Name))
		}
	}
	if m.CreatedBy == "" {
		r.Warnings = append(r.Warnings, "created_by not set")
	} else if !isAllowedCreatedBy(m.CreatedBy) {
		r.Warnings = append(r.Warnings, fmt.Sprintf("created_by %q is unusual (expected user, knight, or agent)", m.CreatedBy))
	}
}

func (r *ValidationResult) checkDependencies(entry *SkillEntry) {
	for _, cmd := range entry.Meta.Requires {
		if _, err := exec.LookPath(cmd); err != nil {
			r.Warnings = append(r.Warnings, fmt.Sprintf("dependency not found on PATH: %s", cmd))
		}
	}
}

func (r *ValidationResult) checkContentQuality(entry *SkillEntry) {

	data, err := readSkillContent(entry.Path)
	if err != nil {
		r.Errors = append(r.Errors, fmt.Sprintf("cannot read skill content: %v", err))
		return
	}
	content := string(data)
	body := strings.TrimSpace(content)
	if bodyStart := splitFrontmatter(content); bodyStart >= 0 {
		body = strings.TrimSpace(content[bodyStart:])
	}
	if body == "" {
		r.Errors = append(r.Errors, "skill body is empty")
		return
	}

	lines := strings.Split(body, "\n")
	contentLines := 0
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" && !strings.HasPrefix(trimmed, "#") {
			contentLines++
		}
	}
	if contentLines < 3 {
		r.Warnings = append(r.Warnings, "skill content is very short (< 3 non-header lines)")
	}

	hasHeading := false
	for _, line := range lines {
		if strings.HasPrefix(line, "# ") || strings.HasPrefix(line, "## ") {
			hasHeading = true
			break
		}
	}
	if !hasHeading {
		r.Warnings = append(r.Warnings, "skill should have at least one markdown heading")
	}
	if !hasSection(lines, "## Steps") {
		r.Errors = append(r.Errors, "missing required '## Steps' section")
	} else if !hasActionableSteps(lines) {
		r.Errors = append(r.Errors, "steps section does not contain actionable list items")
	}
	if !hasSection(lines, "## When to Use") {
		r.Warnings = append(r.Warnings, "skill should document '## When to Use'")
	}
	if !strings.Contains(strings.ToLower(body), "when not to use") {
		r.Warnings = append(r.Warnings, "skill should explain when not to use it")
	}
	if hasUnresolvedMarkers(body) {
		r.Warnings = append(r.Warnings, "skill contains TODOs or unresolved placeholders")
	}

	r.checkSemanticQuality(body)
}

func (r *ValidationResult) checkSemanticQuality(body string) {
	lower := strings.ToLower(body)

	trivialPatterns := []string{
		"run commands sequentially",
		"execute a series of commands",
		"run multiple commands",
		"read file contents",
		"edit a file",
		"search for patterns",
	}
	for _, pat := range trivialPatterns {
		if strings.Contains(lower, pat) {

			r.Warnings = append(r.Warnings, "skill content mentions basic tool usage (\""+pat+"\") without teaching a specific behavioral rule")
			break
		}
	}

	hasConcrete := false
	concretePatterns := []string{
		"internal/", "cmd/", "src/", "pkg/",
		".go", ".ts", ".py", ".rs",
		"make ", "go test", "go build", "npm ",
		"config.", "debug.", "knight.",
		"bool", "zero value", "default",
		"permission", "mode",
	}
	for _, pat := range concretePatterns {
		if strings.Contains(lower, pat) {
			hasConcrete = true
			break
		}
	}
	if !hasConcrete {
		r.Warnings = append(r.Warnings, "skill lacks concrete details (file paths, commands, or code references): may be too abstract")
	}
}

func CheckDuplicate(entry *SkillEntry, existing []*SkillEntry) bool {
	name := strings.ToLower(entry.Name)
	desc := strings.ToLower(entry.Meta.Description)

	for _, e := range existing {
		if e.Staging {
			continue
		}

		if e.Scope != entry.Scope {
			continue
		}

		if strings.ToLower(e.Name) == name {
			return true
		}

		existingDesc := strings.ToLower(e.Meta.Description)
		if len(desc) > 20 && len(existingDesc) > 20 {
			threshold := 0.75

			if cjkRatio(tokenSet(desc)) >= 0.5 {
				threshold = 0.85
			}
			if jaccardSimilarity(tokenSet(desc), tokenSet(existingDesc)) >= threshold {
				return true
			}
		}
	}
	return false
}

func cjkRatio(set map[string]struct{}) float64 {
	if len(set) == 0 {
		return 0
	}
	cjk := 0
	for t := range set {
		rs := []rune(t)
		if len(rs) == 1 {
			r := rs[0]
			if unicode.Is(unicode.Han, r) || unicode.Is(unicode.Hangul, r) ||
				unicode.Is(unicode.Hiragana, r) || unicode.Is(unicode.Katakana, r) {
				cjk++
			}
		}
	}
	return float64(cjk) / float64(len(set))
}

func tokenSet(desc string) map[string]struct{} {
	set := make(map[string]struct{})
	for _, w := range strings.Fields(desc) {
		set[w] = struct{}{}
	}

	for _, r := range desc {
		if unicode.Is(unicode.Han, r) ||
			unicode.Is(unicode.Hangul, r) ||
			unicode.Is(unicode.Hiragana, r) ||
			unicode.Is(unicode.Katakana, r) ||
			(r >= 0xFF01 && r <= 0xFF5E) {
			set[string(r)] = struct{}{}
		}
	}
	return set
}

func readSkillContent(path string) ([]byte, error) {
	return os.ReadFile(path)
}

func shouldCheckActiveSkillDir(path string, staging bool) bool {
	if staging {
		return false
	}
	clean := filepath.ToSlash(path)
	return strings.Contains(clean, "/.ggcode/skills/") && strings.HasSuffix(clean, "/SKILL.md")
}

func isAllowedCreatedBy(createdBy string) bool {
	switch strings.ToLower(strings.TrimSpace(createdBy)) {
	case "user", "knight", "agent":
		return true
	default:
		return false
	}
}

func hasSection(lines []string, heading string) bool {

	want := strings.ToLower(strings.TrimSpace(heading))
	for _, line := range lines {
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(line)), want) {
			return true
		}
	}
	return false
}

func hasActionableSteps(lines []string) bool {
	inSteps := false
	stepPattern := regexp.MustCompile(`^\s*(?:[-*]|\d+\.)\s+\S+`)
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {

			inSteps = strings.HasPrefix(strings.ToLower(trimmed), "## steps")
			continue
		}
		if inSteps && stepPattern.MatchString(trimmed) {
			return true
		}
	}
	return false
}

func hasUnresolvedMarkers(body string) bool {
	lower := strings.ToLower(body)
	return strings.Contains(lower, "todo") || strings.Contains(body, "{{") || strings.Contains(body, "}}")
}
