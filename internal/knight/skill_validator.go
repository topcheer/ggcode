package knight

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// ValidationResult contains the outcome of skill validation.
type ValidationResult struct {
	Valid    bool     `json:"valid"`
	Errors   []string `json:"errors,omitempty"`
	Warnings []string `json:"warnings,omitempty"`
}

// ValidateSkill runs all validation checks on a skill entry.
func ValidateSkill(entry *SkillEntry) ValidationResult {
	var result ValidationResult

	// 1. Format check — required frontmatter fields
	result.checkFormat(entry)

	// 2. Duplicate check — compare with existing active skills
	// (caller should do this against the full index)

	// 3. Dependency check — required commands exist on PATH
	result.checkDependencies(entry)

	// 4. Content quality check — basic heuristics
	result.checkContentQuality(entry)

	result.Valid = len(result.Errors) == 0
	return result
}

// checkFormat verifies required frontmatter fields are present.
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

// checkDependencies verifies that required commands exist on PATH.
func (r *ValidationResult) checkDependencies(entry *SkillEntry) {
	for _, cmd := range entry.Meta.Requires {
		if _, err := exec.LookPath(cmd); err != nil {
			r.Warnings = append(r.Warnings, fmt.Sprintf("dependency not found on PATH: %s", cmd))
		}
	}
}

// checkContentQuality performs basic quality heuristics on the skill content.
func (r *ValidationResult) checkContentQuality(entry *SkillEntry) {
	// Read the full skill file to check content
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

	// Check for minimum content length
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

	// Check for at least one heading
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

	// Semantic quality: reject generic/trivial content that adds no value
	r.checkSemanticQuality(body)
}

// checkSemanticQuality rejects skills that are too generic to be useful.
// A skill must teach something specific, not describe basic tool usage.
func (r *ValidationResult) checkSemanticQuality(body string) {
	lower := strings.ToLower(body)

	// Trivial skill patterns: these add zero value
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
			// #767: substring hits on common English phrases ("edit a file",
			// "read file contents", "search for patterns") inside perfectly
			// legitimate skills (e.g. a safety rule "Never edit a file without
			// reading it first") used to be a hard Error: Valid=false, and 3
			// consecutive ticks auto-deleted the file. Word presence is not
			// triviality -- downgrade to a warning like the specificity check
			// below, and never let it alone trigger auto-reject.
			r.Warnings = append(r.Warnings, "skill content mentions basic tool usage (\""+pat+"\") without teaching a specific behavioral rule")
			break
		}
	}

	// Check for specificity: skill should mention at least one concrete thing
	// (file path, command name, function name, or tool name with context)
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

// CheckDuplicate returns true if a skill with similar name already exists.
func CheckDuplicate(entry *SkillEntry, existing []*SkillEntry) bool {
	name := strings.ToLower(entry.Name)
	desc := strings.ToLower(entry.Meta.Description)

	for _, e := range existing {
		if e.Staging {
			continue // don't compare with other staging skills
		}
		// #766: skills with the same name in different scopes are legal
		// (findSkillByRef's multi-match error implies coexistence). Without
		// the scope check, a global staging candidate named like an active
		// project skill was rejected as duplicate and deleted in one tick.
		if e.Scope != entry.Scope {
			continue
		}
		// Exact name match
		if strings.ToLower(e.Name) == name {
			return true
		}
		// High description similarity (simple substring check)
		existingDesc := strings.ToLower(e.Meta.Description)
		if len(desc) > 20 && len(existingDesc) > 20 {
			// Check if either description contains the other
			if strings.Contains(desc, existingDesc) || strings.Contains(existingDesc, desc) {
				return true
			}
		}
	}
	return false
}

// readSkillContent reads the full content of a skill file.
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
	// #985: prefix match instead of whole-line equality so common heading
	// variants like "## Steps:" or "## Steps Overview" are not misrejected
	// as missing. Headings like "## Pipeline - 5 steps" that do not start
	// with the canonical prefix still do not match (accepted boundary).
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
			// #985: prefix match (case-insensitive) to stay consistent with
			// hasSection — "## Steps Overview" etc. must count as the steps
			// section here too, otherwise valid variant headings pass the
			// presence check but fail the actionable-steps check.
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
