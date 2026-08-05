package memory

import (
	"fmt"
	"strings"
	"time"
)

// HealthReport provides a diagnostic overview of the memory store.
// It summarizes counts, category distribution, staleness signals, and
// total context budget usage.
type HealthReport struct {
	Total   int
	Active  int
	Expired int
	Deduped int
	Capped  int

	// Category distribution
	Transient  int
	Evolving   int
	Persistent int
	Default    int

	// Budget usage
	InlineEntries int
	IndexEntries  int
	InlineBytes   int
	BudgetPercent int // percentage of maxTotalInlineBytes used

	// Staleness signals
	StaleBrokenPaths int
	StaleOversized   int
	StaleAncient     int

	// Newest and oldest entry ages
	OldestDays int
	NewestDays int

	// Potential duplicates (entries with same dedup key)
	DuplicateGroups int
}

// HealthReport returns a diagnostic summary of the memory store's health.
// workingDir is used for path staleness checks (pass "" to skip path checks).
func (am *AutoMemory) HealthReport(workingDir string) HealthReport {
	metas, err := am.collectMetas()
	if err != nil {
		return HealthReport{}
	}

	now := time.Now()
	active, expired, deduped, capped := curateEntries(metas, now)

	report := HealthReport{
		Total:   len(metas),
		Active:  len(active),
		Expired: expired,
		Deduped: deduped,
		Capped:  capped,
	}

	// Category distribution.
	report.Transient, report.Evolving, report.Persistent, report.Default = countCategories(active)

	// Budget usage (what LoadForPrompt would inject).
	inline, indexOnly, _ := am.LoadForPrompt()
	report.InlineEntries = len(inline)
	report.IndexEntries = len(indexOnly)
	for _, e := range inline {
		report.InlineBytes += len(e.Content)
	}
	if maxTotalInlineBytes > 0 {
		report.BudgetPercent = report.InlineBytes * 100 / maxTotalInlineBytes
	}

	// Age range.
	report.OldestDays, report.NewestDays = computeAgeRange(active, now)

	// Staleness scan.
	stale := am.ScanStaleness(workingDir)
	report.StaleBrokenPaths = stale.BrokenPaths
	report.StaleOversized = stale.Oversized
	report.StaleAncient = stale.Ancient

	// Duplicate group detection (same dedup key among active entries).
	report.DuplicateGroups = countDuplicateGroups(active)

	return report
}

// FormatHealthReport returns a human-readable health summary string.
func (r HealthReport) FormatHealthReport() string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("Memory Health: %d active / %d total", r.Active, r.Total))
	if r.Expired > 0 || r.Deduped > 0 || r.Capped > 0 {
		sb.WriteString(fmt.Sprintf(" (%d expired, %d deduped, %d capped)", r.Expired, r.Deduped, r.Capped))
	}
	sb.WriteString("\n")

	// Categories
	sb.WriteString(fmt.Sprintf("  Categories: %d persistent, %d evolving, %d transient, %d default\n",
		r.Persistent, r.Evolving, r.Transient, r.Default))

	// Budget
	sb.WriteString(fmt.Sprintf("  Context budget: %d/%d entries inline (%d%% of %d token budget)\n",
		r.InlineEntries, r.InlineEntries+r.IndexEntries, r.BudgetPercent, maxTotalInlineBytes/4))

	// Age
	if r.OldestDays > 0 {
		sb.WriteString(fmt.Sprintf("  Age range: %d-%d days (oldest-newest)\n", r.NewestDays, r.OldestDays))
	}

	// Staleness signals
	warnings := 0
	if r.StaleBrokenPaths > 0 {
		sb.WriteString(fmt.Sprintf("  [STALE] %d entries reference broken file paths\n", r.StaleBrokenPaths))
		warnings++
	}
	if r.StaleOversized > 0 {
		sb.WriteString(fmt.Sprintf("  [OVERSIZED] %d entries exceed inline size limit\n", r.StaleOversized))
		warnings++
	}
	if r.StaleAncient > 0 {
		sb.WriteString(fmt.Sprintf("  [ANCIENT] %d persistent entries older than 180 days\n", r.StaleAncient))
		warnings++
	}
	if r.DuplicateGroups > 0 {
		sb.WriteString(fmt.Sprintf("  [DUPLICATES] %d potential duplicate groups detected\n", r.DuplicateGroups))
		warnings++
	}

	if warnings == 0 {
		sb.WriteString("  Status: healthy (no issues detected)\n")
	} else {
		sb.WriteString(fmt.Sprintf("  Status: %d issue(s) need attention\n", warnings))
	}

	return strings.TrimRight(sb.String(), "\n")
}

// countCategories tallies entries by memory category.
func countCategories(metas []MemoryMeta) (transient, evolving, persistent, defaultCount int) {
	for _, m := range metas {
		switch m.Category {
		case CategoryTransient:
			transient++
		case CategoryEvolving:
			evolving++
		case CategoryPersistent:
			persistent++
		default:
			defaultCount++
		}
	}
	return
}

// computeAgeRange returns the oldest and newest entry ages in days.
// Returns (0, 0) if the slice is empty.
func computeAgeRange(metas []MemoryMeta, now time.Time) (oldestDays, newestDays int) {
	if len(metas) == 0 {
		return 0, 0
	}
	oldest := metas[0].CreatedAt
	newest := metas[0].CreatedAt
	for _, m := range metas[1:] {
		if m.CreatedAt.Before(oldest) {
			oldest = m.CreatedAt
		}
		if m.CreatedAt.After(newest) {
			newest = m.CreatedAt
		}
	}
	oldestDays = int(now.Sub(oldest).Hours() / 24)
	newestDays = int(now.Sub(newest).Hours() / 24)
	return
}

// countDuplicateGroups counts how many dedup keys appear more than once.
func countDuplicateGroups(metas []MemoryMeta) int {
	dedupCount := make(map[string]int)
	for _, m := range metas {
		if m.DedupKey != "" {
			dedupCount[m.DedupKey]++
		}
	}
	groups := 0
	for _, count := range dedupCount {
		if count > 1 {
			groups++
		}
	}
	return groups
}
