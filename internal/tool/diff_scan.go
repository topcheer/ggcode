package tool

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/topcheer/ggcode/internal/security"
)

type DiffIssue struct {
	File     string `json:"file"`
	Line     int    `json:"line"`
	Severity string `json:"severity"`
	Category string `json:"category"`
	Message  string `json:"message"`
}

const maxDiffScanIssues = 15

var debugStmtPatterns = []*regexp.Regexp{

	regexp.MustCompile(`\bfmt\.Println\(`),
	regexp.MustCompile(`\bfmt\.Printf\(`),
	regexp.MustCompile(`\bprintln\(`),

	regexp.MustCompile(`\bconsole\.log\(`),
	regexp.MustCompile(`\bconsole\.debug\(`),

	regexp.MustCompile(`\bprint\(`),
	regexp.MustCompile(`\bpprint\.pprint\(`),
	regexp.MustCompile(`\bpprint\.pformat\(`),

	regexp.MustCompile(`\bbinding\.irb\b`),

	regexp.MustCompile(`\bSystem\.out\.println\(`),
	regexp.MustCompile(`\bSystem\.err\.println\(`),

	regexp.MustCompile(`\beprintln!\(`),
	regexp.MustCompile(`\bdbg!\(`),

	regexp.MustCompile(`\bvar_dump\(`),
	regexp.MustCompile(`\bprint_r\(`),
	regexp.MustCompile(`\bdd\(`),

	regexp.MustCompile(`\bfprintf\s*\(\s*stderr\b`),
}

var pythonOnlyPattern = map[string]bool{
	`\bprint\(`:           true,
	`\bpprint\.pprint\(`:  true,
	`\bpprint\.pformat\(`: true,

	`\bdd\(`: true,
}

var debuggerPatterns = []*regexp.Regexp{
	regexp.MustCompile(`\bpdb\.set_trace\(\)`),
	regexp.MustCompile(`\bbreakpoint\(\)`),
	regexp.MustCompile(`^\s*import\s+pdb\b`),
	regexp.MustCompile(`^\s*debugger;\s*$`),
	regexp.MustCompile(`\bbinding\.pry\b`),
}

var pythonFileRe = regexp.MustCompile(`\.py$|(^|/)python(/|$)`)

var todoPattern = regexp.MustCompile(`(?i)\b(TODO|FIXME|HACK|XXX|BUG|WORKAROUND)\b`)

var mergeConflictMarkers = []*regexp.Regexp{
	regexp.MustCompile(`^<{7}`),
	regexp.MustCompile(`^={7}`),
	regexp.MustCompile(`^>{7}`),
	regexp.MustCompile(`^\|{7}`),
}

var testFilePattern = regexp.MustCompile(`(_test\.go|\.test\.[jt]sx?|\.spec\.[jt]sx?|test_.*\.py|.*_test\.py|\.test\.ts|\.test\.js|__tests__/.*|Test\.cs|_spec\.rb|\.spec\.rb|_test\.rs)$`)

var diffFileHeader = regexp.MustCompile(`^\+\+\+\s+(?:b/"([^"]+)"|b/([^"\s]\S*))`)

var diffOldFileHeader = regexp.MustCompile(`^---\s+(?:a/"([^"]+)"|a/([^"\s]\S*))`)

func diffHeaderPath(m []string) string {
	if len(m) >= 3 && m[2] != "" {
		return normalizeGitDiffPath(m[2])
	}
	if len(m) >= 2 {
		return normalizeGitDiffPath(m[1])
	}
	return ""
}

var diffHunkHeader = regexp.MustCompile(`^@{2,3}\s+(?:-\d+(?:,\d+)?\s+)+\+(\d+)(?:,\d+)?\s+@{2,3}`)

func ScanStagedDiffForIssues(diffOutput string) []DiffIssue {
	if strings.TrimSpace(diffOutput) == "" {
		return nil
	}

	var issues []DiffIssue
	lowSeverityIssues := 0
	currentFile := ""
	newLineNum := 0
	isTestFile := false

	inCombinedHunk := false

	lines := strings.Split(diffOutput, "\n")

	for _, line := range lines {

		if m := diffFileHeader.FindStringSubmatch(line); m != nil {

			currentFile = diffHeaderPath(m)
			isTestFile = testFilePattern.MatchString(currentFile)
			continue
		}

		if diffOldFileHeader.MatchString(line) {
			continue
		}

		if m := diffHunkHeader.FindStringSubmatch(line); m != nil {
			fmt.Sscanf(m[1], "%d", &newLineNum)

			inCombinedHunk = strings.HasPrefix(line, "@@@")
			continue
		}

		if len(line) == 0 || line[0] != '+' {

			if len(line) > 0 && line[0] == ' ' {
				newLineNum++
			}
			continue
		}

		var addedContent string

		if inCombinedHunk && len(line) > 2 && (line[1] == '+' || line[1] == '-' || line[1] == ' ') {
			addedContent = line[2:]
		} else {
			addedContent = line[1:]
		}
		issueLine := newLineNum
		newLineNum++

		for _, pat := range mergeConflictMarkers {
			if pat.MatchString(addedContent) {
				issues = append(issues, DiffIssue{
					File:     currentFile,
					Line:     issueLine,
					Severity: "warning",
					Category: "merge-conflict",
					Message:  "Unresolved merge conflict marker — resolve before committing.",
				})
				break
			}
		}

		if secretScanEnabled {
			findings := security.ScanForSecrets(currentFile, addedContent+"\n")
			for _, f := range findings {
				issues = append(issues, DiffIssue{
					File:     currentFile,
					Line:     issueLine,
					Severity: severityFromSecurity(f.Severity),
					Category: "secret",
					Message:  fmt.Sprintf("Potential secret detected: %s. Remove or use an environment variable.", f.Name),
				})
			}
		}

		if isTestFile {
			continue
		}

		if lowSeverityIssues >= maxDiffScanIssues {
			continue
		}

		for _, pat := range debuggerPatterns {
			if pat.MatchString(addedContent) {
				issues = append(issues, DiffIssue{
					File:     currentFile,
					Line:     issueLine,
					Severity: "warning",
					Category: "debugger",
					Message:  "Debugger/breakpoint statement detected — remove before committing.",
				})
				lowSeverityIssues++
				break
			}
		}

		for _, pat := range debugStmtPatterns {

			if pythonOnlyPattern[pat.String()] && !pythonFileRe.MatchString(currentFile) {
				continue
			}
			if pat.MatchString(addedContent) {
				issues = append(issues, DiffIssue{
					File:     currentFile,
					Line:     issueLine,
					Severity: "info",
					Category: "debug-stmt",
					Message:  "Debug print statement detected — remove if not intentionally logging.",
				})
				lowSeverityIssues++
				break
			}
		}

		if todoPattern.MatchString(addedContent) {
			issues = append(issues, DiffIssue{
				File:     currentFile,
				Line:     issueLine,
				Severity: "info",
				Category: "todo",
				Message:  "TODO/FIXME marker in new code — track this as a follow-up task.",
			})
			lowSeverityIssues++
		}
	}

	return issues
}

func severityFromSecurity(sec string) string {
	if sec == "high" {
		return "warning"
	}
	return "info"
}

func FormatDiffIssues(issues []DiffIssue) string {
	if len(issues) == 0 {
		return ""
	}

	var b strings.Builder
	warnings := 0
	infos := 0
	for _, iss := range issues {
		if iss.Severity == "warning" {
			warnings++
		} else {
			infos++
		}
	}

	b.WriteString("[Pre-commit scan] ")
	parts := []string{}
	if warnings > 0 {
		parts = append(parts, fmt.Sprintf("%d warning(s)", warnings))
	}
	if infos > 0 {
		parts = append(parts, fmt.Sprintf("%d info", infos))
	}
	b.WriteString(strings.Join(parts, ", "))
	if warnings > 0 {
		b.WriteString(" — review before pushing")
	}
	b.WriteString(":\n")

	for _, iss := range issues {
		loc := iss.File
		if iss.Line > 0 {
			loc = fmt.Sprintf("%s:%d", iss.File, iss.Line)
		}
		icon := "i"
		if iss.Severity == "warning" {
			icon = "!"
		}
		b.WriteString(fmt.Sprintf("  [%s] %s — %s\n", icon, loc, iss.Message))
	}

	if warnings > 0 {
		b.WriteString("\nFix the warnings above before pushing to a shared branch.")
	}

	return strings.TrimRight(b.String(), "\n")
}

func getStagedDiff(ctx context.Context, dir string) string {
	cmd := gitCommand(ctx, "diff", "--cached")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return string(out)
}

func getWorkingTreeDiff(ctx context.Context, dir string) string {
	cmd := gitCommand(ctx, "diff", "HEAD")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return string(out)
}
