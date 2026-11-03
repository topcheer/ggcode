package tool

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

type FileChangeInfo struct {
	Path      string
	Category  FileCategory
	Dir       string
	Base      string
	Additions int
	Deletions int
}

type CommitGroup struct {
	Files    []FileChangeInfo
	Category FileCategory
	Dir      string
	Label    string
	Type     string
}

func (g CommitGroup) TotalLines() int {
	total := 0
	for _, f := range g.Files {
		total += f.Additions + f.Deletions
	}
	return total
}

var diffDevNullHeader = regexp.MustCompile(`^\+\+\+\s+/dev/null`)

func parseFileChanges(diffOutput string) []FileChangeInfo {
	if strings.TrimSpace(diffOutput) == "" {
		return nil
	}

	var files []FileChangeInfo
	currentFile := ""
	currentAdd := 0
	currentDel := 0
	newLineNum := 0

	lastGitPath := ""

	lastOldFile := ""

	prevWasOldHeader := false

	flush := func() {

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

	lines := strings.Split(diffOutput, "\n")
	for i := 0; i < len(lines); i++ {
		line := lines[i]

		afterOld := prevWasOldHeader
		prevWasOldHeader = false

		if m := diffGitHeader.FindStringSubmatch(line); m != nil && (m[1] != "" || (len(m) > 2 && m[2] != "")) {
			lastGitPath = normalizeGitDiffPath(m[1])
			if m[1] == "" {
				lastGitPath = normalizeGitDiffPath(m[2])
			}
		}
		if m := diffFileHeader.FindStringSubmatch(line); m != nil {

			if p := diffHeaderPath(m); p != currentFile {
				flush()
				currentFile = p
			}
			lastOldFile = ""
			newLineNum = 0
			continue
		}

		if m := diffRenameTo.FindStringSubmatch(line); m != nil {
			flush()
			currentFile = m[1]
			lastOldFile = ""
			newLineNum = 0
			continue
		}

		if m := diffBinaryHeader.FindStringSubmatch(line); m != nil && len(m) > 2 {
			flush()
			bSide := m[3]
			if bSide == "" {
				bSide = m[4]
			}
			currentFile = bSide
			lastOldFile = ""
			newLineNum = 0
			continue
		}

		if diffModeChange.MatchString(line) && lastGitPath != "" {

			if currentFile != lastGitPath {
				flush()
				currentFile = lastGitPath
			}
			lastOldFile = ""
			newLineNum = 0
			continue
		}

		if diffDevNullHeader.MatchString(line) && lastOldFile != "" {
			flush()
			currentFile = lastOldFile
			lastOldFile = ""
			newLineNum = 0
			continue
		}

		if m := diffOldFileHeader.FindStringSubmatch(line); m != nil &&
			i+1 < len(lines) && strings.HasPrefix(lines[i+1], "+++ ") {
			lastOldFile = diffHeaderPath(m)
			prevWasOldHeader = true
			continue
		}
		if m := diffHunkHeader.FindStringSubmatch(line); m != nil {
			fmt.Sscanf(m[1], "%d", &newLineNum)
			continue
		}
		if len(line) == 0 {
			continue
		}
		if line[0] == '+' {

			if strings.HasPrefix(line, "+++") && afterOld {
				continue
			}
			currentAdd++
		} else if line[0] == '-' {

			if strings.HasPrefix(line, "--- /dev/null") {
				prevWasOldHeader = true
				continue
			}
			currentDel++
		}
	}
	flush()

	return files
}

func SuggestCommitPartition(diffOutput string) []CommitGroup {
	files := parseFileChanges(diffOutput)
	if len(files) < 2 {
		return nil
	}

	type groupKey struct {
		category FileCategory
		dir      string
	}

	groups := make(map[groupKey]*CommitGroup)
	var keyOrder []groupKey

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

	for _, f := range files {
		if f.Category != CatTest {
			continue
		}

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

	if len(result) < 2 {
		return nil
	}

	return result
}

func testToSourceBase(testBase string) string {
	lower := strings.ToLower(testBase)

	if strings.HasSuffix(lower, "_test.go") {
		return testBase[:len(testBase)-len("_test.go")] + ".go"
	}

	for _, suffix := range []string{".test.js", ".test.ts", ".test.jsx", ".test.tsx",
		".spec.js", ".spec.ts", ".spec.jsx", ".spec.tsx"} {
		if strings.HasSuffix(lower, suffix) {
			ext := suffix[strings.LastIndex(suffix, "."):]
			return testBase[:len(testBase)-len(suffix)] + ext
		}
	}

	if strings.HasPrefix(lower, "test_") && strings.HasSuffix(lower, ".py") {
		return testBase[len("test_"):]
	}

	if strings.HasSuffix(lower, "_test.py") {
		return testBase[:len(testBase)-len("_test.py")] + ".py"
	}

	if strings.HasSuffix(lower, "_spec.rb") {
		return testBase[:len(testBase)-len("_spec.rb")] + ".rb"
	}

	if strings.HasSuffix(lower, "_test.rs") {
		return testBase[:len(testBase)-len("_test.rs")] + ".rs"
	}
	return ""
}

func groupLabel(cat FileCategory, dir string) string {
	catLabel := categoryLabel(cat)
	if dir == "" {
		return catLabel
	}
	return fmt.Sprintf("%s in %s/", catLabel, dir)
}

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

func sumAdditions(files []FileChangeInfo) int {
	total := 0
	for _, f := range files {
		total += f.Additions
	}
	return total
}

func sumDeletions(files []FileChangeInfo) int {
	total := 0
	for _, f := range files {
		total += f.Deletions
	}
	return total
}
