package tool

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

var conventionalCommitPattern = regexp.MustCompile(`^(\w+)(\([^)]+\))?!?: `)

var conventionalCommitTypes = map[string]bool{
	"feat": true, "fix": true, "docs": true, "style": true,
	"refactor": true, "perf": true, "test": true, "build": true,
	"ci": true, "chore": true, "revert": true,
}

const (
	maxFilesPerCommit = 15
	maxLinesPerCommit = 500
)

func AnalyzeCommitMessage(msg string) string {
	firstLine := strings.SplitN(strings.TrimSpace(msg), "\n", 2)[0]
	if firstLine == "" {
		return ""
	}

	if m := conventionalCommitPattern.FindStringSubmatch(firstLine); m != nil {
		if conventionalCommitTypes[strings.ToLower(m[1])] {
			return ""
		}
	}

	lower := strings.ToLower(firstLine)

	if strings.HasPrefix(lower, "merge ") || strings.HasPrefix(lower, "revert ") {
		return ""
	}

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
	case msgContainsAny(lower, "pipeline", "deploy", "release", "ci pipeline"):
		return "ci"
	case msgWordBoundary(lower, "ci"):

		return "ci"
	default:
		return "feat"
	}
}

func msgWordBoundary(s, word string) bool {
	return regexp.MustCompile(`\b` + regexp.QuoteMeta(word) + `\b`).MatchString(s)
}

func msgContainsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

type FileCategory int

const (
	CatSource FileCategory = iota
	CatTest
	CatDocs
	CatConfig
	CatOther
)

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

func categorizeFile(path string) FileCategory {
	lower := strings.ToLower(path)

	testSuffixes := []string{
		"_test.go", ".test.js", ".test.ts", ".test.jsx", ".test.tsx",
		".spec.js", ".spec.ts", ".spec.jsx", ".spec.tsx",
		"test_", "_test.py", ".spec.rb", "_spec.rb", "_test.rs",
	}
	for _, sfx := range testSuffixes {

		if strings.HasSuffix(lower, sfx) ||
			(strings.HasPrefix(sfx, "test_") && (strings.HasPrefix(lower, "test_") || strings.Contains(lower, "/test_"))) {
			return CatTest
		}
	}

	for _, ext := range []string{".md", ".rst", ".txt", ".adoc", ".tex"} {
		if strings.HasSuffix(lower, ext) {
			return CatDocs
		}
	}

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

type diffStats struct {
	files     []string
	additions int
	deletions int
}

var diffBinaryHeader = regexp.MustCompile(`^Binary files (?:a/(\S+)|"a/([^"]+)"|/dev/null) and (?:b/(\S+)|"b/([^"]+)"|/dev/null) differ$`)

var diffModeChange = regexp.MustCompile(`^(old|new) mode `)

var diffRenameTo = regexp.MustCompile(`^rename to (.+)`)

func parseDiffStats(diffOutput string) diffStats {
	var stats diffStats
	seen := make(map[string]bool)
	add := func(path string) {
		if path == "" || seen[path] {
			return
		}
		seen[path] = true
		stats.files = append(stats.files, path)
	}
	lastOldFile := ""
	lastGitPath := ""

	prevWasOldHeader := false

	lines := strings.Split(diffOutput, "\n")
	for i, line := range lines {

		afterOld := prevWasOldHeader
		prevWasOldHeader = false

		if m := diffGitHeader.FindStringSubmatch(line); m != nil && (m[1] != "" || (len(m) > 2 && m[2] != "")) {
			lastGitPath = normalizeGitDiffPath(m[1])
			if m[1] == "" {
				lastGitPath = normalizeGitDiffPath(m[2])
			}
		}
		if m := diffFileHeader.FindStringSubmatch(line); m != nil {
			lastOldFile = ""
			add(diffHeaderPath(m))
			continue
		}

		if diffDevNullHeader.MatchString(line) {
			add(lastOldFile)
			lastOldFile = ""
			continue
		}
		if m := diffOldFileHeader.FindStringSubmatch(line); m != nil &&

			i+1 < len(lines) && strings.HasPrefix(lines[i+1], "+++") {
			lastOldFile = diffHeaderPath(m)
			prevWasOldHeader = true
			continue
		}

		if m := diffBinaryHeader.FindStringSubmatch(line); m != nil {

			aSide := m[1]
			if aSide == "" && len(m) > 2 {
				aSide = m[2]
			}
			bSide := m[3]
			if bSide == "" && len(m) > 4 {
				bSide = m[4]
			}
			if aSide != "" {
				add(normalizeGitDiffPath(aSide))
			} else {
				add(normalizeGitDiffPath(bSide))
			}
			lastOldFile = ""
			continue
		}

		if diffModeChange.MatchString(line) {
			add(lastGitPath)
			continue
		}

		if m := diffRenameTo.FindStringSubmatch(line); m != nil && len(m) > 1 {
			add(strings.TrimSpace(m[1]))
			lastGitPath = ""
			continue
		}

		if len(line) > 0 && line[0] == '+' {
			if strings.HasPrefix(line, "+++") && afterOld {
				continue
			}
			stats.additions++
		}

		if len(line) > 0 && line[0] == '-' {
			if strings.HasPrefix(line, "--- /dev/null") {
				prevWasOldHeader = true
				continue
			}
			stats.deletions++
		}
	}
	return stats
}

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

func AnalyzeCommitScope(diffOutput string) (cohesionWarning, sizeWarning string) {
	if strings.TrimSpace(diffOutput) == "" {
		return "", ""
	}

	stats := parseDiffStats(diffOutput)
	if len(stats.files) == 0 {
		return "", ""
	}

	totalLines := stats.additions + stats.deletions

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

	categories := make(map[FileCategory][]string)
	for _, f := range stats.files {
		cat := categorizeFile(f)
		categories[cat] = append(categories[cat], f)
	}

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
