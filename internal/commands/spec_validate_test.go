package commands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeSkill(t *testing.T, dir, name, frontmatter, body string) string {
	t.Helper()
	skillDir := filepath.Join(dir, name)
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("mkdir skill dir: %v", err)
	}
	content := "---\n" + frontmatter + "\n---\n\n" + body + "\n"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}
	return skillDir
}

func TestValidateSkillMarkdown_PortableSpecSkill(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(writeSkill(t, dir, "deploy-check",
		"name: deploy-check\n"+
			"description: Runs the pre-merge deploy checklist. Use when asked to ship or deploy.\n"+
			"license: MIT\n"+
			"compatibility: Requires git and make on PATH.\n"+
			"metadata:\n  owner: platform\n  tier: ga\n"+
			"allowed-tools:\n  - Read\n  - Bash(git log:*)\n",
		"# Deploy Check\n\n1. make verify-ci\n"), "SKILL.md")

	_, issues, ok := validateSkillMarkdown("deploy-check", path)
	if !ok {
		t.Fatalf("expected ok=true, issues=%+v", issues)
	}
	for _, issue := range issues {
		if issue.Severity == SkillError || issue.Severity == SkillWarning {
			t.Errorf("unexpected %s: %+v", issue.Severity, issue)
		}
	}
}

func TestValidateSkillMarkdown_UnknownFieldWarns(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(writeSkill(t, dir, "my-skill",
		"name: my-skill\ndescription: A skill.\nmy-custom-field: oops\n", "body"),
		"SKILL.md")
	_, issues, _ := validateSkillMarkdown("my-skill", path)
	found := false
	for _, issue := range issues {
		if issue.Field == "my-custom-field" {
			found = true
			if issue.Severity != SkillWarning {
				t.Errorf("expected warning severity, got %s", issue.Severity)
			}
			if !strings.Contains(issue.Message, "portable") {
				t.Errorf("expected portability guidance in message, got: %s", issue.Message)
			}
		}
	}
	if !found {
		t.Fatalf("expected unknown-field warning, issues=%+v", issues)
	}
}

func TestValidateSkillMarkdown_ExtensionFieldInfo(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(writeSkill(t, dir, "my-skill",
		"name: my-skill\ndescription: A skill.\nwhen_to_use: When testing.\n", "body"),
		"SKILL.md")
	_, issues, _ := validateSkillMarkdown("my-skill", path)
	found := false
	for _, issue := range issues {
		if issue.Field == "when_to_use" {
			found = true
			if issue.Severity != SkillInfo {
				t.Errorf("expected info severity for known extension, got %s", issue.Severity)
			}
		}
	}
	if !found {
		t.Fatalf("expected extension-field info, issues=%+v", issues)
	}
}

func TestValidateSkillMarkdown_NameRules(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		fmName   string
		dirName  string
		severity SkillSeverity
		contains string
	}{
		{"My_Skill", "my_skill", SkillError, "lowercase"},
		{strings.Repeat("a", specMaxNameLen+1), "long-skill", SkillError, "char limit"},
		{"other-name", "my-skill", SkillWarning, "does not match directory name"},
		{"", "my-skill", SkillWarning, "empty"},
	}
	for _, tc := range cases {
		path := filepath.Join(writeSkill(t, dir, tc.dirName,
			"name: "+tc.fmName+"\ndescription: A skill.\n", "body"), "SKILL.md")
		_, issues, _ := validateSkillMarkdown(tc.dirName, path)
		found := false
		for _, issue := range issues {
			if issue.Field == "name" && issue.Severity == tc.severity && strings.Contains(issue.Message, tc.contains) {
				found = true
			}
		}
		if !found {
			t.Errorf("name=%q dir=%q: expected %s containing %q, issues=%+v", tc.fmName, tc.dirName, tc.severity, tc.contains, issues)
		}
	}
}

func TestValidateSkillMarkdown_DescriptionLimit(t *testing.T) {
	dir := t.TempDir()
	long := strings.Repeat("x", specMaxDescLen+1)
	path := filepath.Join(writeSkill(t, dir, "my-skill",
		"name: my-skill\ndescription: "+long+"\n", "body"), "SKILL.md")
	_, issues, _ := validateSkillMarkdown("my-skill", path)
	found := false
	for _, issue := range issues {
		if issue.Field == "description" && issue.Severity == SkillError {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected description limit error, issues=%+v", issues)
	}
}

func TestValidateSkillMarkdown_MissingAndBrokenFrontmatter(t *testing.T) {
	dir := t.TempDir()
	noFM := filepath.Join(dir, "nofm")
	if err := os.MkdirAll(noFM, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(noFM, "SKILL.md"), []byte("# just body\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, issues, ok := validateSkillMarkdown("nofm", filepath.Join(noFM, "SKILL.md"))
	if ok || len(issues) == 0 || issues[0].Severity != SkillError {
		t.Fatalf("expected error for missing frontmatter, ok=%v issues=%+v", ok, issues)
	}

	unclosed := filepath.Join(dir, "unclosed")
	if err := os.MkdirAll(unclosed, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(unclosed, "SKILL.md"), []byte("---\nname: x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, issues, ok = validateSkillMarkdown("unclosed", filepath.Join(unclosed, "SKILL.md"))
	if ok || len(issues) == 0 || issues[0].Severity != SkillError {
		t.Fatalf("expected error for unclosed frontmatter, ok=%v issues=%+v", ok, issues)
	}
}

func TestValidateSkillMetadataTypeMismatch(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(writeSkill(t, dir, "my-skill",
		"name: my-skill\ndescription: A skill.\nmetadata:\n  owner: [a, b]\n", "body"), "SKILL.md")
	_, issues, ok := validateSkillMarkdown("my-skill", path)
	if ok {
		t.Fatalf("expected type-mismatch failure, issues=%+v", issues)
	}
	if len(issues) == 0 || issues[0].Severity != SkillError || !strings.Contains(issues[0].Message, "unexpected types") {
		t.Fatalf("expected typed error message, issues=%+v", issues)
	}
}

func TestValidateSkillDir_MissingSkillFile(t *testing.T) {
	dir := t.TempDir()
	empty := filepath.Join(dir, "empty-skill")
	if err := os.MkdirAll(empty, 0o755); err != nil {
		t.Fatal(err)
	}
	report := validateSkillDir("empty-skill", empty, "project")
	if len(report.Issues) != 1 || report.Issues[0].Severity != SkillError {
		t.Fatalf("expected missing SKILL.md error, got %+v", report.Issues)
	}
}

func TestValidateSkillPath_Modes(t *testing.T) {
	dir := t.TempDir()
	single := writeSkill(t, dir, "alpha", "name: alpha\ndescription: A.\n", "body")

	// Single skill folder
	reports := ValidateSkillPath(single)
	if len(reports) != 1 || reports[0].Skill != "alpha" {
		t.Fatalf("expected single report for skill folder, got %+v", reports)
	}
	// SKILL.md file path
	reports = ValidateSkillPath(filepath.Join(single, "SKILL.md"))
	if len(reports) != 1 || reports[0].Skill != "alpha" {
		t.Fatalf("expected single report for SKILL.md path, got %+v", reports)
	}
	// Skills root
	writeSkill(t, dir, "beta", "name: beta\ndescription: B.\n", "body")
	reports = ValidateSkillPath(dir)
	if len(reports) != 2 {
		t.Fatalf("expected 2 reports for skills root, got %+v", reports)
	}
	// Nonexistent
	reports = ValidateSkillPath(filepath.Join(dir, "missing"))
	if len(reports) != 1 || len(reports[0].Issues) != 1 || reports[0].Issues[0].Severity != SkillError {
		t.Fatalf("expected error for missing path, got %+v", reports)
	}
}

func TestValidateSkillScopes(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", filepath.Join(tmp, "home"))
	projectDir := filepath.Join(tmp, "proj")

	// Project skill with missing dependency and missing requires-tools entry.
	writeSkill(t, filepath.Join(projectDir, ".ggcode", "skills"), "proj-skill",
		"name: proj-skill\ndescription: P.\ndependencies:\n  - nope-skill\nrequires-tools:\n  - ggcode-definitely-not-installed-xyz\n",
		"body")
	// A healthy project skill for the same name in user scope to test shadowing.
	writeSkill(t, filepath.Join(tmp, "home", ".ggcode", "skills"), "proj-skill",
		"name: proj-skill\ndescription: Shadowed version.\n", "body")

	reports := ValidateSkillScopes(projectDir)
	byPath := make(map[string]*SkillValidationReport)
	for _, r := range reports {
		byPath[r.Path] = r
	}
	projReport, ok := byPath[filepath.Join(projectDir, ".ggcode", "skills", "proj-skill")]
	if !ok {
		t.Fatalf("expected project-skill report, got %+v", reports)
	}
	var hasDep, hasTool bool
	for _, issue := range projReport.Issues {
		if issue.Field == "dependencies" && strings.Contains(issue.Message, "nope-skill") {
			hasDep = true
		}
		if issue.Field == "requires-tools" && strings.Contains(issue.Message, "ggcode-definitely-not-installed-xyz") {
			hasTool = true
		}
	}
	if !hasDep || !hasTool {
		t.Fatalf("expected dependency and requires-tools warnings, issues=%+v", projReport.Issues)
	}

	userReport, ok := byPath[filepath.Join(tmp, "home", ".ggcode", "skills", "proj-skill")]
	if !ok {
		t.Fatalf("expected user-scope report, got %+v", reports)
	}
	if userReport.OverriddenBy == "" {
		t.Fatalf("expected user-scope skill marked as overridden, got %+v", userReport)
	}
	if !strings.Contains(userReport.OverriddenBy, filepath.Join(projectDir, ".ggcode", "skills")) {
		t.Fatalf("override should point at project path, got %q", userReport.OverriddenBy)
	}
}

func TestCountSevere(t *testing.T) {
	reports := []*SkillValidationReport{
		{Issues: []SkillIssue{
			{Severity: SkillError}, {Severity: SkillWarning}, {Severity: SkillInfo},
		}},
		{Issues: []SkillIssue{{Severity: SkillError}}},
	}
	errs, warns := CountSevere(reports)
	if errs != 2 || warns != 1 {
		t.Fatalf("expected 2 errors 1 warning, got %d/%d", errs, warns)
	}
}
