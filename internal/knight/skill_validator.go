package knight

import (
	"fmt"
	"os"
	"os/exec"
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
	m := entry.Meta

	if m.Name == "" {
		r.Errors = append(r.Errors, "missing required field: name")
	}
	if m.Description == "" {
		r.Errors = append(r.Errors, "missing required field: description")
	}
	if m.Scope == "" {
		r.Warnings = append(r.Warnings, "scope not set, defaulting to project")
	}
	if m.CreatedBy == "" {
		r.Warnings = append(r.Warnings, "created_by not set")
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

	// Check for minimum content length
	lines := strings.Split(content, "\n")
	contentLines := 0
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" && !strings.HasPrefix(trimmed, "---") && !strings.HasPrefix(trimmed, "#") {
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
}

// CheckDuplicate returns true if a skill with similar name already exists.
func CheckDuplicate(entry *SkillEntry, existing []*SkillEntry) bool {
	name := strings.ToLower(entry.Name)
	desc := strings.ToLower(entry.Meta.Description)

	for _, e := range existing {
		if e.Staging {
			continue // don't compare with other staging skills
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
