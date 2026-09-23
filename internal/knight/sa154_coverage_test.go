package knight

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/session"
)

// sa-154: coverage supplements for previously untested pure helpers and
// lightly-covered state paths (79.2% -> target >= 85%). No production code
// is modified by this file.

// --- candidate_queue.go ---

func TestSa154CandidateOrderLess(t *testing.T) {
	now := time.Now()
	earlier := now.Add(-time.Hour)

	cases := []struct {
		name string
		a, b SkillCandidate
		want bool
	}{
		{"priority higher first", SkillCandidate{QueuePriority: 5}, SkillCandidate{QueuePriority: 3}, true},
		{"priority lower not less", SkillCandidate{QueuePriority: 3}, SkillCandidate{QueuePriority: 5}, false},
		{"zero time loses to set time", SkillCandidate{QueuePriority: 1}, SkillCandidate{QueuePriority: 1, FirstQueuedAt: now}, false},
		{"set time beats zero time", SkillCandidate{QueuePriority: 1, FirstQueuedAt: now}, SkillCandidate{QueuePriority: 1}, true},
		{"earlier queued first", SkillCandidate{QueuePriority: 1, FirstQueuedAt: earlier}, SkillCandidate{QueuePriority: 1, FirstQueuedAt: now}, true},
		{"more evidence first", SkillCandidate{QueuePriority: 1, FirstQueuedAt: now, EvidenceCount: 3}, SkillCandidate{QueuePriority: 1, FirstQueuedAt: now, EvidenceCount: 2}, true},
		{"higher score first", SkillCandidate{QueuePriority: 1, FirstQueuedAt: now, Score: 9}, SkillCandidate{QueuePriority: 1, FirstQueuedAt: now, Score: 4}, true},
		{"tie broken by name", SkillCandidate{QueuePriority: 1, Name: "alpha"}, SkillCandidate{QueuePriority: 1, Name: "beta"}, true},
	}
	for _, tc := range cases {
		if got := candidateOrderLess(tc.a, tc.b); got != tc.want {
			t.Errorf("%s: candidateOrderLess = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestSa154UniqueSortedStrings(t *testing.T) {
	got := uniqueSortedStrings([]string{"b ", " a", "b", "", "a", "  "})
	want := []string{"a", "b"}
	if len(got) != len(want) {
		t.Fatalf("uniqueSortedStrings = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("uniqueSortedStrings = %v, want %v", got, want)
		}
	}
	if out := uniqueSortedStrings(nil); len(out) != 0 {
		t.Errorf("nil input should yield empty, got %v", out)
	}
}

// --- governance.go ---

func TestSa154RecommendationFormatHuman(t *testing.T) {
	full := SkillActionRecommendation{
		Ref: "a|b", Action: "merge-review", Priority: "high",
		Reason: "overlapping triggers", Command: "ggcode /skills review",
	}.formatHuman()
	for _, want := range []string{"[high]", "merge-review", "overlapping triggers", "(next: ggcode /skills review)"} {
		if !strings.Contains(full, want) {
			t.Errorf("formatHuman missing %q in %q", want, full)
		}
	}
	min := SkillActionRecommendation{Ref: "x", Action: "prune", Priority: "low"}.formatHuman()
	if strings.Contains(min, "\u2014") || strings.Contains(min, "(next:") {
		t.Errorf("minimal formatHuman should omit reason/command: %q", min)
	}
}

func TestSa154AuditFormatHuman(t *testing.T) {
	audit := SkillGovernanceAudit{
		Window:            time.Hour,
		ActiveSkills:      3,
		StagingSkills:     1,
		StaleGlobalSkills: []string{"old-one"},
		Recommendations: []SkillActionRecommendation{
			{Ref: "old-one", Action: "prune", Priority: "medium", Reason: "stale"},
		},
	}
	out := audit.FormatHuman()
	for _, want := range []string{"Knight governance audit", "active skills: 3", "stale global skills (1): old-one", "recommended actions (1)"} {
		if !strings.Contains(out, want) {
			t.Errorf("FormatHuman missing %q in %q", want, out)
		}
	}
	if !strings.Contains(SkillGovernanceAudit{Window: time.Hour}.FormatHuman(), "active skills: 0") {
		t.Error("minimal audit should still render header and counts")
	}
}

func TestSa154AddSkillRecommendation(t *testing.T) {
	dst := map[string]SkillActionRecommendation{}

	// Empty ref/action entries are dropped.
	addSkillRecommendation(dst, SkillActionRecommendation{Action: "prune"})
	addSkillRecommendation(dst, SkillActionRecommendation{Ref: "x"})
	if len(dst) != 0 {
		t.Fatalf("invalid recommendations should be dropped, got %v", dst)
	}

	// Default priority is medium.
	addSkillRecommendation(dst, SkillActionRecommendation{Ref: "a", Action: "prune"})
	if dst["prune|a"].Priority != "medium" {
		t.Errorf("default priority = %q, want medium", dst["prune|a"].Priority)
	}

	// Upgrade priority and append a disjoint reason.
	addSkillRecommendation(dst, SkillActionRecommendation{Ref: "a", Action: "prune", Priority: "high", Reason: "also stale", Command: "do-it"})
	merged := dst["prune|a"]
	if merged.Priority != "high" || merged.Command != "do-it" {
		t.Errorf("merge failed: %+v", merged)
	}
	if !strings.Contains(merged.Reason, "also stale") {
		t.Errorf("reason not merged: %q", merged.Reason)
	}

	// Downgrade is ignored; duplicate reason is not appended twice.
	addSkillRecommendation(dst, SkillActionRecommendation{Ref: "a", Action: "prune", Priority: "low", Reason: "also stale"})
	merged = dst["prune|a"]
	if merged.Priority != "high" {
		t.Errorf("priority downgrade should be ignored: %q", merged.Priority)
	}
	if strings.Count(merged.Reason, "also stale") != 1 {
		t.Errorf("duplicate reason appended: %q", merged.Reason)
	}
}

func TestSa154PriorityHelpers(t *testing.T) {
	if priorityRank("HIGH") != 3 || priorityRank(" medium ") != 2 || priorityRank("low") != 1 || priorityRank("other") != 0 {
		t.Error("priorityRank ranking unexpected")
	}
	if priorityForRejectCount(2) != "high" || priorityForRejectCount(10) != "high" || priorityForRejectCount(1) != "medium" || priorityForRejectCount(0) != "medium" {
		t.Error("priorityForRejectCount unexpected")
	}
}

func TestSa154StalenessHelpers(t *testing.T) {
	cutoff := time.Now().Add(-24 * time.Hour)

	if skillLooksStale(nil, skillUsage{}, cutoff) {
		t.Error("nil entry must never look stale")
	}

	used := skillUsage{LastUsed: cutoff.Add(-time.Hour)}
	entry := &SkillEntry{Name: "e"}
	if !skillLooksStale(entry, used, cutoff) {
		t.Error("last-used before cutoff should be stale")
	}
	if got := staleReason(entry, used, 0, cutoff); !strings.Contains(got, "no confirmed use since") {
		t.Errorf("staleReason = %q, want 'no confirmed use since'", got)
	}

	// Old skill, never used, heavily exposed -> stale with "never explicitly used".
	old := &SkillEntry{Meta: SkillMeta{CreatedAt: cutoff.Add(-time.Hour).Format(time.RFC3339)}}
	exposed := skillUsage{PromptExposureCount: 1 << 20}
	if !skillLooksStale(old, exposed, cutoff) {
		t.Error("old never-used over-exposed skill should be stale")
	}
	if got := staleReason(old, exposed, 7, cutoff); !strings.Contains(got, "never explicitly used") {
		t.Errorf("staleReason = %q, want 'never explicitly used'", got)
	}

	// Low exposure: not stale via the created path.
	if skillLooksStale(old, skillUsage{}, cutoff) {
		t.Error("old skill with no exposures should not be stale (created path needs exposure)")
	}

	// Otherwise: fallback reason.
	if got := staleReason(entry, skillUsage{}, 0, cutoff); got != "skill looks stale" {
		t.Errorf("staleReason fallback = %q", got)
	}
}

func TestSa154SkillCreatedAt(t *testing.T) {
	if !skillCreatedAt(nil).IsZero() {
		t.Error("nil entry should yield zero time")
	}
	if !skillCreatedAt(&SkillEntry{}).IsZero() {
		t.Error("empty CreatedAt should yield zero time")
	}
	if !skillCreatedAt(&SkillEntry{Meta: SkillMeta{CreatedAt: "not-a-time"}}).IsZero() == true {
		t.Error("invalid CreatedAt should yield zero time")
	}
	want := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	got := skillCreatedAt(&SkillEntry{Meta: SkillMeta{CreatedAt: "2026-01-02T03:04:05Z"}})
	if !got.Equal(want) {
		t.Errorf("skillCreatedAt = %v, want %v", got, want)
	}
}

// --- usage_tracker.go ---

func TestSa154UsageTrackerAllUsageFlushAndReload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "usage.json")
	ut := NewUsageTracker(path)
	if err := ut.EnsureDir(); err != nil {
		t.Fatalf("EnsureDir: %v", err)
	}
	ut.RecordUse("alpha")
	ut.RecordPromptExposure("alpha")
	ut.RecordPromptOutcome("alpha", true)
	ut.RecordEffectiveness("alpha", 4)

	snap := ut.AllUsage()
	if len(snap) != 1 {
		t.Fatalf("AllUsage len = %d, want 1", len(snap))
	}
	if snap["alpha"].UsageCount != 1 {
		t.Errorf("UsageCount = %d, want 1", snap["alpha"].UsageCount)
	}

	ut.Flush()
	reloaded := NewUsageTracker(path)
	after := reloaded.AllUsage()
	if len(after) != 1 || after["alpha"].UsageCount != 1 {
		t.Errorf("reload after Flush = %+v, want persisted alpha entry", after)
	}
}

// --- skill_index.go ---

func TestSa154FindActiveByName(t *testing.T) {
	home := t.TempDir()
	proj := t.TempDir()
	si := NewSkillIndex(home, proj)

	writeSkill := func(dir, name string) {
		t.Helper()
		skillDir := filepath.Join(dir, ".ggcode", "skills", name)
		if err := os.MkdirAll(skillDir, 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		content := "---\nname: " + name + "\ndescription: sa154 test skill\n---\n# " + name + "\nbody\n"
		if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
	}

	if e := si.FindActiveByName("missing"); e != nil {
		t.Fatalf("expected nil for missing skill, got %+v", e)
	}

	writeSkill(home, "global-one")
	if e := si.FindActiveByName("global-one"); e == nil || e.Scope != "global" {
		t.Errorf("global fallback failed: %+v", e)
	}

	writeSkill(proj, "proj-one")
	e := si.FindActiveByName("proj-one")
	if e == nil || e.Scope != "project" || e.Name != "proj-one" {
		t.Errorf("project lookup failed: %+v", e)
	}
}

// --- analyzer.go ---

func TestSa154DetectFailureFixes(t *testing.T) {
	sa := &SessionAnalyzer{}
	ses := &session.Session{ID: "s1", Messages: []provider.Message{
		{Role: "assistant", Content: []provider.ContentBlock{
			{Type: "tool_use", ToolID: "t1", ToolName: "Bash", Input: json.RawMessage(`{"cmd":"ls x"}`)},
		}},
		{Role: "user", Content: []provider.ContentBlock{
			{Type: "tool_result", ToolID: "t1", IsError: true, Output: "boom: no such file"},
		}},
		{Role: "assistant", Content: []provider.ContentBlock{
			{Type: "text", Text: "fixed by quoting the path"},
			{Type: "tool_use", ToolID: "t2", ToolName: "Bash", Input: json.RawMessage(`{"cmd":"ls 'x'"}`)},
		}},
		{Role: "user", Content: []provider.ContentBlock{
			{Type: "tool_result", ToolID: "t2", IsError: false, Output: "ok"},
		}},
	}}

	candidates := sa.detectFailureFixes(ses)
	if len(candidates) != 1 {
		t.Fatalf("expected 1 failure-fix candidate, got %d", len(candidates))
	}
	c := candidates[0]
	if c.Category != "failure-fix" || c.Score != 1.5 || c.Name == "" {
		t.Errorf("unexpected candidate: %+v", c)
	}
	if len(c.Evidence) != 2 || !strings.Contains(c.Evidence[1], "fixed by quoting the path") {
		t.Errorf("evidence missing fix description: %v", c.Evidence)
	}
}

func TestSa154DetectFailureFixesSkips(t *testing.T) {
	sa := &SessionAnalyzer{}

	// Unknown tool IDs are skipped; no assistant in between means no fix.
	ses := &session.Session{ID: "s2", Messages: []provider.Message{
		{Role: "user", Content: []provider.ContentBlock{
			{Type: "tool_result", ToolID: "ghost", IsError: true, Output: "err"},
			{Type: "tool_result", ToolID: "t1", IsError: true, Output: "err"},
		}},
		{Role: "user", Content: []provider.ContentBlock{
			{Type: "tool_result", ToolID: "t1", IsError: false, Output: "ok"},
		}},
	}}
	if got := sa.detectFailureFixes(ses); len(got) != 0 {
		t.Errorf("expected no candidates (no assistant between), got %d", len(got))
	}

	// Same tool failing twice only yields one candidate.
	ses2 := &session.Session{ID: "s3", Messages: []provider.Message{
		{Role: "assistant", Content: []provider.ContentBlock{
			{Type: "tool_use", ToolID: "a", ToolName: "Edit", Input: json.RawMessage(`{"p":1}`)},
			{Type: "tool_use", ToolID: "b", ToolName: "Edit", Input: json.RawMessage(`{"p":2}`)},
		}},
		{Role: "user", Content: []provider.ContentBlock{
			{Type: "tool_result", ToolID: "a", IsError: true, Output: "e1"},
			{Type: "tool_result", ToolID: "b", IsError: true, Output: "e2"},
		}},
		{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "retry smarter"}}},
		{Role: "user", Content: []provider.ContentBlock{
			{Type: "tool_result", ToolID: "a", IsError: false, Output: "ok"},
			{Type: "tool_result", ToolID: "b", IsError: false, Output: "ok"},
		}},
	}}
	if got := sa.detectFailureFixes(ses2); len(got) != 1 {
		t.Errorf("expected dedupe to 1 candidate, got %d", len(got))
	}
}

func TestSa154IsValidCandidateName(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"short", false},              // < 8 chars
		{"correction-123", false},     // pure fallback pattern
		{"correction-abc", true},      // correction with semantic tail
		{"------------", false},       // all dashes, no alnum
		{"good-candidate-name", true}, // healthy name
		{"a-b-c-d-e-f", true},         // exactly half alnum
	}
	for _, tc := range cases {
		if got := isValidCandidateName(tc.in); got != tc.want {
			t.Errorf("isValidCandidateName(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestSa154AnalyzerRecordUsageNoop(t *testing.T) {
	(&SessionAnalyzer{}).RecordUsage(provider.TokenUsage{})
}

// --- skill_validator.go ---

func TestSa154CheckDependencies(t *testing.T) {
	r := &ValidationResult{}
	entry := &SkillEntry{Meta: SkillMeta{Requires: []string{"sa154-no-such-binary-xyz", "go"}}}
	r.checkDependencies(entry)
	if len(r.Warnings) != 1 {
		t.Fatalf("expected exactly 1 missing-dependency warning, got %v", r.Warnings)
	}
	if !strings.Contains(r.Warnings[0], "dependency not found on PATH: sa154-no-such-binary-xyz") {
		t.Errorf("unexpected warning: %q", r.Warnings[0])
	}
}

// --- reject_feedback.go ---

func TestSa154RejectFeedbackLoad(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	stale := rejectFeedbackEntry{Time: now.Add(-rejectCoolDownWindow - time.Hour), Name: "old-skill", Action: "reject"}
	fresh := rejectFeedbackEntry{Time: now.Add(-time.Hour), Name: "fresh-skill", Action: "rollback", Reporter: "user"}

	writeLines := func(path string, lines ...any) {
		t.Helper()
		f, err := os.Create(path)
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		defer f.Close()
		enc := json.NewEncoder(f)
		for _, line := range lines {
			switch v := line.(type) {
			case string:
				if _, err := f.WriteString(v + "\n"); err != nil {
					t.Fatalf("WriteString: %v", err)
				}
			case rejectFeedbackEntry:
				if err := enc.Encode(v); err != nil {
					t.Fatalf("Encode: %v", err)
				}
			}
		}
	}

	// Corrupt line: decode stops there, so trailing entries are not loaded and
	// the end-of-load trim is skipped (early return). Assert current behavior.
	corruptPath := filepath.Join(dir, "corrupt.jsonl")
	writeLines(corruptPath, stale, "this is not json", fresh)
	s := newRejectFeedbackStore(corruptPath)
	s.load()
	if len(s.entries) != 1 || s.entries[0].Name != "old-skill" {
		t.Fatalf("corrupt-file load = %+v, want decode to stop at [old-skill]", s.entries)
	}
	before := len(s.entries)
	s.load() // idempotent
	if len(s.entries) != before {
		t.Errorf("second load re-read entries: %d -> %d", before, len(s.entries))
	}

	// Clean file: load trims entries beyond the cool-down window.
	cleanPath := filepath.Join(dir, "clean.jsonl")
	writeLines(cleanPath, stale, fresh)
	c := newRejectFeedbackStore(cleanPath)
	c.load()
	if len(c.entries) != 1 || c.entries[0].Name != "fresh-skill" {
		t.Fatalf("clean-file load = %+v, want stale entry trimmed, only [fresh-skill]", c.entries)
	}

	// Missing file: no panic, no entries.
	empty := newRejectFeedbackStore(filepath.Join(dir, "missing.jsonl"))
	empty.load()
	if len(empty.entries) != 0 {
		t.Errorf("missing file should load zero entries, got %d", len(empty.entries))
	}
}
