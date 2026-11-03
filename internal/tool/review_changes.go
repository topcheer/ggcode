package tool

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

type ReviewChanges struct{ WorkingDir string }

func (t ReviewChanges) Clone() Tool { return &ReviewChanges{WorkingDir: t.WorkingDir} }

func (t ReviewChanges) Name() string { return "review_changes" }

func (t ReviewChanges) Description() string {
	return "Review current working tree changes and produce a structured code review report with categorized findings (critical, warning, info). Analyzes git diff for debug artifacts, hardcoded secrets, new TODOs, commented-out code, and large changes. Can review staged, unstaged, or all changes."
}

func (t ReviewChanges) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"path": {
				"type": "string",
				"description": "Repository path (default: current directory)"
			},
			"scope": {
				"type": "string",
				"enum": ["all", "staged", "unstaged"],
				"description": "Which changes to review: 'all' (default), 'staged', or 'unstaged'"
			},
			"description": {
				"type": "string",
				"description": "REQUIRED. Brief activity label shown in the UI."
			}
		},
		"required": ["description"]
	}`)
}

var commentedCodePatterns = []*regexp.Regexp{
	regexp.MustCompile(`^\s*//\s*(if|for|func|var|const|type|return|switch|case|defer|go |select|chan|map\[|interface)\b`),
	regexp.MustCompile(`^\s*//\s*\w+\([^)]*\)\s*\{?\s*$`),
	regexp.MustCompile(`^\s*#\s*(if|for|def |class |import |return |print|from )`),
	regexp.MustCompile(`^\s*//\s*(import|package|struct)\s+`),
}

const reviewLargeFileThreshold = 50

func (t ReviewChanges) Execute(ctx context.Context, input json.RawMessage) (Result, error) {
	var args struct {
		Path  string `json:"path"`
		Scope string `json:"scope"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid input: %v", err)}, nil
	}

	dir := resolveDir(args.Path, t.WorkingDir)
	scope := args.Scope
	if scope == "" {
		scope = "all"
	}

	diff, err := getReviewDiff(ctx, dir, scope)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("failed to get diff: %v", err)}, nil
	}

	if strings.TrimSpace(diff) == "" {
		return Result{Content: "No changes found in the working tree."}, nil
	}

	issues := ScanStagedDiffForIssues(diff)

	files := parseReviewDiff(diff)
	var totalAdd, totalDel int
	for _, file := range files {
		totalAdd += file.addedCount
		totalDel += file.removedCount

		for _, fb := range detectCommentBlocks(file) {
			issues = append(issues, fb)
		}

		if file.addedCount > reviewLargeFileThreshold {
			issues = append(issues, DiffIssue{
				File:     file.path,
				Severity: "info",
				Category: "large-change",
				Message:  fmt.Sprintf("Large change: %d lines added - verify completeness", file.addedCount),
			})
		}
	}

	if scope == "all" {
		for _, f := range getUntrackedFiles(ctx, dir) {
			issues = append(issues, DiffIssue{
				File:     f,
				Severity: "info",
				Category: "untracked",
				Message:  "Untracked file not staged",
			})
		}
	}

	report := formatReviewReport(issues, len(files), totalAdd, totalDel)

	debug.Log("review-changes", "reviewed %d files, %d findings",
		len(files), len(issues))

	return Result{Content: report}, nil
}

func getReviewDiff(ctx context.Context, dir, scope string) (string, error) {
	switch scope {
	case "staged":
		return runReviewGit(ctx, dir, "diff", "--cached", "--no-color")
	case "unstaged":
		return runReviewGit(ctx, dir, "diff", "--no-color")
	default:

		return runReviewGit(ctx, dir, "diff", "HEAD", "--no-color")
	}
}

func runReviewGit(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return "", fmt.Errorf("%w: %s", err, msg)
		}
		return "", err
	}
	return string(out), nil
}

func getUntrackedFiles(ctx context.Context, dir string) []string {
	out, err := runReviewGit(ctx, dir, "status", "--porcelain", "--untracked-files=all")
	if err != nil {
		return nil
	}
	var untracked []string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if len(line) >= 3 {
			status := line[:2]
			file := strings.TrimSpace(line[2:])

			if len(file) >= 2 && file[0] == '"' && file[len(file)-1] == '"' {
				file = file[1 : len(file)-1]
			}
			if strings.Contains(status, "?") {
				untracked = append(untracked, file)
			}
		}
	}
	return untracked
}

type reviewDiffLine struct {
	lineNum int
	content string
}

type reviewDiffFile struct {
	path         string
	addedLines   []reviewDiffLine
	addedCount   int
	removedCount int
}

func parseReviewDiff(diff string) []*reviewDiffFile {
	lines := strings.Split(diff, "\n")
	var files []*reviewDiffFile
	var current *reviewDiffFile
	var newLineNum int

	for _, line := range lines {
		if strings.HasPrefix(line, "diff --git") {
			if current != nil {
				files = append(files, current)
			}
			current = &reviewDiffFile{}
			continue
		}
		if current == nil {
			continue
		}
		if strings.HasPrefix(line, "+++ b/") {
			current.path = strings.TrimPrefix(line, "+++ b/")
		} else if strings.HasPrefix(line, "+++ /dev/null") {
			current.path = "(deleted)"
		} else if strings.HasPrefix(line, "--- a/") || strings.HasPrefix(line, "--- /dev/null") {

			continue
		} else if strings.HasPrefix(line, "@@") {
			newLineNum = parseReviewHunkStart(line)
		} else if strings.HasPrefix(line, "+") {

			current.addedLines = append(current.addedLines, reviewDiffLine{
				lineNum: newLineNum,
				content: line[1:],
			})
			current.addedCount++
			newLineNum++
		} else if strings.HasPrefix(line, "-") {

			current.removedCount++
		} else if strings.HasPrefix(line, " ") || line == "" {
			newLineNum++
		}
	}
	if current != nil {
		files = append(files, current)
	}
	return files
}

func parseReviewHunkStart(hunkLine string) int {
	idx := strings.Index(hunkLine, "+")
	if idx < 0 {
		return 0
	}
	rest := hunkLine[idx+1:]
	end := strings.IndexAny(rest, ", ")
	if end < 0 {
		end = len(rest)
	}
	num := 0
	for i := 0; i < end; i++ {
		if rest[i] >= '0' && rest[i] <= '9' {
			num = num*10 + int(rest[i]-'0')
		}
	}
	return num
}

func detectCommentBlocks(file *reviewDiffFile) []DiffIssue {
	var findings []DiffIssue
	consecutive := 0
	startLine := 0
	prevLineNum := 0

	for _, dl := range file.addedLines {
		isCommentedCode := false
		for _, cp := range commentedCodePatterns {
			if cp.MatchString(dl.content) {
				isCommentedCode = true
				break
			}
		}
		if isCommentedCode {

			if consecutive > 0 && dl.lineNum != prevLineNum+1 {
				if consecutive >= 3 {
					findings = append(findings, DiffIssue{
						File:     file.path,
						Line:     startLine,
						Severity: "warning",
						Category: "commented-code",
						Message:  fmt.Sprintf("Commented-out code block (%d lines) - remove dead code", consecutive),
					})
				}
				consecutive = 0
			}
			if consecutive == 0 {
				startLine = dl.lineNum
			}
			consecutive++
			prevLineNum = dl.lineNum
		} else {
			if consecutive >= 3 {
				findings = append(findings, DiffIssue{
					File:     file.path,
					Line:     startLine,
					Severity: "warning",
					Category: "commented-code",
					Message:  fmt.Sprintf("Commented-out code block (%d lines) - remove dead code", consecutive),
				})
			}
			consecutive = 0
		}
	}
	if consecutive >= 3 {
		findings = append(findings, DiffIssue{
			File:     file.path,
			Line:     startLine,
			Severity: "warning",
			Category: "commented-code",
			Message:  fmt.Sprintf("Commented-out code block (%d lines) - remove dead code", consecutive),
		})
	}
	return findings
}

func formatReviewReport(issues []DiffIssue, fileCount, totalAdd, totalDel int) string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("Code Review: %d files changed, +%d -%d\n", fileCount, totalAdd, totalDel))

	if len(issues) == 0 {
		sb.WriteString("\nNo issues found. Changes look clean.")
		return sb.String()
	}

	var critical, warnings, infos []DiffIssue
	for _, iss := range issues {
		switch iss.Severity {
		case "critical":
			critical = append(critical, iss)
		case "warning":
			warnings = append(warnings, iss)
		default:
			infos = append(infos, iss)
		}
	}

	for _, group := range []struct {
		label string
		items []DiffIssue
	}{
		{"CRITICAL", critical},
		{"WARNING", warnings},
		{"INFO", infos},
	} {
		if len(group.items) == 0 {
			continue
		}
		sb.WriteString(fmt.Sprintf("\n%s (%d):\n", group.label, len(group.items)))
		for _, iss := range group.items {
			loc := iss.File
			if iss.Line > 0 {
				loc = fmt.Sprintf("%s:%d", iss.File, iss.Line)
			}
			sb.WriteString(fmt.Sprintf("  %s - %s\n", loc, iss.Message))
		}
	}

	if len(critical) > 0 {
		sb.WriteString(fmt.Sprintf("\n%d critical issue(s) should be fixed before commit.", len(critical)))
	} else if len(warnings) > 0 {
		sb.WriteString(fmt.Sprintf("\n%d warning(s) to review.", len(warnings)))
	}

	return sb.String()
}
