package memory

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
)

// StaleFinding represents a single staleness signal in a memory entry.
type StaleFinding struct {
	Key    string
	Reason string // "broken-path", "oversized", "ancient"
	Detail string // specific detail (e.g., which path is broken)
	Age    time.Duration
}

// StaleReport holds the results of a staleness scan.
type StaleReport struct {
	Scanned     int
	BrokenPaths int
	Oversized   int
	Ancient     int
	Findings    []StaleFinding
}

// HasFindings reports whether any staleness signals were detected.
func (r StaleReport) HasFindings() bool {
	return len(r.Findings) > 0
}

// ancientThreshold is the age at which a persistent memory is considered
// potentially ancient and worth review. 180 days.
const ancientThreshold = 180 * 24 * time.Hour

// filePathPattern detects file paths in memory content.
// Matches patterns like: internal/agent/foo.go, cmd/ggcode/root.go, ./src/main.ts
var filePathPattern = regexp.MustCompile(`(?:^|[\s'"(+])((?:\.{0,2}/)?(?:[a-zA-Z0-9_-]+/)+[a-zA-Z0-9_-]+\.[a-zA-Z]{1,5})`)

// dirPathPattern detects directory paths in memory content.
// Matches patterns like: internal/agent/, cmd/ggcode/, pkg/util/
var dirPathPattern = regexp.MustCompile(`(?:^|[\s'"(+])((?:\.{0,2}/)?(?:[a-zA-Z0-9_-]+/){2,})`)

// ScanStaleness checks memory entries for potential staleness signals:
//   - Broken file/dir path references (paths that no longer exist)
//   - Oversized entries (exceeding inline budget)
//   - Ancient persistent entries (older than 180 days)
//
// workingDir is the project root used to resolve relative paths.
// If workingDir is empty, path checks are skipped.
func (am *AutoMemory) ScanStaleness(workingDir string) StaleReport {
	metas, err := am.collectMetas()
	if err != nil {
		debug.Log("memory", "staleness scan: failed to read dir %s: %v", am.dir, err)
		return StaleReport{}
	}
	now := time.Now()
	active, _, _, _ := curateEntries(metas, now)

	report := StaleReport{Scanned: len(active)}

	for _, m := range active {
		path := filepath.Join(am.dir, m.Key+".md")
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		content := string(data)
		age := now.Sub(m.CreatedAt)

		// Check for broken path references (only for project-scoped memories
		// where workingDir is meaningful).
		if workingDir != "" {
			broken := findBrokenPaths(content, workingDir)
			if len(broken) > 0 {
				detail := strings.Join(broken, ", ")
				if len(detail) > 120 {
					detail = detail[:120] + "..."
				}
				report.Findings = append(report.Findings, StaleFinding{
					Key:    m.Key,
					Reason: "broken-path",
					Detail: detail,
					Age:    age,
				})
				report.BrokenPaths++
			}
		}

		// Check for oversized entries.
		if len(content) > maxInlineBytes {
			report.Findings = append(report.Findings, StaleFinding{
				Key:    m.Key,
				Reason: "oversized",
				Detail: formatByteSize(len(content)) + " (limit: " + formatByteSize(maxInlineBytes) + ")",
				Age:    age,
			})
			report.Oversized++
		}

		// Check for ancient persistent entries.
		if m.Category == CategoryPersistent && age > ancientThreshold {
			report.Findings = append(report.Findings, StaleFinding{
				Key:    m.Key,
				Reason: "ancient",
				Detail: formatDuration(age),
				Age:    age,
			})
			report.Ancient++
		}
	}

	return report
}

// findBrokenPaths extracts file and directory path references from memory
// content and returns those that don't exist relative to workingDir.
func findBrokenPaths(content, workingDir string) []string {
	seen := make(map[string]bool)
	var broken []string

	checkPath := func(p string) {
		p = strings.TrimSpace(p)
		if p == "" || seen[p] {
			return
		}
		seen[p] = true

		// Skip very generic paths that are too common to be meaningful.
		if isGenericPath(p) {
			return
		}

		full := filepath.Join(workingDir, p)
		if _, err := os.Stat(full); os.IsNotExist(err) {
			broken = append(broken, p)
		}
	}

	for _, match := range filePathPattern.FindAllStringSubmatch(content, -1) {
		if len(match) > 1 {
			checkPath(match[1])
		}
	}
	for _, match := range dirPathPattern.FindAllStringSubmatch(content, -1) {
		if len(match) > 1 {
			p := strings.TrimRight(match[1], "/")
			if p != "" {
				checkPath(p + "/")
			}
		}
	}

	return broken
}

// isGenericPath returns true for paths that are too common to verify
// meaningfully (e.g., "src/main.go", "README.md" without a directory prefix).
func isGenericPath(p string) bool {
	// Single-component files without a directory prefix.
	if !strings.Contains(p, "/") {
		return true
	}
	// Skip paths that are likely code examples in comments, not real paths.
	// Only match placeholder filenames like "example.go" as standalone names.
	base := p
	if idx := strings.LastIndex(p, "/"); idx >= 0 {
		base = p[idx+1:]
	}
	if strings.HasPrefix(base, "example.") {
		return true
	}
	// Skip npm/node_modules paths that aren't in Go projects.
	if strings.HasPrefix(p, "node_modules/") {
		return true
	}
	return false
}

// formatByteSize returns a human-readable byte size string.
func formatByteSize(n int) string {
	if n < 1024 {
		return itoa(n) + "B"
	}
	if n < 1024*1024 {
		return itoa(n/1024) + "KB"
	}
	return itoa(n/(1024*1024)) + "MB"
}

// formatDuration returns a human-readable duration string.
func formatDuration(d time.Duration) string {
	days := int(d.Hours() / 24)
	if days >= 365 {
		return itoa(days/365) + "y" + itoa(days%365) + "d"
	}
	return itoa(days) + "d"
}
