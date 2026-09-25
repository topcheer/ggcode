package memory

import (
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
)

// Date-horizon expiry ("temporal self-update", cf. OpenAI Dreaming V3 and
// MaRS retention schemas, 2026): a memory whose content is bound to a
// calendar horizon ("deadline 2026-09-30", "until Oct 1", "截止 2026年9月30日")
// keeps being injected into every system prompt even after ALL of its
// horizons have passed — the classic proactive-interference failure mode of
// unbounded retention. Curation already retires transient-category entries
// by file age (transientExpiry), but evolving/default entries with expired
// content horizons never retire.
//
// This file adds a deterministic, content-based retirement signal:
//
//   - horizon dates are absolute calendar dates that appear in a SENTENCE
//     also containing a temporal cue (deadline, until, 截止, ...). Dates in
//     sentences without cues are historical facts ("released 2025-01-15")
//     and NEVER trigger expiry (conservative by design).
//   - an entry is horizon-expired when it has >=1 horizon date and the
//     LATEST one is older than horizonGrace. Any live horizon keeps the
//     whole entry active.
//
// Doctrine compliance:
//   - only curation hides; nothing is deleted here. GC explicitly
//     whitelists horizon-expired entries (only expiry-by-category and
//     dedup losers are deletable, per #779).
//   - CategoryPersistent is documented "Never expires" — the filter never
//     hides persistent entries; staleness/consolidation may still report
//     them as review signals.

// horizonGrace is the slack between the last horizon date and retirement,
// absorbing timezone skew and same-day references.
const horizonGrace = 72 * time.Hour

// Plausible calendar-year bounds so version strings and port numbers never
// parse as dates.
const (
	minHorizonYear = 2000
	maxHorizonYear = 2100
)

// horizonMatcher holds the absolute-calendar-date patterns:
//   - iso:  2026-09-30 / 2026-9-30 / 2026/09/30
//   - cjk:  2026年9月30日
//   - mono: September 30, 2026 / Sep 30, 2026 / sep 30 2026
type horizonMatcher struct {
	iso, cjk, mono *regexp.Regexp
}

var horizonDatePattern = horizonMatcher{
	iso:  regexp.MustCompile(`(\d{4})[-/](\d{1,2})[-/](\d{1,2})`),
	cjk:  regexp.MustCompile(`(\d{4})年(\d{1,2})月(\d{1,2})日`),
	mono: regexp.MustCompile(`(?i)\b(jan|feb|mar|apr|may|jun|jul|aug|sep|sept|oct|nov|dec)[a-z]*\.?\s+(\d{1,2})(?:st|nd|rd|th)?\s*,?\s+(\d{4})\b`),
}

// horizonCues: a sentence must contain one of these (case-insensitive) to
// make its dates binding. EN and ZH cues share one flat list.
var horizonCues = []string{
	// English
	"until ", "valid until", "valid through", "deadline", "due ", "expire",
	"expiry", "scheduled", "planned for", "planned to", "starts on",
	"starts", "ends on", "ends ", "window", "eta ", "eta:", "no later than",
	"by ", "before ", "after ", "closes on", "opens on", "begins",
	// Chinese
	"截止", "到期", "过期", "之前", "以前", "之后", "以后", "将于",
	"限期", "期限", "开始", "结束", "窗口", "预计", "计划",
}

// monthIndex maps EN month prefixes to month numbers.
var monthIndex = map[string]time.Month{
	"jan": time.January, "feb": time.February, "mar": time.March,
	"apr": time.April, "may": time.May, "jun": time.June,
	"jul": time.July, "aug": time.August, "sep": time.September,
	"oct": time.October, "nov": time.November, "dec": time.December,
}

// sentenceSplit breaks content into candidate sentences on newlines, CJK
// terminators, and common EN terminators.
func sentenceSplit(content string) []string {
	f := func(r rune) bool {
		switch r {
		case '\n', '。', '！', '？', '!', '?', ';', '；':
			return true
		}
		return false
	}
	parts := strings.FieldsFunc(content, f)
	// ". " also terminates sentences; FieldsFunc can't express pairs, so a
	// second pass splits on it.
	var out []string
	for _, p := range parts {
		out = append(out, strings.Split(p, ". ")...)
	}
	return out
}

// parseHorizonDate validates and constructs a date; returns zero time for
// implausible components (month 13, day 32, out-of-range years).
func parseHorizonDate(y, m, d int) time.Time {
	if y < minHorizonYear || y > maxHorizonYear || m < 1 || m > 12 || d < 1 || d > 31 {
		return time.Time{}
	}
	t := time.Date(y, time.Month(m), d, 0, 0, 0, 0, time.Local)
	// time.Date normalizes overflow (Feb 31 -> Mar 3); reject normalized
	// mismatches so 2026-02-31 is not silently accepted.
	if t.Day() != d || int(t.Month()) != m {
		return time.Time{}
	}
	return t
}

// extractHorizonDates parses all binding horizon dates in content: a date
// only counts when its sentence contains a temporal cue.
func extractHorizonDates(content string) []time.Time {
	var out []time.Time
	for _, sent := range sentenceSplit(content) {
		if !sentenceHasCue(sent) {
			continue
		}
		out = append(out, extractDatesFromSentence(sent)...)
	}
	return out
}

func sentenceHasCue(sent string) bool {
	if sent == "" {
		return false
	}
	lower := strings.ToLower(sent)
	for _, cue := range horizonCues {
		// ToLower leaves CJK unchanged, so one Contains covers both scripts.
		if strings.Contains(lower, cue) {
			return true
		}
	}
	return false
}

func extractDatesFromSentence(sent string) []time.Time {
	var out []time.Time
	for _, loc := range horizonDatePattern.iso.FindAllStringSubmatchIndex(sent, -1) {
		if t := parseHorizonDate(atoiSent(sent[loc[2]:loc[3]]), atoiSent(sent[loc[4]:loc[5]]), atoiSent(sent[loc[6]:loc[7]])); !t.IsZero() {
			out = append(out, t)
		}
	}
	for _, loc := range horizonDatePattern.cjk.FindAllStringSubmatchIndex(sent, -1) {
		if t := parseHorizonDate(atoiSent(sent[loc[2]:loc[3]]), atoiSent(sent[loc[4]:loc[5]]), atoiSent(sent[loc[6]:loc[7]])); !t.IsZero() {
			out = append(out, t)
		}
	}
	for _, loc := range horizonDatePattern.mono.FindAllStringSubmatchIndex(sent, -1) {
		mon := strings.ToLower(sent[loc[2]:loc[3]])
		if len(mon) > 3 {
			mon = mon[:3]
		}
		mm, ok := monthIndex[mon]
		if !ok {
			continue
		}
		if t := parseHorizonDate(atoiSent(sent[loc[6]:loc[7]]), int(mm), atoiSent(sent[loc[4]:loc[5]])); !t.IsZero() {
			out = append(out, t)
		}
	}
	return out
}

// atoiSent parses a small ASCII integer; returns 0 on garbage so
// parseHorizonDate's range checks reject it.
func atoiSent(s string) int {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0
		}
		n = n*10 + int(r-'0')
	}
	return n
}

// latestHorizon returns the furthest horizon date (zero time when none).
func latestHorizon(horizons []time.Time) time.Time {
	var latest time.Time
	for _, h := range horizons {
		if h.After(latest) {
			latest = h
		}
	}
	return latest
}

// dateHorizonVerdict returns the binding horizons of content and whether
// every horizon has passed (plus grace). No horizons => never expired.
func dateHorizonVerdict(content string, now time.Time) (horizons []time.Time, passed bool) {
	horizons = extractHorizonDates(content)
	if len(horizons) == 0 {
		return nil, false
	}
	return horizons, now.After(latestHorizon(horizons).Add(horizonGrace))
}

// horizonMemoEntry caches one verdict, keyed by ModTime so an overwritten
// entry (new horizon) is re-evaluated without rescans.
type horizonMemoEntry struct {
	modTime time.Time
	expired bool
}

// filterDateExpired splits curateEntries' active set into entries that stay
// prompt-visible and entries whose content horizons have all passed.
// CategoryPersistent is never filtered (documented "Never expires" contract).
func (am *AutoMemory) filterDateExpired(active []MemoryMeta, now time.Time) (kept, expired []MemoryMeta) {
	for _, m := range active {
		if m.Category == CategoryPersistent {
			kept = append(kept, m)
			continue
		}
		if am.horizonExpired(m, now) {
			expired = append(expired, m)
		} else {
			kept = append(kept, m)
		}
	}
	return kept, expired
}

// horizonExpired evaluates (with memo) whether one entry's horizons passed.
func (am *AutoMemory) horizonExpired(m MemoryMeta, now time.Time) bool {
	memoKey := am.dir + "\x00" + m.Key
	if v, ok := am.horizonCache.Load(memoKey); ok {
		if e, ok := v.(horizonMemoEntry); ok && e.modTime.Equal(m.CreatedAt) {
			return e.expired
		}
	}
	data, err := readEntryHead(am.dir, m.Key)
	if err != nil {
		if !os.IsNotExist(err) {
			debug.Log("memory", "horizon scan: read %s: %v", m.Key, err)
		}
		return false // unreadable entries stay visible (fail-open)
	}
	_, passed := dateHorizonVerdict(string(data), now)
	am.horizonCache.Store(memoKey, horizonMemoEntry{modTime: m.CreatedAt, expired: passed})
	if passed {
		debug.Log("memory", "horizon expiry: %s retired from prompt set", m.Key)
	}
	return passed
}

// dateExpiredKeys returns the keys of active entries hidden by horizon
// expiry. GC uses this to whitelist them (non-destructive doctrine).
func (am *AutoMemory) dateExpiredKeys(active []MemoryMeta, now time.Time) map[string]bool {
	_, expired := am.filterDateExpired(active, now)
	keys := make(map[string]bool, len(expired))
	for _, m := range expired {
		keys[m.Key] = true
	}
	return keys
}

// horizonExpiryDetail returns a human-readable note for reporting surfaces
// (staleness scan, consolidation) about an entry whose horizons passed.
func horizonExpiryDetail(content string, now time.Time) string {
	horizons, passed := dateHorizonVerdict(content, now)
	if !passed || len(horizons) == 0 {
		return ""
	}
	latest := latestHorizon(horizons)
	return "date-passed: horizon " + latest.Format("2006-01-02") +
		" passed " + formatDuration(now.Sub(latest)) + " ago"
}
