package memory

import (
	"regexp"
	"strings"
	"time"
)

// MemoryCategory classifies how a memory entry should be curated.
type MemoryCategory string

const (
	// CategoryTransient: one-time task records, bug fixes, implementation logs.
	// Expires after 30 days. Examples: impl-task-*, *-fix, *-bug.
	CategoryTransient MemoryCategory = "transient"

	// CategoryEvolving: research and analysis that gets superseded by newer
	// versions. Same-prefix dedup keeps only the latest.
	// Examples: competitor-*, research-*, perf-*, ux-research-*.
	CategoryEvolving MemoryCategory = "evolving"

	// CategoryPersistent: architecture decisions, design docs, build processes.
	// Never expires. Examples: *-impl, *-design, *-architecture, build-*.
	CategoryPersistent MemoryCategory = "persistent"

	// CategoryDefault: general memories with no special curation rules.
	CategoryDefault MemoryCategory = "default"
)

// MemoryMeta holds curation metadata for a memory file.
type MemoryMeta struct {
	Key       string         // sanitized filename without extension
	Category  MemoryCategory // auto-classified category
	CreatedAt time.Time      // file ModTime
	DedupKey  string         // prefix used for dedup (date/version stripped)
}

// transientExpiry is how long transient memories stay active.
const transientExpiry = 30 * 24 * time.Hour

// Patterns for classification (ordered: first match wins).
var transientPatterns = []*regexp.Regexp{
	regexp.MustCompile(`^impl-task-`),
	regexp.MustCompile(`-fix$`),
	regexp.MustCompile(`-bug$`),
	regexp.MustCompile(`-fix-`), // e.g. session-fix-bug
}

var evolvingPatterns = []*regexp.Regexp{
	regexp.MustCompile(`^competitor-`),
	regexp.MustCompile(`^research-`),
	regexp.MustCompile(`^perf-`),
	regexp.MustCompile(`^ux-research-`),
	regexp.MustCompile(`^frontier-`),
	regexp.MustCompile(`^multi-agent-research-`),
}

var persistentPatterns = []*regexp.Regexp{
	regexp.MustCompile(`-impl$`),
	regexp.MustCompile(`-design$`),
	regexp.MustCompile(`-architecture`),
	regexp.MustCompile(`^build-`),
	regexp.MustCompile(`^release-`),
}

// yearPattern matches a standalone 4-digit year.
var yearPattern = regexp.MustCompile(`^\d{4}$`)

// twoDigitPattern matches 2-digit month/day segments.
var twoDigitPattern = regexp.MustCompile(`^\d{2}$`)

// versionPattern matches r1, r2, r3, cycle1, cycle2, summary, r4 etc.
// These are always stripped regardless of position.
var versionPattern = regexp.MustCompile(`^(r\d+|cycle\d+|summary)$`)

// monthDayPattern matches jul6, jul10, aug1 etc.
var monthDayPattern = regexp.MustCompile(`^[a-z]{3}\d+$`)

// dedupKeyFor returns the dedup key for a memory key by stripping
// embedded date and version suffixes. This groups related entries
// (e.g. "competitor-analysis-2026-07-13-r3" → "competitor-analysis").
func dedupKeyFor(key string) string {
	parts := strings.Split(key, "-")
	var kept []string
	skippingDate := false // consuming YYYY-MM-DD or YYYY-MM
	afterDate := false    // past the date, skip version/monthday segments

	for _, part := range parts {
		// Version segments (r1, cycle2, summary) always stripped
		if versionPattern.MatchString(part) {
			continue
		}
		if skippingDate {
			// Consume MM or DD segments (2-digit)
			if twoDigitPattern.MatchString(part) {
				continue
			}
			skippingDate = false
			afterDate = true
		}
		if afterDate {
			if versionPattern.MatchString(part) || monthDayPattern.MatchString(part) {
				continue
			}
			afterDate = false // non-version segment = real content
		}
		// Check if this part starts a date (4-digit year)
		if yearPattern.MatchString(part) {
			skippingDate = true
			continue
		}
		// Check standalone jul6-style month+day tags (no preceding date)
		if monthDayPattern.MatchString(part) && !afterDate {
			continue
		}
		kept = append(kept, part)
	}
	result := strings.Join(kept, "-")
	for strings.Contains(result, "--") {
		result = strings.ReplaceAll(result, "--", "-")
	}
	return strings.Trim(result, "-")
}

// classifyMemory determines the curation category from the memory key.
func classifyMemory(key string) MemoryCategory {
	for _, p := range transientPatterns {
		if p.MatchString(key) {
			return CategoryTransient
		}
	}
	for _, p := range evolvingPatterns {
		if p.MatchString(key) {
			return CategoryEvolving
		}
	}
	for _, p := range persistentPatterns {
		if p.MatchString(key) {
			return CategoryPersistent
		}
	}
	return CategoryDefault
}

// shouldExpire returns true if a transient memory has exceeded its lifetime.
func shouldExpire(meta MemoryMeta, now time.Time) bool {
	if meta.Category != CategoryTransient {
		return false
	}
	return now.Sub(meta.CreatedAt) > transientExpiry
}

// curateEntries filters and deduplicates memory entries.
// Rules:
//   - Transient entries older than 30 days are excluded.
//   - Evolving entries with the same dedup key: only the newest is kept.
//   - All other entries are kept as-is.
//
// Returns the curated list and counts for diagnostics.
func curateEntries(metas []MemoryMeta, now time.Time) (active []MemoryMeta, expiredCount, dedupedCount int) {
	// Phase 1: filter expired transient entries.
	var survived []MemoryMeta
	for _, m := range metas {
		if shouldExpire(m, now) {
			expiredCount++
			continue
		}
		survived = append(survived, m)
	}

	// Phase 2: dedup evolving entries by DedupKey (keep newest).
	latestByDedup := make(map[string]MemoryMeta)
	for _, m := range survived {
		if m.Category != CategoryEvolving {
			continue
		}
		existing, ok := latestByDedup[m.DedupKey]
		if !ok || m.CreatedAt.After(existing.CreatedAt) {
			if ok {
				dedupedCount++
			}
			latestByDedup[m.DedupKey] = m
		} else {
			dedupedCount++
		}
	}

	// Phase 3: build final list — non-evolving + winning evolving entries.
	evolvingWinners := make(map[string]bool)
	for _, m := range latestByDedup {
		evolvingWinners[m.Key] = true
	}
	for _, m := range survived {
		if m.Category == CategoryEvolving {
			if evolvingWinners[m.Key] {
				active = append(active, m)
			}
			continue
		}
		active = append(active, m)
	}
	return active, expiredCount, dedupedCount
}

// buildMemoryMeta creates metadata for a memory file from its key and modtime.
func buildMemoryMeta(key string, modTime time.Time) MemoryMeta {
	return MemoryMeta{
		Key:       key,
		Category:  classifyMemory(key),
		CreatedAt: modTime,
		DedupKey:  dedupKeyFor(key),
	}
}

// formatMemorySummary returns a one-line diagnostic summary of curation results.
func formatMemorySummary(total, active, expired, deduped int) string {
	var sb strings.Builder
	sb.WriteString("memory: ")
	sb.WriteString(activeStr(active, total))
	if expired > 0 {
		sb.WriteString(", expired=")
		// convert int to string without fmt for test simplicity
		sb.WriteString(itoa(expired))
	}
	if deduped > 0 {
		sb.WriteString(", deduped=")
		sb.WriteString(itoa(deduped))
	}
	return sb.String()
}

func activeStr(active, total int) string {
	if active == total {
		return "all " + itoa(total) + " active"
	}
	return itoa(active) + "/" + itoa(total) + " active"
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
