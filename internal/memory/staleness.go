package memory

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
)

type StaleFinding struct {
	Key    string
	Reason string
	Detail string
	Age    time.Duration
}

type StaleReport struct {
	Scanned     int
	BrokenPaths int
	Oversized   int
	Ancient     int
	Findings    []StaleFinding
}

func (r StaleReport) HasFindings() bool {
	return len(r.Findings) > 0
}

const ancientThreshold = 180 * 24 * time.Hour

var filePathPattern = regexp.MustCompile(`(?:^|[\s'"(+])((?:\.{0,2}/)?(?:[a-zA-Z0-9_-]+/)+[a-zA-Z0-9_-]+\.[a-zA-Z]{1,5})`)

var dirPathPattern = regexp.MustCompile(`(?:^|[\s'"(+])((?:\.{0,2}/)?(?:[a-zA-Z0-9_-]+/){2,})`)

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

		if len(content) > maxInlineBytes {
			report.Findings = append(report.Findings, StaleFinding{
				Key:    m.Key,
				Reason: "oversized",
				Detail: formatByteSize(len(content)) + " (limit: " + formatByteSize(maxInlineBytes) + ")",
				Age:    age,
			})
			report.Oversized++
		}

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

const maxStaleInlineAnnotations = 5

func (am *AutoMemory) annotateStaleInline(inline []MemoryEntry) {
	if am.projectRoot == "" || len(inline) == 0 {
		return
	}
	annotated := 0
	for idx := range inline {
		if annotated >= maxStaleInlineAnnotations {
			return
		}
		broken := findBrokenPaths(inline[idx].Content, am.projectRoot)
		if len(broken) == 0 {
			continue
		}
		detail := strings.Join(broken, ", ")
		if len(detail) > 120 {
			detail = detail[:120] + "..."
		}
		inline[idx].Content += "\n> [memory-stale] This entry references paths that no longer exist: " +
			detail + ". Treat these references as outdated and verify against the current workspace."
		annotated++
	}
}

func findBrokenPaths(content, workingDir string) []string {
	seen := make(map[string]bool)
	var broken []string

	checkPath := func(p string) {
		p = strings.TrimSpace(p)
		if p == "" || seen[p] {
			return
		}
		seen[p] = true

		if isGenericPath(p) {
			return
		}

		full := filepath.Join(workingDir, p)

		if _, err := os.Stat(full); err != nil {
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

func isGenericPath(p string) bool {

	if !strings.Contains(p, "/") {
		return true
	}

	base := p
	if idx := strings.LastIndex(p, "/"); idx >= 0 {
		base = p[idx+1:]
	}
	if strings.HasPrefix(base, "example.") {
		return true
	}

	if strings.HasPrefix(p, "node_modules/") {
		return true
	}
	return false
}

func formatByteSize(n int) string {
	if n < 1024 {
		return itoa(n) + "B"
	}
	if n < 1024*1024 {
		return itoa(n/1024) + "KB"
	}
	return itoa(n/(1024*1024)) + "MB"
}

func formatDuration(d time.Duration) string {
	days := int(d.Hours() / 24)
	if days >= 365 {
		return itoa(days/365) + "y" + itoa(days%365) + "d"
	}
	return itoa(days) + "d"
}
