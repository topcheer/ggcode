package memory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestExtractHorizonDates_Formats(t *testing.T) {
	past := time.Date(2026, 9, 30, 0, 0, 0, 0, time.Local)
	future := time.Date(2027, 3, 1, 0, 0, 0, 0, time.Local)

	cases := []struct {
		name    string
		content string
		want    []time.Time
	}{
		{"iso dash", "deadline is 2026-09-30 for the release", []time.Time{past}},
		{"iso slash", "valid through 2026/09/30", []time.Time{past}},
		{"cjk", "本窗口将于2026年9月30日结束", []time.Time{past}},
		{"en month", "the review window closes on September 30, 2026", []time.Time{past}},
		{"en month abbr", "due Sep 30 2026", []time.Time{past}},
		// Historical facts: date present, no cue in the sentence.
		{"historical no cue", "released 2026-09-30 to production", nil},
		// Implausible dates rejected even with cues.
		{"invalid month", "deadline 2026-13-40 in the plan", nil},
		{"invalid day", "expires 2026-02-31 per contract", nil},
		// Future horizon inside a mixed entry: both dates parse.
		{"mixed horizons", "phase 1 was due 2026-09-30; phase 2 is due 2027-03-01", []time.Time{past, future}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := extractHorizonDates(tc.content)
			if len(got) != len(tc.want) {
				t.Fatalf("extractHorizonDates(%q) = %v, want %d dates", tc.content, got, len(tc.want))
			}
			for i, w := range tc.want {
				if !got[i].Equal(w) {
					t.Errorf("date[%d] = %v, want %v", i, got[i], w)
				}
			}
		})
	}
}

func TestDateHorizonVerdict(t *testing.T) {
	now := time.Date(2026, 10, 10, 0, 0, 0, 0, time.Local)

	if _, passed := dateHorizonVerdict("deadline 2026-09-30 for the migration", now); !passed {
		t.Error("past horizon + grace should be passed")
	}
	if _, passed := dateHorizonVerdict("deadline 2026-12-31 for the migration", now); passed {
		t.Error("future horizon must stay active")
	}
	if _, passed := dateHorizonVerdict("we shipped on 2026-09-30", now); passed {
		t.Error("historical date without cue must never expire")
	}
	// Latest horizon wins: one live horizon keeps the entry active.
	if _, passed := dateHorizonVerdict("phase 1 due 2026-09-30, phase 2 due 2027-01-15", now); passed {
		t.Error("entry with a live horizon must stay active")
	}
	if _, passed := dateHorizonVerdict("no dates here at all", now); passed {
		t.Error("entries without horizons never expire")
	}
	// Within grace window: not yet expired.
	if _, passed := dateHorizonVerdict("deadline 2026-10-09 for cleanup", now); passed {
		t.Error("horizon inside 72h grace must stay active")
	}
}

// writeEntryFile writes an entry file directly (bypasses SaveMemory's key
// sanitization) and backdates its ModTime.
func writeEntryFile(t *testing.T, dir, key, content string, mod time.Time) {
	t.Helper()
	path := filepath.Join(dir, key+".md")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write %s: %v", key, err)
	}
	if err := os.Chtimes(path, mod, mod); err != nil {
		t.Fatalf("chtimes %s: %v", key, err)
	}
}

func TestFilterDateExpired_CategoryGuard(t *testing.T) {
	tmpDir := t.TempDir()
	am := &AutoMemory{dir: tmpDir}
	old := time.Date(2026, 8, 1, 12, 0, 0, 0, time.Local)
	now := time.Date(2026, 10, 10, 0, 0, 0, 0, time.Local)

	writeEntryFile(t, tmpDir, "research-window", "research r99: the merge window closes 2026-09-30", old)
	writeEntryFile(t, tmpDir, "build-setup-impl", "build pipeline steps; first phase deadline was 2026-09-30", old)
	writeEntryFile(t, tmpDir, "notes-future", "rollout planned for 2027-01-15", old)
	writeEntryFile(t, tmpDir, "notes-history", "v1.3 shipped 2026-09-30 without issues", old)

	metas, err := am.collectMetas()
	if err != nil {
		t.Fatalf("collectMetas: %v", err)
	}
	active, _, _, _ := curateEntries(metas, now)
	kept, expired := am.filterDateExpired(active, now)

	expiredKeys := map[string]bool{}
	for _, m := range expired {
		expiredKeys[m.Key] = true
	}
	keptKeys := map[string]bool{}
	for _, m := range kept {
		keptKeys[m.Key] = true
	}

	if !expiredKeys["research-window"] {
		t.Errorf("evolving entry with passed horizon should be expired; got %v", expiredKeys)
	}
	if expiredKeys["build-setup-impl"] {
		t.Error("persistent entries are documented Never expires; must not be filtered")
	}
	if expiredKeys["notes-future"] {
		t.Error("entry with live horizon must stay active")
	}
	if expiredKeys["notes-history"] {
		t.Error("historical date without cue must not expire")
	}
	if !keptKeys["build-setup-impl"] || !keptKeys["notes-future"] || !keptKeys["notes-history"] {
		t.Errorf("kept set missing entries: %v", keptKeys)
	}
}

// horizonStr formats a date for embedding in test content.
func horizonStr(t time.Time) string { return t.Format("2006-01-02") }

func TestLoadIndex_HidesDateExpired(t *testing.T) {
	tmpDir := t.TempDir()
	am := &AutoMemory{dir: tmpDir}
	past := time.Now().AddDate(0, 0, -30)

	writeEntryFile(t, tmpDir, "research-expired", "merge window ends "+horizonStr(past), past)
	writeEntryFile(t, tmpDir, "research-live", "next window opens "+horizonStr(time.Now().AddDate(0, 0, 90)), past)

	index, files, err := am.LoadIndex()
	if err != nil {
		t.Fatalf("LoadIndex: %v", err)
	}
	if strings.Contains(index, "research-expired") {
		t.Errorf("date-expired entry must not appear in index:\n%s", index)
	}
	if !strings.Contains(index, "research-live") {
		t.Errorf("live entry missing from index:\n%s", index)
	}
	for _, f := range files {
		if strings.HasSuffix(f, "research-expired.md") {
			t.Error("date-expired entry must not be listed in files")
		}
	}
}

func TestHorizonMemo_InvalidationOnRewrite(t *testing.T) {
	tmpDir := t.TempDir()
	am := &AutoMemory{dir: tmpDir}
	old := time.Date(2026, 8, 1, 12, 0, 0, 0, time.Local)
	later := old.Add(48 * time.Hour)
	now := time.Date(2026, 10, 10, 0, 0, 0, 0, time.Local)

	writeEntryFile(t, tmpDir, "research-refresh", "window ends 2026-09-30", old)
	metas, _ := am.collectMetas()
	active, _, _, _ := curateEntries(metas, now)
	if _, expired := am.filterDateExpired(active, now); len(expired) != 1 {
		t.Fatalf("expected 1 expired before rewrite, got %d", len(expired))
	}

	// Rewrite with a new horizon and a fresh ModTime; the memo keyed by
	// ModTime must re-evaluate.
	writeEntryFile(t, tmpDir, "research-refresh", "window extended to 2027-06-30", later)
	metas, _ = am.collectMetas()
	active, _, _, _ = curateEntries(metas, now)
	if _, expired := am.filterDateExpired(active, now); len(expired) != 0 {
		t.Fatalf("expected 0 expired after rewrite, got %d", len(expired))
	}
}

func TestGarbageCollect_KeepsDateExpired(t *testing.T) {
	tmpDir := t.TempDir()
	am := &AutoMemory{dir: tmpDir}
	past := time.Now().AddDate(0, 0, -30)

	writeEntryFile(t, tmpDir, "research-expired", "window ends "+horizonStr(past), past)
	stats := am.GarbageCollect()
	if stats.Total == 0 {
		t.Fatal("GC scanned nothing")
	}
	if _, err := os.Stat(filepath.Join(tmpDir, "research-expired.md")); err != nil {
		t.Error("GC must not delete horizon-expired entries (non-destructive doctrine)")
	}
	// The entry must also be hidden from the prompt set.
	_, files, err := am.LoadIndex()
	if err != nil {
		t.Fatalf("LoadIndex: %v", err)
	}
	for _, f := range files {
		if strings.HasSuffix(f, "research-expired.md") {
			t.Error("horizon-expired entry should not stay prompt-active")
		}
	}
}

func TestScanStaleness_DatePassed(t *testing.T) {
	tmpDir := t.TempDir()
	am := &AutoMemory{dir: tmpDir}
	past := time.Now().AddDate(0, 0, -30)

	writeEntryFile(t, tmpDir, "research-expired", "window ends "+horizonStr(past), past)
	report := am.ScanStaleness("")
	if report.DatePassed != 1 {
		t.Fatalf("DatePassed = %d, want 1", report.DatePassed)
	}
	found := false
	for _, f := range report.Findings {
		if f.Reason == "date-passed" && strings.Contains(f.Detail, horizonStr(past)) {
			found = true
		}
	}
	if !found {
		t.Errorf("expected date-passed finding with horizon detail, got %+v", report.Findings)
	}
}

func TestConsolidate_ReportsDatePassed(t *testing.T) {
	tmpDir := t.TempDir()
	am := &AutoMemory{dir: tmpDir}
	past := time.Now().AddDate(0, 0, -30)

	writeEntryFile(t, tmpDir, "research-expired", "merge window ends "+horizonStr(past), past)
	report := am.Consolidate("")
	if report.StaleFindings == 0 {
		t.Fatal("consolidation should surface date-passed as a stale finding")
	}
	reported := false
	for _, w := range report.Warnings {
		if strings.Contains(w, "research-expired") && strings.Contains(w, "date-passed") {
			reported = true
		}
	}
	if !reported {
		t.Errorf("warnings should mention research-expired date-passed; got %v", report.Warnings)
	}
}

func TestHealthReport_DateExpiredHidden(t *testing.T) {
	tmpDir := t.TempDir()
	am := &AutoMemory{dir: tmpDir}
	past := time.Now().AddDate(0, 0, -30)

	writeEntryFile(t, tmpDir, "research-expired", "window ends "+horizonStr(past), past)
	hr := am.HealthReport("")
	if hr.DateExpiredHidden != 1 {
		t.Fatalf("DateExpiredHidden = %d, want 1", hr.DateExpiredHidden)
	}
	if hr.Active != 0 {
		t.Errorf("Active = %d, want 0 (hidden entry not injectable)", hr.Active)
	}
	if !strings.Contains(hr.FormatHealthReport(), "HORIZON-EXPIRED") {
		t.Errorf("health summary should mention horizon-expired:\n%s", hr.FormatHealthReport())
	}
}

func TestLoadForPrompt_HidesDateExpired(t *testing.T) {
	tmpDir := t.TempDir()
	am := &AutoMemory{dir: tmpDir}
	past := time.Now().AddDate(0, 0, -30)

	writeEntryFile(t, tmpDir, "research-expired", "window ends "+horizonStr(past), past)
	inline, indexOnly, err := am.LoadForPrompt()
	if err != nil {
		t.Fatalf("LoadForPrompt: %v", err)
	}
	if len(inline) != 0 || len(indexOnly) != 0 {
		t.Errorf("date-expired entry must not be injected; inline=%v indexOnly=%v", inline, indexOnly)
	}
}
