package agent

import (
	"fmt"
	"path"
	"regexp"
	"strings"
	"sync"
)

type fixAmnesiaState struct {
	mu sync.Mutex

	observed map[string][]string

	fixedPatterns map[string][]string

	warned map[string]bool

	maxWarnings int
}

type faErrorCategory struct {
	category       string
	label          string
	contentPattern *regexp.Regexp
}

var faErrorCategories = []faErrorCategory{
	{
		category: "missing-import",
		label:    "used a package symbol without importing the package",
	},
	{
		category: "unused-variable",
		label:    "variable declared and not used",
	},
	{
		category: "unused-import",
		label:    "import declared and not used",
	},
	{
		category:       "defer-in-loop",
		label:          "defer statement inside a loop (resource leak)",
		contentPattern: regexp.MustCompile(`(?m)for\s.*\{[^}]*defer\s+\w+\.`),
	},
	{
		category:       "range-val-copy",
		label:          "range loop uses value copy where address is needed",
		contentPattern: regexp.MustCompile(`(?m)for\s+_\s*,\s*\w+\s*:?=\s*range\s+\w+\s*\{[^}]*&\w+\}`),
	},
	{
		category:       "map-concurrent-write",
		label:          "map written without mutex in concurrent context",
		contentPattern: regexp.MustCompile(`(?m)go\s+func\s*\([^)]*\)\s*\{[^}]*\w+\[`),
	},
}

func newFixAmnesiaState() *fixAmnesiaState {
	return &fixAmnesiaState{
		observed:      make(map[string][]string),
		fixedPatterns: make(map[string][]string),
		warned:        make(map[string]bool),
		maxWarnings:   2,
	}
}

func (d *fixAmnesiaState) recordErrorObserved(category, file string) {
	if category == "" || file == "" {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.observed[category] = appendIfMissing(d.observed[category], file)
}

func (d *fixAmnesiaState) recordFileEdited(file string) {
	if file == "" {
		return
	}

	d.mu.Lock()
	defer d.mu.Unlock()
	for cat, files := range d.observed {
		for _, f := range files {
			if fixAmnesiaSameFile(f, file) {
				d.fixedPatterns[cat] = appendIfMissing(d.fixedPatterns[cat], f)
			}
		}
	}
}

func fixAmnesiaSameFile(a, b string) bool {

	a = strings.ReplaceAll(strings.TrimPrefix(strings.TrimSpace(a), "./"), "\\", "/")
	b = strings.ReplaceAll(strings.TrimPrefix(strings.TrimSpace(b), "./"), "\\", "/")
	if a == b {
		return true
	}

	if pathDepth(b) >= 2 && strings.HasSuffix(a, "/"+b) {
		return true
	}
	if pathDepth(a) >= 2 && strings.HasSuffix(b, "/"+a) {
		return true
	}
	return false
}

func pathDepth(p string) int {
	dir := path.Dir(p)
	if dir == "." {
		return 0
	}
	return strings.Count(dir, "/") + 1
}

func (d *fixAmnesiaState) hasFixedPatternsInOtherFiles(newFile string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, files := range d.fixedPatterns {
		for _, f := range files {
			if !fixAmnesiaSameFile(f, newFile) {
				return true
			}
		}
	}
	return false
}

func (d *fixAmnesiaState) checkContentAgainstFixed(fixFile, newFile, content string) string {

	if fixAmnesiaSameFile(fixFile, newFile) {
		return ""
	}
	if len(content) == 0 {
		return ""
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	warningCount := 0
	for cat := range d.warned {
		if d.warned[cat] {
			warningCount++
		}
	}

	var warnings []string
	for _, ec := range faErrorCategories {
		if d.warned[ec.category] {
			continue
		}

		fixedFiles, ok := d.fixedPatterns[ec.category]
		if !ok {
			continue
		}

		fixedInDiffFile := false
		for _, f := range fixedFiles {

			if !fixAmnesiaSameFile(f, newFile) && f != "" {
				fixedInDiffFile = true
				break
			}
		}
		if !fixedInDiffFile {
			continue
		}

		if contentMatchesCategory(ec, content) {
			if warningCount+len(warnings) >= d.maxWarnings {
				break
			}
			d.warned[ec.category] = true
			msg := fmt.Sprintf("Fix Amnesia: you previously fixed '%s' in %s, but similar pattern detected in %s. "+
				"Apply the same fix proactively to avoid repeating the error.",
				ec.label, fixedFiles[0], newFile)
			warnings = append(warnings, msg)
		}
	}

	if len(warnings) == 0 {
		return ""
	}
	return "[fix-amnesia] " + strings.Join(warnings, " | ")
}

func classifyToolError(_ string, errMsg string) (category, file string) {
	errLower := strings.ToLower(errMsg)
	if errLower == "" {
		return "", ""
	}

	file = extractFilePathFromError(errMsg)

	switch {
	case strings.Contains(errLower, "nil pointer") || strings.Contains(errLower, "nil dereference"):
		return "nil-deref-after-nil-check", file

	case strings.Contains(errLower, "declared and not used"):
		return "unused-variable", file
	case strings.Contains(errLower, "imported and not used") || strings.Contains(errLower, "unused import"):
		return "unused-import", file
	case strings.Contains(errLower, "undefined:") && (strings.Contains(errLower, "fmt.") || strings.Contains(errLower, "os.") ||
		strings.Contains(errLower, "strings.") || strings.Contains(errLower, "errors.")):
		return "missing-import", file
	case strings.Contains(errLower, "defer in loop") || strings.Contains(errLower, "resource leak"):
		return "defer-in-loop", file
	case strings.Contains(errLower, "concurrent map") || strings.Contains(errLower, "map write"):
		return "map-concurrent-write", file
	case strings.Contains(errLower, "range value copy") || strings.Contains(errLower, "took address of range"):
		return "range-val-copy", file
	}
	return "", ""
}

func extractFilePathFromError(errMsg string) string {

	re := regexp.MustCompile(`(?:^|[\s:(])(/[^\s:]+\.(?:go|ts|js|py|rs))`)
	m := re.FindStringSubmatch(errMsg)
	if len(m) >= 2 {
		return m[1]
	}

	re2 := regexp.MustCompile(`([\w/]+\.go):\d+`)
	m2 := re2.FindStringSubmatch(errMsg)
	if len(m2) >= 2 {
		return m2[1]
	}
	return ""
}

var faStdlibPkgs = []string{"fmt", "os", "io", "io/ioutil", "net/http", "net", "sync", "time", "errors", "strings", "strconv", "bytes", "context", "encoding/json"}

func contentMatchesCategory(ec faErrorCategory, content string) bool {
	if ec.category == "missing-import" {
		return missingImportInContent(content)
	}
	if ec.contentPattern == nil {
		return false
	}
	return ec.contentPattern.MatchString(content)
}

func missingImportInContent(content string) bool {
	for _, pkg := range faStdlibPkgs {
		used := regexp.MustCompile(`(?:^|\s)` + regexp.QuoteMeta(pkg) + `\.`)
		if !used.MatchString(content) {
			continue
		}
		if !hasImportFor(content, pkg) {
			return true
		}
	}
	return false
}

func hasImportFor(content, pkg string) bool {

	imp := regexp.MustCompile(`(?m)^[+\-]?\s*import\s+"` + regexp.QuoteMeta(pkg) + `"`)
	if imp.MatchString(content) {
		return true
	}
	blk := regexp.MustCompile(`(?s)import\s*\((.*?)\)`)
	for _, m := range blk.FindAllStringSubmatch(content, -1) {
		line := regexp.MustCompile(`(?m)^[+\-]?\s*(?:\w+\s+)?"` + regexp.QuoteMeta(pkg) + `"`)
		if line.MatchString(m[1]) {
			return true
		}
	}
	return false
}

func appendIfMissing(slice []string, s string) []string {
	for _, v := range slice {
		if v == s {
			return slice
		}
	}
	return append(slice, s)
}
