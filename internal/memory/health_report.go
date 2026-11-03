package memory

import (
	"fmt"
	"strings"
	"time"
)

type HealthReport struct {
	Total   int
	Active  int
	Expired int
	Deduped int
	Capped  int

	Transient  int
	Evolving   int
	Persistent int
	Default    int

	InlineEntries        int
	IndexEntries         int
	InlineBytes          int
	EffectiveBudgetBytes int
	BudgetPercent        int

	StaleBrokenPaths int
	StaleOversized   int
	StaleAncient     int

	OldestDays int
	NewestDays int

	DuplicateGroups int

	UsedAtLeastOnce int
	NeverUsed       int
}

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

	report.Transient, report.Evolving, report.Persistent, report.Default = countCategories(active)

	inline, indexOnly, _ := am.loadForPrompt(false, "")
	report.InlineEntries = len(inline)
	report.IndexEntries = len(indexOnly)
	for _, e := range inline {
		report.InlineBytes += len(e.Content)
	}

	budget := am.EffectiveInlineBudget()
	if budget > 0 {
		report.BudgetPercent = report.InlineBytes * 100 / budget
	}
	report.EffectiveBudgetBytes = budget

	report.OldestDays, report.NewestDays = computeAgeRange(active, now)

	for _, m := range active {
		if m.Uses > 0 {
			report.UsedAtLeastOnce++
		} else {
			report.NeverUsed++
		}
	}

	stale := am.ScanStaleness(workingDir)
	report.StaleBrokenPaths = stale.BrokenPaths
	report.StaleOversized = stale.Oversized
	report.StaleAncient = stale.Ancient

	report.DuplicateGroups = countDuplicateGroups(active)

	return report
}

func (r HealthReport) FormatHealthReport() string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("Memory Health: %d active / %d total", r.Active, r.Total))
	if r.Expired > 0 || r.Deduped > 0 || r.Capped > 0 {
		sb.WriteString(fmt.Sprintf(" (%d expired, %d deduped, %d capped)", r.Expired, r.Deduped, r.Capped))
	}
	sb.WriteString("\n")

	sb.WriteString(fmt.Sprintf("  Categories: %d persistent, %d evolving, %d transient, %d default\n",
		r.Persistent, r.Evolving, r.Transient, r.Default))

	sb.WriteString(fmt.Sprintf("  Context budget: %d/%d entries inline (%d%% of %d-byte effective inline budget)\n",
		r.InlineEntries, r.InlineEntries+r.IndexEntries, r.BudgetPercent, r.EffectiveBudgetBytes))

	if r.OldestDays > 0 {

		sb.WriteString(fmt.Sprintf("  Age range: %d-%d days (oldest-newest)\n", r.OldestDays, r.NewestDays))
	}

	if r.Active > 0 {
		sb.WriteString(fmt.Sprintf("  Usage: %d/%d entries injected at least once\n", r.UsedAtLeastOnce, r.Active))
	}

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
	if r.NeverUsed > 0 && r.Active >= 5 && r.NeverUsed*2 >= r.Active {
		sb.WriteString(fmt.Sprintf("  [UNUSED] %d/%d entries never injected into a prompt (curation candidates)\n", r.NeverUsed, r.Active))
		warnings++
	}

	if warnings == 0 {
		sb.WriteString("  Status: healthy (no issues detected)\n")
	} else {
		sb.WriteString(fmt.Sprintf("  Status: %d issue(s) need attention\n", warnings))
	}

	return strings.TrimRight(sb.String(), "\n")
}

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
