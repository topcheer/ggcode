package tool

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// conventionalCommitPattern matches "type(scope): description" or "type: description".
// The type is case-insensitive; scope and breaking-change "!" are optional.
var conventionalCommitPattern = regexp.MustCompile(`^(\w+)(\([^)]+\))?!?: `)

// conventionalCommitTypes are the standard Conventional Commits type prefixes.
var conventionalCommitTypes = map[string]bool{
	"feat": true, "fix": true, "docs": true, "style": true,
	"refactor": true, "perf": true, "test": true, "build": true,
	"ci": true, "chore": true, "revert": true,
}

// commitSizeThresholds control when commit size warnings fire.
const (
	maxFilesPerCommit = 15  // warn if more staged files
	maxLinesPerCommit = 500 // warn if more total line changes
)

// AnalyzeCommitMessage checks whether the commit message follows the
// Conventional Commits specification (type(scope): description) and returns a
// non-blocking suggestion when it does not. Returns an empty string when the
// message already follows the convention or is a merge/revert (which have
// their own format).
func AnalyzeCommitMessage(msg string) string {
	firstLine := strings.SplitN(strings.TrimSpace(msg), "\n", 2)[0]
	if firstLine == "" {
		return ""
	}

	// Already follows conventional commits: "type: ..." or "type(scope): ..."
	if m := conventionalCommitPattern.FindStringSubmatch(firstLine); m != nil {
		if conventionalCommitTypes[strings.ToLower(m[1])] {
			return ""
		}
	}

	lower := strings.ToLower(firstLine)

	// Merge and revert commits have their own established formats.
	if strings.HasPrefix(lower, "merge ") || strings.HasPrefix(lower, "revert ") {
		return ""
	}

	// Only suggest for messages long enough to be descriptive — don't pile on
	// when the vague-message check already fires.
	if len(strings.TrimSpace(firstLine)) < 10 {
		return ""
	}

	suggestedType := inferCommitType(firstLine)
	example := fmt.Sprintf("%s: %s", suggestedType, firstLine)

	return fmt.Sprintf(
		"Tip: consider Conventional Commits format: 'type(scope): description'.\n"+
			"  Example: %q\n"+
			"  Common types: feat, fix, docs, refactor, perf, test, chore, build, ci, revert.",
		example,
	)
}

// inferCommitType guesses the Conventional Commits type from message content.
func inferCommitType(msg string) string {
	lower := strings.ToLower(msg)
	switch {
	case msgContainsAny(lower, "fix", "bug", "error", "crash", "broken", "nil pointer", "panic"):
		return "fix"
	case msgContainsAny(lower, "test", "mock", "fixture"):
		return "test"
	case msgContainsAny(lower, "add", "new ", "implement", "support", "introduce", "create"):
		return "feat"
	case msgContainsAny(lower, "doc", "readme", "comment", "changelog", "guide"):
		return "docs"
	case msgContainsAny(lower, "refactor", "clean", "simplif", "renam", "extract", "move"):
		return "refactor"
	case msgContainsAny(lower, "perf", "optim", "speed", "fast", "cache", "benchmark"):
		return "perf"
	case msgContainsAny(lower, "config", "dependenc", "build", "version", "bump", "upgrade"):
		return "chore"
	case msgContainsAny(lower, "ci", "pipeline", "deploy", "release"):
		return "ci"
	default:
		return "feat"
	}
}

// msgContainsAny reports whether s contains any of the given substrings.
func msgContainsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// FileCategory represents the role a file plays in a project.
type FileCategory int

const (
	CatSource FileCategory = iota
	CatTest
	CatDocs
	CatConfig
	CatOther
)

// categoryLabel returns a human-readable label for a file category.
func categoryLabel(cat FileCategory) string {
	switch cat {
	case CatSource:
		return "source code"
	case CatTest:
		return "tests"
	case CatDocs:
		return "documentation"
	case CatConfig:
		return "configuration"
	default:
		return "other"
	}
}

// categorizeFile classifies a file path by its likely role in the project.
func categorizeFile(path string) FileCategory {
	lower := strings.ToLower(path)

	// Test files — check first since extensions overlap with source.
	testSuffixes := []string{
		"_test.go", ".test.js", ".test.ts", ".test.jsx", ".test.tsx",
		".spec.js", ".spec.ts", ".spec.jsx", ".spec.tsx",
		"test_", "_test.py", ".spec.rb", "_spec.rb", "_test.rs",
	}
	for _, sfx := range testSuffixes {
		if strings.Contains(lower, sfx) {
			return CatTest
		}
	}

	// Documentation.
	for _, ext := range []string{".md", ".rst", ".txt", ".adoc", ".tex"} {
		if strings.HasSuffix(lower, ext) {
			return CatDocs
		}
	}

	// Configuration.
	configSuffixes := []string{".yaml", ".yml", ".toml", ".ini", ".cfg", ".conf", ".json"}
	configNames := []string{"dockerfile", "makefile", "docker-compose", ".gitignore", ".editorconfig"}
	for _, ext := range configSuffixes {
		if strings.HasSuffix(lower, ext) {
			return CatConfig
		}
	}
	for _, name := range configNames {
		if strings.Contains(lower, name) {
			return CatConfig
		}
	}

	// Source code.
	sourceExts := []string{
		".go", ".py", ".js", ".ts", ".jsx", ".tsx", ".mjs", ".mts",
		".java", ".kt", ".rs", ".c", ".cpp", ".h", ".hpp", ".cc",
		".rb", ".php", ".swift", ".scala", ".cs", ".sh", ".bash",
		".vue", ".svelte", ".dart", ".ex", ".exs", ".clj", ".hs", ".lua",
	}
	for _, ext := range sourceExts {
		if strings.HasSuffix(lower, ext) {
			return CatSource
		}
	}

	return CatOther
}

// diffStats holds parsed statistics from a unified diff.
type diffStats struct {
	files     []string
	additions int
	deletions int
}

// parseDiffStats extracts file paths and counts additions/deletions from
// a unified diff output (e.g. from "git diff --cached").
func parseDiffStats(diffOutput string) diffStats {
	var stats diffStats
	seen := make(map[string]bool)

	for _, line := range strings.Split(diffOutput, "\n") {
		if m := diffFileHeader.FindStringSubmatch(line); m != nil {
			if !seen[m[1]] {
				seen[m[1]] = true
				stats.files = append(stats.files, m[1])
			}
			continue
		}
		// Count additions (lines starting with '+', but not "+++").
		if len(line) > 0 && line[0] == '+' && !strings.HasPrefix(line, "+++") {
			stats.additions++
		}
		// Count deletions (lines starting with '-', but not "---").
		if len(line) > 0 && line[0] == '-' && !strings.HasPrefix(line, "---") {
			stats.deletions++
		}
	}
	return stats
}

// combineScopeWarnings merges cohesion and size warnings into a single string.
func combineScopeWarnings(cohesion, size string) string {
	var parts []string
	if cohesion != "" {
		parts = append(parts, cohesion)
	}
	if size != "" {
		parts = append(parts, size)
	}
	return strings.Join(parts, "\n\n")
}

// AnalyzeCommitScope evaluates the cohesion and size of staged changes and
// returns advisory warnings. Both return values are empty when the changes
// appear cohesive and reasonably sized.
func AnalyzeCommitScope(diffOutput string) (cohesionWarning, sizeWarning string) {
	if strings.TrimSpace(diffOutput) == "" {
		return "", ""
	}

	stats := parseDiffStats(diffOutput)
	if len(stats.files) == 0 {
		return "", ""
	}

	totalLines := stats.additions + stats.deletions

	// --- Size analysis ---

	if len(stats.files) > maxFilesPerCommit {
		sizeWarning = fmt.Sprintf(
			"Warning: this commit touches %d files (+%d/-%d lines). "+
				"Consider splitting into smaller, focused commits — one logical change per commit — "+
				"for easier review, bisect, and revert.",
			len(stats.files), stats.additions, stats.deletions,
		)
	} else if totalLines > maxLinesPerCommit {
		sizeWarning = fmt.Sprintf(
			"Warning: this commit has %d line changes (+%d/-%d) across %d files. "+
				"Consider splitting large changes into smaller, focused commits.",
			totalLines, stats.additions, stats.deletions, len(stats.files),
		)
	}

	// --- Cohesion analysis ---

	// Group staged files by category.
	categories := make(map[FileCategory][]string)
	for _, f := range stats.files {
		cat := categorizeFile(f)
		categories[cat] = append(categories[cat], f)
	}

	// Count distinct non-test categories. It is normal to commit source + test
	// together; mixing 3+ non-test categories (e.g. code + docs + config) in a
	// single commit often means unrelated changes were batched together.
	nonTestCats := 0
	for cat := range categories {
		if cat != CatTest {
			nonTestCats++
		}
	}

	if nonTestCats >= 3 && len(stats.files) >= 4 {
		var parts []string
		for cat := CatSource; cat <= CatOther; cat++ {
			if cat == CatTest || len(categories[cat]) == 0 {
				continue
			}
			parts = append(parts, fmt.Sprintf("%s (%d)", categoryLabel(cat), len(categories[cat])))
		}
		sort.Strings(parts)
		cohesionWarning = fmt.Sprintf(
			"Warning: this commit mixes changes across multiple concerns: %s. "+
				"Consider splitting into separate commits (e.g., one for code, one for docs, one for config) "+
				"so each commit represents a single logical change.",
			strings.Join(parts, ", "),
		)
	}

	return cohesionWarning, sizeWarning
}
