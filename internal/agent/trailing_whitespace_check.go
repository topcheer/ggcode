package agent

// Trailing whitespace introduction detection for post-write integrity checks.
//
// Research basis: Trailing whitespace is one of the most common code quality
// issues introduced by AI coding agents. LLMs frequently leave trailing spaces
// or tabs on lines - especially when copying or transforming code from context.
// This causes:
//   - Lint failures (eslint, flake8, pylint, shellcheck, rubocop all flag it)
//   - Git diff noise that pollutes code reviews
//   - Pre-commit hook failures that waste a full iteration
//   - Merge conflicts in collaborative editing
//
// Competitor analysis:
//   - Cursor: IDE auto-trim on save (not available in CLI agents)
//   - Aider: runs pre-commit hooks that catch this after the fact
//   - Claude Code: relies on the model not introducing trailing whitespace
//   - Cline: no trailing whitespace detection
//
// This check is delta-based: it only flags trailing whitespace that is NEWLY
// introduced by this edit (lines that didn't have trailing whitespace before).
// Pre-existing trailing whitespace is not flagged to avoid noise on legacy files.

import (
	"fmt"
	"path/filepath"
	"strings"
)

// trailingWhitespaceExts lists file extensions that are commonly lint-checked
// for trailing whitespace. Binary/asset files are excluded.
var trailingWhitespaceExts = map[string]bool{
	// Scripting
	".py": true, ".sh": true, ".bash": true, ".zsh": true, ".rb": true,
	".pl": true, ".lua": true,
	// Web
	".js": true, ".jsx": true, ".ts": true, ".tsx": true, ".vue": true,
	".svelte": true, ".html": true, ".htm": true, ".css": true, ".scss": true,
	".sass": true, ".less": true,
	// Systems
	".c": true, ".h": true, ".cpp": true, ".cc": true, ".cxx": true,
	".hpp": true, ".rs": true, ".swift": true, ".kt": true, ".java": true,
	".scala": true,
	// Config/data
	".json": true, ".yaml": true, ".yml": true, ".toml": true, ".xml": true,
	".ini": true, ".cfg": true, ".conf": true,
	// Other
	".dart": true, ".php": true, ".sql": true, ".graphql": true, ".proto": true,
	".dockerfile": true,
	".md":         true, ".rst": true,
	// Makefiles have no extension but are checked via filename below
}

// maxTrailingWhitespaceWarns caps how many lines we report to keep output concise.
const maxTrailingWhitespaceWarns = 5

// minTrailingWhitespaceRatio: if the old file already has trailing whitespace
// on >40% of lines, we skip the check entirely - the file has a pre-existing
// style issue and we don't want to nag about every edit.
const trailingWhitespaceOldRatio = 0.40

// shouldCheckTrailingWhitespace returns true for file types where trailing
// whitespace is a meaningful quality issue.
func shouldCheckTrailingWhitespace(filePath string) bool {
	ext := strings.ToLower(filepath.Ext(filePath))
	if ext == ".go" {
		return true
	}
	if trailingWhitespaceExts[ext] {
		return true
	}
	// Check filename-based files (Makefile, Dockerfile, etc.)
	base := strings.ToLower(filepath.Base(filePath))
	if base == "makefile" || base == "dockerfile" || base == "cmakelists.txt" {
		return true
	}
	// Shell scripts without extensions
	if base == "gemfile" || base == "rakefile" || base == "brewfile" {
		return true
	}
	return false
}

// hasTrailingWhitespace returns true if the line has trailing whitespace
// (spaces or tabs after the last non-whitespace character, excluding the
// newline itself).
func hasTrailingWhitespace(line string) bool {
	// Strip the trailing newline if present (already split by lines, but be safe)
	line = strings.TrimRight(line, "\r\n")
	if line == "" {
		return false // empty lines are fine
	}
	last := line[len(line)-1]
	return last == ' ' || last == '\t'
}

// checkTrailingWhitespace detects trailing whitespace newly introduced by this
// edit. Returns a non-empty warning string if newly-introduced trailing
// whitespace is found, or "" if none.
//
// Delta-based detection: compares the set of lines with trailing whitespace
// in old vs new content. Only lines that are NEW (not present in old content
// with trailing whitespace) are flagged. This avoids nagging on files that
// already have pre-existing trailing whitespace issues.
func checkTrailingWhitespace(filePath, oldContent, newContent string) string {
	if !shouldCheckTrailingWhitespace(filePath) {
		return ""
	}
	if strings.TrimSpace(newContent) == "" {
		return ""
	}

	oldLines := strings.Split(oldContent, "\n")
	newLines := strings.Split(newContent, "\n")

	// Count trailing-whitespace lines in old content to skip files with
	// pre-existing style issues.
	oldTWCount := 0
	for _, line := range oldLines {
		if hasTrailingWhitespace(line) {
			oldTWCount++
		}
	}
	// If >40% of old lines already had trailing whitespace, skip - the file
	// has a pre-existing style and we'd just generate noise.
	if len(oldLines) > 5 && float64(oldTWCount)/float64(len(oldLines)) > trailingWhitespaceOldRatio {
		return ""
	}

	// Build a set of old lines WITH trailing whitespace so we can skip them
	// when found in new content (they're pre-existing, not newly introduced).
	oldTWSet := make(map[string]bool, oldTWCount)
	for _, line := range oldLines {
		if hasTrailingWhitespace(line) {
			oldTWSet[line] = true
		}
	}

	// Find newly-introduced trailing whitespace lines in the new content.
	var newlyIntroduced []int // 1-based line numbers
	for i, line := range newLines {
		if !hasTrailingWhitespace(line) {
			continue
		}
		// Skip if this exact line (with trailing whitespace) already existed
		// in old content - it's a pre-existing line, not newly introduced.
		if oldTWSet[line] {
			continue
		}
		newlyIntroduced = append(newlyIntroduced, i+1)
	}

	if len(newlyIntroduced) == 0 {
		return ""
	}

	// Build warning message
	var b strings.Builder
	shown := newlyIntroduced
	if len(shown) > maxTrailingWhitespaceWarns {
		shown = shown[:maxTrailingWhitespaceWarns]
	}

	// Format line numbers
	lineNums := make([]string, len(shown))
	for i, ln := range shown {
		lineNums[i] = fmt.Sprintf("%d", ln)
	}

	b.WriteString(fmt.Sprintf(
		"Trailing whitespace introduced on %d line(s) (line%s: %s). "+
			"Trailing spaces/tabs cause lint failures (eslint, flake8, pylint, etc.), "+
			"git diff noise, and pre-commit hook rejections. Remove trailing whitespace from these lines.",
		len(newlyIntroduced),
		pluralS(len(newlyIntroduced)),
		strings.Join(lineNums, ", "),
	))
	if len(newlyIntroduced) > maxTrailingWhitespaceWarns {
		b.WriteString(fmt.Sprintf(" (%d more)", len(newlyIntroduced)-maxTrailingWhitespaceWarns))
	}

	return b.String()
}

func pluralS(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
