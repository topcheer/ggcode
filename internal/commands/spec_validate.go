package commands

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Agent Skills open-standard validation (agentskills.io, December 2025).
//
// The open standard defines exactly six portable frontmatter fields; anything
// outside that set is rejected outright by strict consumers (claude.ai skill
// upload, Skills API packaging). ggcode's loader is lenient and accepts both
// the portable set and ggcode/Claude-Code-style extensions, but skill authors
// have had no way to discover portability landmines, malformed frontmatter,
// or scope-override surprises. `ggcode skills validate` surfaces them.
//
// Evidence base (sa-46 research):
//   - https://agentskills.io/specification (portable fields, limits)
//   - https://sureprompts.com/blog/claude-skills-guide-2026 ("Including a
//     field outside that set does not get silently ignored; packaging or
//     upload fails with an explicit error")

// SkillSeverity classifies a validation finding.
type SkillSeverity string

const (
	SkillError   SkillSeverity = "error"
	SkillWarning SkillSeverity = "warning"
	SkillInfo    SkillSeverity = "info"
)

// SkillIssue is a single validation finding for one skill.
type SkillIssue struct {
	Severity SkillSeverity `json:"severity"`
	Skill    string        `json:"skill"`
	Path     string        `json:"path"`
	Field    string        `json:"field,omitempty"`
	Message  string        `json:"message"`
}

// SkillValidationReport groups the findings for one skill folder.
type SkillValidationReport struct {
	Skill        string       `json:"skill"`
	Path         string       `json:"path"`
	Source       string       `json:"source"`
	OverriddenBy string       `json:"overridden_by,omitempty"`
	Issues       []SkillIssue `json:"issues"`
}

// specPortableFields is the closed set of fields the open standard carries
// across every conforming tool.
var specPortableFields = map[string]bool{
	"name":          true,
	"description":   true,
	"license":       true,
	"compatibility": true,
	"metadata":      true,
	"allowed-tools": true,
}

// knownExtensionFields are fields ggcode's loader understands beyond the
// portable set. They are valid for ggcode but hurt portability.
var knownExtensionFields = map[string]bool{
	"argument-hint":            true,
	"arguments":                true,
	"when_to_use":              true,
	"requires-tools":           true,
	"dependencies":             true,
	"version":                  true,
	"user-invocable":           true,
	"disable-model-invocation": true,
	"context":                  true,
}

const (
	specMaxNameLen    = 64
	specMaxDescLen    = 1024
	specMaxCompatLen  = 500
	specSkillFileName = "SKILL.md"
)

var specNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
var specSemverPattern = regexp.MustCompile(`^\d+\.\d+\.\d+(-[0-9A-Za-z.-]+)?$`)

// extractSkillFrontmatter isolates the raw YAML frontmatter block, mirroring
// the delimiter handling of parseCommandMarkdown but returning enough
// information for validation to distinguish "no frontmatter" from "broken".
func extractSkillFrontmatter(content string) (raw string, present bool, err error) {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	if !strings.HasPrefix(content, "---\n") {
		return "", false, nil
	}
	rest := strings.TrimPrefix(content, "---\n")
	idx := strings.Index(rest, "\n---\n")
	if idx < 0 {
		return "", false, fmt.Errorf("frontmatter block is not closed with a --- line")
	}
	return rest[:idx], true, nil
}

// validateSkillMarkdown validates one SKILL.md file. It returns the parsed
// frontmatter (when parsing succeeds) plus the findings. ok=false means the
// file is broken badly enough that field-level checks were skipped.
func validateSkillMarkdown(skillName, path string) (fm frontmatter, issues []SkillIssue, ok bool) {
	fail := func(sev SkillSeverity, field, msg string) {
		issues = append(issues, SkillIssue{Severity: sev, Skill: skillName, Path: path, Field: field, Message: msg})
	}
	data, readErr := os.ReadFile(path)
	if readErr != nil {
		fail(SkillError, "", fmt.Sprintf("cannot read %s: %v", specSkillFileName, readErr))
		return fm, issues, false
	}
	raw, present, fmErr := extractSkillFrontmatter(string(data))
	if !present {
		fail(SkillError, "", "missing YAML frontmatter block (--- delimited)")
		return fm, issues, false
	}
	var rawFields map[string]any
	if rawErr := yaml.Unmarshal([]byte(raw), &rawFields); rawErr != nil {
		fail(SkillError, "", fmt.Sprintf("invalid YAML frontmatter: %v", rawErr))
		return fm, issues, false
	}
	if fmErr != nil {
		fail(SkillError, "", fmt.Sprintf("invalid YAML frontmatter: %v", fmErr))
		return fm, issues, false
	}
	if err := yaml.Unmarshal([]byte(raw), &fm); err != nil {
		// Struct-typed parse failed (e.g. metadata values are not strings)
		// while the raw map parsed: report per-field rather than bailing.
		fail(SkillError, "", fmt.Sprintf("frontmatter fields have unexpected types: %v", err))
		return fm, issues, false
	}

	// Field-set portability: the open standard's closed six-field set.
	fields := make([]string, 0, len(rawFields))
	for k := range rawFields {
		fields = append(fields, k)
	}
	sort.Strings(fields)
	for _, f := range fields {
		switch {
		case specPortableFields[f]:
		case knownExtensionFields[f]:
			issues = append(issues, SkillIssue{
				Severity: SkillInfo, Skill: skillName, Path: path, Field: f,
				Message: "extension field: valid for ggcode but outside the portable Agent Skills set (strict tools reject packaging)",
			})
		default:
			issues = append(issues, SkillIssue{
				Severity: SkillWarning, Skill: skillName, Path: path, Field: f,
				Message: "unknown field: not in the portable Agent Skills set; strict consumers (claude.ai upload, Skills API) fail packaging",
			})
		}
	}

	// name: lowercase alphanumerics and hyphens, <=64 chars (spec).
	name := strings.TrimSpace(fm.Name)
	if name == "" {
		fail(SkillWarning, "name", "empty; directory name will be used as fallback")
	} else if len(name) > specMaxNameLen {
		fail(SkillError, "name", fmt.Sprintf("%d chars exceeds the %d-char limit", len(name), specMaxNameLen))
	} else if !specNamePattern.MatchString(name) {
		fail(SkillError, "name", fmt.Sprintf("%q: must be lowercase letters, digits, and hyphens", name))
	} else if name != skillName {
		fail(SkillWarning, "name", fmt.Sprintf("%q does not match directory name %q (directory name wins for command routing)", name, skillName))
	}

	// description: the routing rule; spec caps it at 1024 chars.
	if strings.TrimSpace(fm.Description) == "" {
		fail(SkillWarning, "description", "empty; the description is the only field the model sees when routing, so the skill may never fire")
	} else if len(fm.Description) > specMaxDescLen {
		fail(SkillError, "description", fmt.Sprintf("%d chars exceeds the %d-char limit", len(fm.Description), specMaxDescLen))
	}

	// compatibility: freeform string, <=500 chars per spec.
	if compat := strings.TrimSpace(fm.Compatibility); len(compat) > specMaxCompatLen {
		fail(SkillError, "compatibility", fmt.Sprintf("%d chars exceeds the %d-char limit", len(compat), specMaxCompatLen))
	}

	// allowed-tools entries must be non-empty.
	for i, t := range fm.AllowedTools {
		if strings.TrimSpace(t) == "" {
			fail(SkillWarning, "allowed-tools", fmt.Sprintf("entry %d is empty", i+1))
		}
	}

	// version: ggcode documents this as semantic version.
	if v := strings.TrimSpace(fm.Version); v != "" && !specSemverPattern.MatchString(v) {
		fail(SkillWarning, "version", fmt.Sprintf("%q is not semantic versioning (e.g. 1.0.0)", v))
	}

	return fm, issues, true
}

// validateSkillDir validates one skill folder (<dir>/SKILL.md).
func validateSkillDir(skillName, dir, source string) *SkillValidationReport {
	path := filepath.Join(dir, specSkillFileName)
	report := &SkillValidationReport{Skill: skillName, Path: dir, Source: source}
	if _, statErr := os.Stat(path); statErr != nil {
		report.Issues = append(report.Issues, SkillIssue{
			Severity: SkillError, Skill: skillName, Path: dir,
			Message: fmt.Sprintf("missing %s: folder is not a valid skill", specSkillFileName),
		})
		return report
	}
	_, issues, _ := validateSkillMarkdown(skillName, path)
	report.Issues = issues
	return report
}

// validateSkillsRoot scans a directory of skill folders.
func validateSkillsRoot(root, source string) []*SkillValidationReport {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var reports []*SkillValidationReport
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		reports = append(reports, validateSkillDir(strings.TrimSpace(entry.Name()), filepath.Join(root, entry.Name()), source))
	}
	sort.Slice(reports, func(i, j int) bool { return reports[i].Skill < reports[j].Skill })
	return reports
}

// ValidateSkillPath validates an explicit path. A directory containing
// SKILL.md is treated as a single skill; any other directory is treated as a
// skills root containing skill folders.
func ValidateSkillPath(path string) []*SkillValidationReport {
	if st, err := os.Stat(path); err == nil && st.IsDir() {
		if _, err := os.Stat(filepath.Join(path, specSkillFileName)); err == nil {
			return []*SkillValidationReport{validateSkillDir(filepath.Base(path), path, "explicit")}
		}
		return validateSkillsRoot(path, "explicit")
	}
	if filepath.Base(path) == specSkillFileName {
		path = filepath.Dir(path)
		return []*SkillValidationReport{validateSkillDir(filepath.Base(path), path, "explicit")}
	}
	return []*SkillValidationReport{{
		Skill: filepath.Base(path), Path: path, Source: "explicit",
		Issues: []SkillIssue{{Severity: SkillError, Skill: filepath.Base(path), Path: path,
			Message: "path not found (pass a skill folder or a directory of skill folders)"}},
	}}
}

// ValidateSkillScopes validates every skill visible to a project: user
// (~/.agents/skills, ~/.ggcode/skills) then project (.ggcode/skills), in the
// same precedence order the runtime loader uses. It adds cross-scope checks
// the single-path mode cannot do: override detection, dependency resolution,
// and requires-tools PATH lookups.
func ValidateSkillScopes(projectDir string) []*SkillValidationReport {
	l := NewLoader(projectDir)
	var reports []*SkillValidationReport
	firstSeen := make(map[string]*SkillValidationReport)
	known := make(map[string]bool)
	for _, target := range l.targets {
		if target.LoadedFrom != LoadedFromSkills {
			continue
		}
		for _, report := range validateSkillsRoot(target.Dir, string(target.Source)) {
			if prev, ok := firstSeen[report.Skill]; ok {
				// Later scopes win at runtime: the earlier report is the
				// shadowed one and points at its winner; the later report
				// carries an info issue naming what it shadows.
				prev.OverriddenBy = report.Path
				report.Issues = append(report.Issues, SkillIssue{
					Severity: SkillInfo, Skill: report.Skill, Path: report.Path,
					Message: fmt.Sprintf("shadows %s (later scopes override earlier ones at runtime)", prev.Path),
				})
			} else {
				firstSeen[report.Skill] = report
			}
			reports = append(reports, report)
			known[report.Skill] = true
		}
	}

	for _, report := range reports {
		path := filepath.Join(report.Path, specSkillFileName)
		fm, issues, ok := validateSkillMarkdown(report.Skill, path)
		if !ok {
			continue
		}
		// Merge field-level findings with the folder-level ones from
		// validateSkillsRoot (which only checked SKILL.md presence).
		report.Issues = append(report.Issues, issues...)
		for _, dep := range fm.Dependencies {
			if dep = strings.TrimSpace(dep); dep != "" && !known[dep] {
				report.Issues = append(report.Issues, SkillIssue{
					Severity: SkillWarning, Skill: report.Skill, Path: path, Field: "dependencies",
					Message: fmt.Sprintf("dependency %q not found in any scope", dep),
				})
			}
		}
		for _, toolName := range fm.RequiresTools {
			if toolName = strings.TrimSpace(toolName); toolName != "" {
				if _, err := exec.LookPath(toolName); err != nil {
					report.Issues = append(report.Issues, SkillIssue{
						Severity: SkillWarning, Skill: report.Skill, Path: path, Field: "requires-tools",
						Message: fmt.Sprintf("%q not found on PATH", toolName),
					})
				}
			}
		}
	}
	return reports
}

// CountSevere totals errors and warnings across reports.
func CountSevere(reports []*SkillValidationReport) (errors, warnings int) {
	for _, r := range reports {
		for _, issue := range r.Issues {
			switch issue.Severity {
			case SkillError:
				errors++
			case SkillWarning:
				warnings++
			}
		}
	}
	return errors, warnings
}
