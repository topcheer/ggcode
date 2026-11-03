package memory

import (
	"regexp"
	"sort"
	"strings"
	"time"
)

type MemoryCategory string

const (
	CategoryTransient MemoryCategory = "transient"

	CategoryEvolving MemoryCategory = "evolving"

	CategoryPersistent MemoryCategory = "persistent"

	CategoryDefault MemoryCategory = "default"
)

type MemoryMeta struct {
	Key       string
	Category  MemoryCategory
	CreatedAt time.Time
	DedupKey  string

	Uses       int
	LastUsedAt time.Time
	Source     string

	Outcome string

	Conflicts int
}

const transientExpiry = 30 * 24 * time.Hour

const maxActiveMemories = 60

var transientPatterns = []*regexp.Regexp{
	regexp.MustCompile(`^impl-task-`),
	regexp.MustCompile(`-fix$`),
	regexp.MustCompile(`-bug$`),
	regexp.MustCompile(`-fix-`),
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

var yearPattern = regexp.MustCompile(`^\d{4}$`)

var twoDigitPattern = regexp.MustCompile(`^\d{2}$`)

var versionPattern = regexp.MustCompile(`^(r\d+|cycle\d+)$`)

var monthDayPattern = regexp.MustCompile(`^(?:jan|feb|mar|apr|may|jun|jul|aug|sep|oct|nov|dec)[1-9]\d?$`)

func dedupKeyFor(key string) string {
	parts := strings.Split(key, "-")
	var kept []string
	skippingDate := false
	afterDate := false

	for i, part := range parts {

		if versionPattern.MatchString(part) || (part == "summary" && i == len(parts)-1) {
			continue
		}
		if skippingDate {

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
			afterDate = false
		}

		if yearPattern.MatchString(part) && i+1 < len(parts) && twoDigitPattern.MatchString(parts[i+1]) {
			skippingDate = true
			continue
		}

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

func classifyMemory(key string) MemoryCategory {

	for _, p := range persistentPatterns {
		if p.MatchString(key) {
			return CategoryPersistent
		}
	}
	for _, p := range evolvingPatterns {
		if p.MatchString(key) {
			return CategoryEvolving
		}
	}
	for _, p := range transientPatterns {
		if p.MatchString(key) {
			return CategoryTransient
		}
	}
	return CategoryDefault
}

func shouldExpire(meta MemoryMeta, now time.Time) bool {
	if meta.Category != CategoryTransient {
		return false
	}
	return now.Sub(meta.CreatedAt) > transientExpiry
}

func curateEntries(metas []MemoryMeta, now time.Time) (active []MemoryMeta, expiredCount, dedupedCount, cappedCount int) {

	var survived []MemoryMeta
	for _, m := range metas {
		if shouldExpire(m, now) {
			expiredCount++
			continue
		}
		survived = append(survived, m)
	}

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

	active, capped := capByCountSplit(active, maxActiveMemories)
	cappedCount = len(capped)

	return active, expiredCount, dedupedCount, cappedCount
}

func capByCount(active []MemoryMeta, max int) ([]MemoryMeta, int) {
	kept, evicted := capByCountSplit(active, max)
	return kept, len(evicted)
}

func capByCountSplit(active []MemoryMeta, max int) (kept, evicted []MemoryMeta) {
	if len(active) <= max {
		return active, nil
	}

	var defaults []MemoryMeta
	for _, m := range active {
		if m.Category == CategoryDefault {
			defaults = append(defaults, m)
		}
	}

	overflow := len(active) - max
	if overflow > len(defaults) {
		overflow = len(defaults)
	}
	if overflow <= 0 {
		return active, nil
	}

	usageTier := func(m MemoryMeta) int {
		if m.Conflicts >= 2 {
			return 0
		}
		if m.Uses == 0 {
			return 1
		}
		return 2
	}
	sort.Slice(defaults, func(i, j int) bool {
		ti, tj := usageTier(defaults[i]), usageTier(defaults[j])
		if ti != tj {
			return ti < tj
		}
		if !defaults[i].CreatedAt.Equal(defaults[j].CreatedAt) {
			return defaults[i].CreatedAt.Before(defaults[j].CreatedAt)
		}
		return defaults[i].Key < defaults[j].Key
	})
	evict := make(map[string]bool, overflow)
	for i := 0; i < overflow; i++ {
		evict[defaults[i].Key] = true
	}

	var keptList, evictedList []MemoryMeta
	for _, m := range active {
		if evict[m.Key] {
			evictedList = append(evictedList, m)
		} else {
			keptList = append(keptList, m)
		}
	}
	return keptList, evictedList
}

func buildMemoryMeta(key string, modTime time.Time) MemoryMeta {
	return MemoryMeta{
		Key:       key,
		Category:  classifyMemory(key),
		CreatedAt: modTime,
		DedupKey:  dedupKeyFor(key),
	}
}

func formatMemorySummary(total, active, expired, deduped, capped int) string {
	var sb strings.Builder
	sb.WriteString("memory: ")
	sb.WriteString(activeStr(active, total))
	if expired > 0 {
		sb.WriteString(", expired=")

		sb.WriteString(itoa(expired))
	}
	if deduped > 0 {
		sb.WriteString(", deduped=")
		sb.WriteString(itoa(deduped))
	}
	if capped > 0 {
		sb.WriteString(", capped=")
		sb.WriteString(itoa(capped))
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
