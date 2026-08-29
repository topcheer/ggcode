package tool

// Smart Commit Partitioning — Logical Commit Grouping Suggestions
//
// Trend: GitHub Copilot Workspace, Sourcegraph Cody, and Aider all help users
// organize changes into focused commits. Claude Code and Cursor leave it to the
// user/agent to figure out which files belong together.
//
// Gap: ggcode's AnalyzeCommitScope detected multi-concern commits and warned
// "consider splitting," but gave no actionable partition plan. The agent was
// forced to reason about file grouping itself — expensive, error-prone LLM
// work for a task that can be done deterministically.
//
// This module analyzes a staged/working-tree diff and produces a concrete
// partition plan: which files belong in which commit, what type each commit
// should be, and a suggested message prefix. Zero LLM cost, fully deterministic.
//
// Grouping heuristics (applied in order):
//  1. Test files merge into the group of their corresponding source file
//     (handler_test.go → handler.go's group).
//  2. Remaining files group by (category, directory) — files in the same
//     package/module directory with the same category form one group.
//  3. Groups are ordered by size (largest first) so the most important changes
//     are committed first.
//
// Integration: called from AnalyzeCommitScope when a cohesion warning fires.
// The partition suggestion is appended to the cohesion warning, giving the
// agent a ready-to-execute plan instead of a vague "consider splitting."

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// FileChangeInfo captures per-file change metadata for partitioning.
type FileChangeInfo struct {
	Path      string // file path as shown in diff (b/ prefix stripped)
	Category  FileCategory
	Dir       string // directory portion of the path
	Base      string // filename without directory
	Additions int
	Deletions int
}

// CommitGroup represents a suggested logical commit grouping.
type CommitGroup struct {
	Files    []FileChangeInfo
	Category FileCategory
	Dir      string // primary directory for this group
	Label    string // human-readable label (e.g. "source code in internal/agent")
	Type     string // suggested Conventional Commits type
}

// TotalLines returns total additions + deletions for this group.
func (g CommitGroup) TotalLines() int {
	total := 0
	for _, f := range g.Files {
		total += f.Additions + f.Deletions
	}
	return total
}

// diffDevNullHeader matches the "+++ /dev/null" marker of a deleted file.
// Lives here (not diff_scan.go) because only parseFileChanges consumes it.
var diffDevNullHeader = regexp.MustCompile(`^\+\+\+\s+/dev/null`)

// parseFileChanges extracts per-file change info from a unified diff.
// Unlike parseDiffStats (which returns aggregate counts), this returns
// structured per-file data needed for intelligent grouping.
func parseFileChanges(diffOutput string) []FileChangeInfo {
	if strings.TrimSpace(diffOutput) == "" {
		return nil
	}

	var files []FileChangeInfo
	currentFile := ""
	currentAdd := 0
	currentDel := 0
	newLineNum := 0
	// Last "--- a/path" header seen; a following "+++ /dev/null" means
	// the file was DELETED - the only place its path survives in the diff
	// (#1319: deleted files dropped out of partition plans entirely).
	lastOldFile := ""

	flush := func() {
		// Reset counters even on the empty-file early return (#1319: the
		// early return leaked pending -N lines into the next parsed file,
		// inflating e.g. +5/-2 to +5/-22 when a deletion preceded it).
		add, del := currentAdd, currentDel
		currentAdd = 0
		currentDel = 0
		if currentFile == "" {
			return
		}
		fc := FileChangeInfo{
			Path:      currentFile,
			Category:  categorizeFile(currentFile),
			Dir:       filepath.Dir(currentFile),
			Base:      filepath.Base(currentFile),
			Additions: add,
			Deletions: del,
		}
		if fc.Dir == "." {
			fc.Dir = ""
		}
		files = append(files, fc)
		currentAdd = 0
		currentDel = 0
	}

	for _, line := range strings.Split(diffOutput, "\n") {
		if m := diffFileHeader.FindStringSubmatch(line); m != nil {
			flush()
			currentFile = m[1]
			lastOldFile = ""
			newLineNum = 0
			continue
		}
		// Deleted file: "--- a/path" followed by "+++ /dev/null". The
		// +++ line does not match diffFileHeader (no b/ prefix), so the
		// old path from the --- header is the only source (#1319:
		// deleted files used to drop out of partition plans entirely).
		if diffDevNullHeader.MatchString(line) && lastOldFile != "" {
			flush()
			currentFile = lastOldFile
			lastOldFile = ""
			newLineNum = 0
			continue
		}
		if m := diffOldFileHeader.FindStringSubmatch(line); m != nil {
			lastOldFile = m[1]
			continue
		}
		if m := diffHunkHeader.FindStringSubmatch(line); m != nil {
			fmt.Sscanf(m[1], "%d", &newLineNum)
			continue
		}
		if len(line) == 0 {
			continue
		}
		if line[0] == '+' && !strings.HasPrefix(line, "+++") {
			currentAdd++
		} else if line[0] == '-' && !strings.HasPrefix(line, "---") {
			currentDel++
		}
	}
	flush()

	return files
}

// SuggestCommitPartition analyzes file changes and returns a concrete plan for
// splitting them into logical commits. Returns nil when the changes are
// cohesive enough for a single commit (fewer than 2 groups).
//
// The partition is generated using directory + category proximity heuristics
// and test-file pairing. Each group includes a suggested Conventional Commits
// type and a human-readable label.
func SuggestCommitPartition(diffOutput string) []CommitGroup {
	files := parseFileChanges(diffOutput)
	if len(files) < 2 {
		return nil
	}

	// Build groups by (category, directory), with test files merging into
	// their source counterpart's group.
	type groupKey struct {
		category FileCategory
		dir      string
	}

	groups := make(map[groupKey]*CommitGroup)
	var keyOrder []groupKey

	// First pass: create groups for non-test files.
	for _, f := range files {
		if f.Category == CatTest {
			continue
		}
		k := groupKey{category: f.Category, dir: f.Dir}
		if _, ok := groups[k]; !ok {
			g := &CommitGroup{
				Category: f.Category,
				Dir:      f.Dir,
				Label:    groupLabel(f.Category, f.Dir),
				Type:     categoryCommitType(f.Category),
			}
			groups[k] = g
			keyOrder = append(keyOrder, k)
		}
		groups[k].Files = append(groups[k].Files, f)
	}

	// Second pass: merge test files into source groups.
	for _, f := range files {
		if f.Category != CatTest {
			continue
		}
		// Find matching source file's group: strip _test suffix and look up.
		sourceBase := testToSourceBase(f.Base)
		merged := false
		if sourceBase != "" {
			for k, g := range groups {
				for _, sf := range g.Files {
					if sf.Base == sourceBase && sf.Dir == f.Dir {
						g.Files = append(g.Files, f)
						merged = true
						break
					}
				}
				if merged {
					break
				}
				_ = k
			}
		}
		// If no source match, create a dedicated test group.
		if !merged {
			k := groupKey{category: CatTest, dir: f.Dir}
			if _, ok := groups[k]; !ok {
				groups[k] = &CommitGroup{
					Category: CatTest,
					Dir:      f.Dir,
					Label:    groupLabel(CatTest, f.Dir),
					Type:     "test",
				}
				keyOrder = append(keyOrder, k)
			}
			groups[k].Files = append(groups[k].Files, f)
		}
	}

	// Collect and sort groups by total lines (largest first), then by label.
	result := make([]CommitGroup, 0, len(groups))
	for _, k := range keyOrder {
		result = append(result, *groups[k])
	}
	sort.SliceStable(result, func(i, j int) bool {
		if result[i].TotalLines() != result[j].TotalLines() {
			return result[i].TotalLines() > result[j].TotalLines()
		}
		return result[i].Label < result[j].Label
	})

	// Only suggest a partition when there are 2+ groups.
	if len(result) < 2 {
		return nil
	}

	return result
}

// testToSourceBase converts a test file base name to its source counterpart.
// Returns empty string if no clear mapping exists.
//
//	handler_test.go    → handler.go
//	auth.test.ts       → auth.ts
//	test_user.py       → user.py
//	user_spec.rb       → user.rb
func testToSourceBase(testBase string) string {
	lower := strings.ToLower(testBase)

	// Go: _test.go
	if strings.HasSuffix(lower, "_test.go") {
		return testBase[:len(testBase)-len("_test.go")] + ".go"
	}
	// JS/TS: .test.js, .test.ts, .test.jsx, .test.tsx, .spec.*
	for _, suffix := range []string{".test.js", ".test.ts", ".test.jsx", ".test.tsx",
		".spec.js", ".spec.ts", ".spec.jsx", ".spec.tsx"} {
		if strings.HasSuffix(lower, suffix) {
			ext := suffix[strings.LastIndex(suffix, "."):] // e.g. ".js"
			return testBase[:len(testBase)-len(suffix)] + ext
		}
	}
	// Python: test_*.py
	if strings.HasPrefix(lower, "test_") && strings.HasSuffix(lower, ".py") {
		return testBase[len("test_"):]
	}
	// Python: *_test.py
	if strings.HasSuffix(lower, "_test.py") {
		return testBase[:len(testBase)-len("_test.py")] + ".py"
	}
	// Ruby: *_spec.rb
	if strings.HasSuffix(lower, "_spec.rb") {
		return testBase[:len(testBase)-len("_spec.rb")] + ".rb"
	}
	// Rust: *_test.rs or tests/*.rs
	if strings.HasSuffix(lower, "_test.rs") {
		return testBase[:len(testBase)-len("_test.rs")] + ".rs"
	}
	return ""
}

// groupLabel generates a human-readable label for a commit group.
func groupLabel(cat FileCategory, dir string) string {
	catLabel := categoryLabel(cat)
	if dir == "" {
		return catLabel
	}
	return fmt.Sprintf("%s in %s/", catLabel, dir)
}

// categoryCommitType maps a file category to a suggested Conventional Commits type.
func categoryCommitType(cat FileCategory) string {
	switch cat {
	case CatSource:
		return "feat"
	case CatTest:
		return "test"
	case CatDocs:
		return "docs"
	case CatConfig:
		return "chore"
	default:
		return "chore"
	}
}

// FormatCommitPartition renders a partition plan into a human-readable string
// suitable for appending to a git_commit result or scope warning.
func FormatCommitPartition(groups []CommitGroup) string {
	if len(groups) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("[Commit partition] Suggested split into %d focused commits:\n", len(groups)))
	for i, g := range groups {
		totalLines := g.TotalLines()
		fileNames := make([]string, 0, len(g.Files))
		for _, f := range g.Files {
			fileNames = append(fileNames, f.Path)
		}
		b.WriteString(fmt.Sprintf("  %d. %s (%s, %d files, +%d/-%d):\n",
			i+1, g.Type, g.Label, len(g.Files),
			sumAdditions(g.Files), sumDeletions(g.Files)))
		// Show up to 5 files per group, then "... and N more".
		showCount := len(fileNames)
		if showCount > 5 {
			showCount = 5
		}
		for j := 0; j < showCount; j++ {
			b.WriteString(fmt.Sprintf("       %s\n", fileNames[j]))
		}
		if len(fileNames) > 5 {
			b.WriteString(fmt.Sprintf("       ... and %d more\n", len(fileNames)-5))
		}
		_ = totalLines
	}
	b.WriteString("\nStage and commit each group separately using git_add + git_commit.")

	return strings.TrimRight(b.String(), "\n")
}

// sumAdditions returns total additions across files.
func sumAdditions(files []FileChangeInfo) int {
	total := 0
	for _, f := range files {
		total += f.Additions
	}
	return total
}

// sumDeletions returns total deletions across files.
func sumDeletions(files []FileChangeInfo) int {
	total := 0
	for _, f := range files {
		total += f.Deletions
	}
	return total
}
