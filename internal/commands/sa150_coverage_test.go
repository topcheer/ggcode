package commands

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// --- isolation helpers -------------------------------------------------

// withIsolatedHome redirects HOME at a fresh temp dir so config.HomeDir()
// based state files (disabled_skills.json, skill_usage.json) stay off the
// developer's real home. Restored automatically by t.Setenv.
func withIsolatedHome(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	return tmp
}

// resetDisabledCache snapshots and clears the package-level disabled-set
// cache, restoring it when the test finishes (same pattern as
// zz_raceprobe_test.go).
func resetDisabledCache(t *testing.T) {
	t.Helper()
	disabledMu.Lock()
	savedCache, savedOK := disabledCache, disabledCacheOK
	disabledCache, disabledCacheOK = nil, false
	disabledMu.Unlock()
	t.Cleanup(func() {
		disabledMu.Lock()
		disabledCache, disabledCacheOK = savedCache, savedOK
		disabledMu.Unlock()
	})
}

func resetUsageWriteLog(t *testing.T) {
	t.Helper()
	skillUsageMu.Lock()
	saved := lastWriteByName
	lastWriteByName = map[string]time.Time{}
	skillUsageMu.Unlock()
	t.Cleanup(func() {
		skillUsageMu.Lock()
		lastWriteByName = saved
		skillUsageMu.Unlock()
	})
}

// --- version.go: SemVer §11 pre-release ordering -----------------------

func TestSA150_ComparePreReleaseIdentOrdering(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"1.0.0-2", "1.0.0-1", 1},            // both identifiers pure numeric: an > bn
		{"1.0.0-1", "1.0.0-2", -1},           // numeric an < bn
		{"1.0.0-beta", "1.0.0-alpha", 1},     // both alphanumeric: lexical >
		{"1.0.0-alpha", "1.0.0-1", 1},        // alphanumeric > numeric
		{"1.0.0-alpha", "1.0.0-alpha.1", -1}, // left missing identifier: shorter ranks lower
		// Trailing-dot identifiers produce an empty extra identifier that
		// equals the missing one, so the compare falls to the length tail.
		{"1.0.0-alpha.", "1.0.0-alpha", 1},
		{"1.0.0-alpha", "1.0.0-alpha.", -1},
	}
	for _, tt := range tests {
		if got := CompareVersions(tt.a, tt.b); got != tt.want {
			t.Errorf("CompareVersions(%q, %q) = %d, want %d", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestSA150_SameCoreTuple(t *testing.T) {
	if !sameCoreTuple([]int{1, 2}, []int{1, 2, 0}) {
		t.Error("missing segments must be treated as 0")
	}
	if sameCoreTuple([]int{1, 2, 0}, []int{1, 3, 0}) {
		t.Error("differing tuples must not match")
	}
	if !sameCoreTuple(nil, []int{0, 0}) {
		t.Error("nil vs zero tuple must match")
	}
}

// npm pre-release exclusion rule for range comparators: a pre-release only
// satisfies a range when the boundary carries a pre-release on the SAME
// [major,minor,patch] tuple.
func TestSA150_PrereleaseRangeExclusion(t *testing.T) {
	tests := []struct {
		actual, op, required string
		want                 bool
	}{
		{"1.2.0-rc1", ">=", "1.2.0-beta", true},  // same tuple + pre-release boundary
		{"1.3.0-rc1", ">=", "1.2.0-beta", false}, // different tuple
		{"2.0.0-rc1", "^", "1.2.0-beta", false},  // caret, tuple mismatch
		{"1.2.0", ">=", "1.0.0-rc1", true},       // plain release: exclusion rule N/A
		{"1.2.0-rc1", ">=", "1.2.0", false},      // boundary without pre-release
		// Operator embedded in requiredVersion is tolerated (^ and ~ both);
		// the actual pre-release must sit on the SAME tuple as the boundary.
		{"1.2.0-rc2", "~", "~1.2.0-beta", true},
		{"1.2.0-rc1", "^", "^1.2.0-beta", true},
	}
	for _, tt := range tests {
		if got := CheckVersionConstraint(tt.actual, tt.op, tt.required); got != tt.want {
			t.Errorf("CheckVersionConstraint(%q, %q, %q) = %v, want %v",
				tt.actual, tt.op, tt.required, got, tt.want)
		}
	}
}

// --- command.go: nil receivers and title fallbacks ----------------------

func TestSA150_CommandNilReceiverHelpers(t *testing.T) {
	var nilCmd *Command
	if got := nilCmd.SlashName(); got != "" {
		t.Errorf("nil SlashName = %q, want empty", got)
	}
	if nilCmd.IsBuiltin() {
		t.Error("nil IsBuiltin = true, want false")
	}
	if got := nilCmd.Title(); got != "" {
		t.Errorf("nil Title = %q, want empty", got)
	}
	if nilCmd.UserSlashVisible() {
		t.Error("nil UserSlashVisible = true, want false")
	}

	c := &Command{Name: "deploy", DisplayName: "  Deploy Prod  ", LoadedFrom: LoadedFromSkills}
	if got := c.Title(); got != "Deploy Prod" {
		t.Errorf("Title = %q, want %q", got, "Deploy Prod")
	}
	c.DisplayName = "   "
	if got := c.Title(); got != "deploy" {
		t.Errorf("Title fallback = %q, want %q", got, "deploy")
	}
	if got := c.SlashName(); got != "/deploy" {
		t.Errorf("SlashName = %q, want %q", got, "/deploy")
	}
}

// --- loader.go: List ordering, dir-entry filtering ----------------------

func TestSA150_LoaderListOrdersBySourceThenName(t *testing.T) {
	ggHome := withIsolatedHome(t)
	proj := filepath.Join(ggHome, "proj")

	cmdDir := filepath.Join(ggHome, ".ggcode", "commands")
	if err := os.MkdirAll(cmdDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cmdDir, "zulu.md"), []byte("Z cmd"), 0o644); err != nil {
		t.Fatal(err)
	}
	// ".md" trims to an empty name and must be skipped.
	if err := os.WriteFile(filepath.Join(cmdDir, ".md"), []byte("no name"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A directory masquerading as a .md file must be skipped too.
	if err := os.MkdirAll(filepath.Join(cmdDir, "fake.md"), 0o755); err != nil {
		t.Fatal(err)
	}

	skillDir := filepath.Join(proj, ".ggcode", "skills", "alpha")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("A skill"), 0o644); err != nil {
		t.Fatal(err)
	}

	l := NewLoader(proj)
	list := l.List()
	if len(list) != 2 {
		t.Fatalf("len(list) = %d, want 2: %+v", len(list), list)
	}
	// Sort is by Source string: "project" < "user".
	if list[0].Name != "alpha" || list[0].Source != SourceProject || list[0].LoadedFrom != LoadedFromSkills {
		t.Errorf("list[0] = %s/%s/%s, want alpha/project/skills", list[0].Name, list[0].Source, list[0].LoadedFrom)
	}
	if list[1].Name != "zulu" || list[1].Source != SourceUser || list[1].LoadedFrom != LoadedFromCommands {
		t.Errorf("list[1] = %s/%s/%s, want zulu/user/commands", list[1].Name, list[1].Source, list[1].LoadedFrom)
	}
}

func TestSA150_SkillsDirSkipsNonSkillEntries(t *testing.T) {
	tmp := withIsolatedHome(t)
	skillsDir := filepath.Join(tmp, ".ggcode", "skills")
	if err := os.MkdirAll(filepath.Join(skillsDir, "hollow"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Directory without SKILL.md is skipped (ReadFile on the skill path fails).
	// A plain file entry (not a dir) is skipped too.
	if err := os.WriteFile(filepath.Join(skillsDir, "stray.md"), []byte("stray"), 0o644); err != nil {
		t.Fatal(err)
	}
	valid := filepath.Join(skillsDir, "good")
	if err := os.MkdirAll(valid, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(valid, "SKILL.md"), []byte("Good"), 0o644); err != nil {
		t.Fatal(err)
	}

	cmds := NewLoader(tmp).Load()
	if _, ok := cmds["hollow"]; ok {
		t.Error("skill dir without SKILL.md must not load")
	}
	if _, ok := cmds["stray"]; ok {
		t.Error("plain file under skills/ must not load")
	}
	if cmd, ok := cmds["good"]; !ok || cmd.Template != "Good" {
		t.Fatalf("good skill missing or wrong: %+v", cmds["good"])
	}
}

func TestSA150_FirstNonEmptyMarkdownLine(t *testing.T) {
	cases := []struct {
		values []string
		want   string
	}{
		{[]string{"\n  \nReal desc\nmore"}, "Real desc"},
		{[]string{"", "   ", "Fallback"}, "Fallback"},
		{[]string{"   ", "\t"}, ""},
		{[]string{"  Padded  "}, "Padded"},
	}
	for _, tc := range cases {
		if got := firstNonEmptyMarkdownLine(tc.values...); got != tc.want {
			t.Errorf("firstNonEmptyMarkdownLine(%q) = %q, want %q", tc.values, got, tc.want)
		}
	}
}

func TestSA150_CanonicalTargetDirSymlinkDedupe(t *testing.T) {
	tmp := withIsolatedHome(t)
	real := filepath.Join(tmp, "realproj")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(tmp, "linkproj")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	targets := []loadTarget{
		{Dir: real, Source: SourceUser, LoadedFrom: LoadedFromSkills},
		{Dir: link, Source: SourceProject, LoadedFrom: LoadedFromSkills},
	}
	if got := dedupeLoadTargets(targets); len(got) != 1 {
		t.Errorf("symlinked duplicate targets not deduped: got %d, want 1", len(got))
	}
	// Non-existent path falls back to Abs+Clean instead of failing.
	if got := canonicalTargetDir(filepath.Join(tmp, "missing", "..", "alsonot")); got == "" {
		t.Error("canonicalTargetDir returned empty for non-existent path")
	}
}

// --- disabled_state.go: persistence + error paths ------------------------

func TestSA150_DisabledStateCorruptAndNormalize(t *testing.T) {
	tmp := withIsolatedHome(t)
	resetDisabledCache(t)
	ggDir := filepath.Join(tmp, ".ggcode")
	if err := os.MkdirAll(ggDir, 0o755); err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(ggDir, "disabled_skills.json")

	if err := os.WriteFile(statePath, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if set := loadDisabledSet(); len(set) != 0 {
		t.Errorf("corrupt JSON should yield empty set, got %v", set)
	}

	if err := os.WriteFile(statePath, []byte(`["/deploy", "  spaced  "]`), 0o644); err != nil {
		t.Fatal(err)
	}
	InvalidateDisabledCache()
	set := loadDisabledSet()
	if !set["deploy"] || !set["spaced"] {
		t.Errorf("names not normalized: %v", set)
	}
}

func TestSA150_SaveDisabledSetErrorPaths(t *testing.T) {
	tmp := withIsolatedHome(t)
	gg := filepath.Join(tmp, ".ggcode")
	// ~/.ggcode as a regular file: MkdirAll must fail.
	if err := os.WriteFile(gg, []byte("not a dir"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := saveDisabledSet(map[string]bool{"x": true}); err == nil {
		t.Error("expected MkdirAll error when ~/.ggcode is a file")
	}
	// State path as a directory: WriteFile must fail.
	os.Remove(gg)
	if err := os.MkdirAll(filepath.Join(gg, "disabled_skills.json"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := saveDisabledSet(map[string]bool{"x": true}); err == nil {
		t.Error("expected WriteFile error when state path is a directory")
	}
}

func TestSA150_ApplyDisabledStateBranches(t *testing.T) {
	withIsolatedHome(t)
	resetDisabledCache(t)
	PersistEnabledState("gone", false) // seed persisted disabled state

	cmds := map[string]*Command{
		"gone":    {Name: "gone", Enabled: true},
		"builtin": {Name: "builtin", Source: SourceBundled, LoadedFrom: LoadedFromBundled, Enabled: false},
		"nilval":  nil,
	}
	ApplyDisabledState(cmds)
	if cmds["gone"].Enabled {
		t.Error("persisted-disabled skill must load as disabled")
	}
	if !cmds["builtin"].Enabled {
		t.Error("builtin skills cannot be disabled")
	}
}

func TestSA150_SetEnabledPersistsAndReapplies(t *testing.T) {
	tmp := withIsolatedHome(t)
	resetDisabledCache(t)
	skillDir := filepath.Join(tmp, ".ggcode", "skills", "deploy")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("Deploy"), 0o644); err != nil {
		t.Fatal(err)
	}

	m := NewManager(tmp)
	if cmd, ok := m.Commands()["deploy"]; !ok || !cmd.Enabled {
		t.Fatalf("fresh skill should be enabled, got %+v", m.Commands()["deploy"])
	}

	m.SetEnabled("deploy", false)
	if cmd, ok := m.Commands()["deploy"]; !ok || cmd.Enabled {
		t.Error("skill should be disabled right after SetEnabled(false)")
	}
	data, err := os.ReadFile(filepath.Join(tmp, ".ggcode", "disabled_skills.json"))
	if err != nil || !strings.Contains(string(data), "deploy") {
		t.Fatalf("disabled state not persisted: %v %q", err, data)
	}

	// A fresh manager (cold cache) must still see the persisted disable.
	resetDisabledCache(t)
	m2 := NewManager(tmp)
	if cmd, ok := m2.Commands()["deploy"]; !ok || cmd.Enabled {
		t.Error("persisted disable must survive manager reload")
	}
	m2.SetEnabled("deploy", true)
	if cmd, ok := m2.Commands()["deploy"]; !ok || !cmd.Enabled {
		t.Error("skill should be re-enabled after SetEnabled(true)")
	}
}

// --- manager.go: nil guards, filtering, extra providers ------------------

func TestSA150_ManagerNilReceiver(t *testing.T) {
	var m *Manager
	if m.Reload() {
		t.Error("nil manager Reload must return false")
	}
	if got := m.WatchedDirs(); got != nil {
		t.Errorf("nil manager WatchedDirs = %v, want nil", got)
	}
	if got := (&Manager{}).WatchedDirs(); got != nil {
		t.Errorf("loader-less WatchedDirs = %v, want nil", got)
	}
	var nilSet *Manager
	nilSet.SetEnabled("anything", true) // must not panic
}

func TestSA150_ManagerWatchedDirs(t *testing.T) {
	tmp := withIsolatedHome(t)
	dirs := NewManager(tmp).WatchedDirs()
	found := false
	for _, d := range dirs {
		if strings.HasSuffix(d, filepath.Join(".ggcode", "skills")) {
			found = true
		}
	}
	if !found {
		t.Errorf("project skills dir missing from WatchedDirs: %v", dirs)
	}
}

func TestSA150_SkillNamesFilters(t *testing.T) {
	withIsolatedHome(t)
	resetDisabledCache(t)
	m := &Manager{commands: map[string]*Command{
		"active": {Name: "active", Enabled: true},
		"off":    {Name: "off", Enabled: false},
		"hidden": {Name: "hidden", Enabled: true, DisableModelInvocation: true},
		"nilval": nil,
	}}
	names := m.SkillNames()
	// combinedCommands always merges bundled skills, so assert the filter
	// semantics over the union rather than an exact list.
	got := make(map[string]bool, len(names))
	for _, n := range names {
		got[n] = true
	}
	if !got["active"] {
		t.Errorf("enabled skill missing from SkillNames: %v", names)
	}
	for _, excluded := range []string{"off", "hidden", "nilval"} {
		if got[excluded] {
			t.Errorf("%s must be filtered out of SkillNames: %v", excluded, names)
		}
	}
}

func TestSA150_CombinedCommandsProviderFiltering(t *testing.T) {
	tmp := withIsolatedHome(t)
	resetDisabledCache(t)
	m := NewManager(tmp)
	m.SetExtraProviders(
		func() []*Command {
			return []*Command{nil, {Name: "  "}, {Name: "prov-skill", Enabled: true}}
		},
		nil, // nil provider must be skipped without panicking
	)
	cmds := m.Commands()
	if _, ok := cmds["prov-skill"]; !ok {
		t.Errorf("valid provider command missing: %v", cmds)
	}
	if _, ok := cmds[""]; ok {
		t.Error("blank-name command must be filtered")
	}
	if _, ok := cmds["debug"]; !ok {
		t.Error("bundled skills must be merged into combined commands")
	}
}

// Loader.List must order same-source entries by LoadedFrom (commands before
// skills). A single user command + a single user skill guarantees the one
// comparison the sort makes crosses the LoadedFrom tiebreak.
func TestSA150_LoaderListLoadedFromTiebreak(t *testing.T) {
	ggHome := withIsolatedHome(t)
	cmdDir := filepath.Join(ggHome, ".ggcode", "commands")
	if err := os.MkdirAll(cmdDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cmdDir, "solo.md"), []byte("Cmd"), 0o644); err != nil {
		t.Fatal(err)
	}
	skillDir := filepath.Join(ggHome, ".ggcode", "skills", "other")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("Skill"), 0o644); err != nil {
		t.Fatal(err)
	}
	list := NewLoader(ggHome).List()
	if len(list) != 2 {
		t.Fatalf("len(list) = %d, want 2", len(list))
	}
	if list[0].Name != "solo" || list[1].Name != "other" {
		t.Errorf("order = [%s %s], want [solo other] (commands < skills)", list[0].Name, list[1].Name)
	}
}

// Same source and LoadedFrom: List falls through to the name tiebreak.
func TestSA150_LoaderListNameTiebreak(t *testing.T) {
	ggHome := withIsolatedHome(t)
	cmdDir := filepath.Join(ggHome, ".ggcode", "commands")
	if err := os.MkdirAll(cmdDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"aaa", "zzz"} {
		if err := os.WriteFile(filepath.Join(cmdDir, name+".md"), []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	list := NewLoader(ggHome).List()
	if len(list) != 2 || list[0].Name != "aaa" || list[1].Name != "zzz" {
		t.Errorf("name tiebreak failed: got [%v %v]", list[0].Name, list[1].Name)
	}
}

// Manager.List with equal usage scores falls through to Source/LoadedFrom
// tiebreaks; two high-usage user commands with different LoadedFrom force
// the LoadedFrom comparison when their score tie is resolved.
func TestSA150_ManagerListLoadedFromTiebreak(t *testing.T) {
	withIsolatedHome(t)
	resetUsageWriteLog(t)
	now := time.Now().UnixMilli()
	skillUsageMu.Lock()
	err := saveUsageLocked(map[string]skillUsageEntry{
		"a": {UsageCount: 100, LastUsedAt: now},
		"b": {UsageCount: 100, LastUsedAt: now},
	})
	skillUsageMu.Unlock()
	if err != nil {
		t.Fatalf("saveUsageLocked: %v", err)
	}
	resetDisabledCache(t)
	m := &Manager{commands: map[string]*Command{
		"a": {Name: "a", Source: SourceUser, LoadedFrom: LoadedFromCommands, Enabled: true},
		"b": {Name: "b", Source: SourceUser, LoadedFrom: LoadedFromSkills, Enabled: true},
	}}
	list := m.List()
	aIdx, bIdx := -1, -1
	for i, cmd := range list {
		switch cmd.Name {
		case "a":
			aIdx = i
		case "b":
			bIdx = i
		}
	}
	if aIdx < 0 || bIdx < 0 {
		t.Fatalf("missing commands in list: %v", list)
	}
	if aIdx > bIdx {
		t.Errorf("user commands (LoadedFrom=commands) must rank before skills: a@%d b@%d", aIdx, bIdx)
	}
}

// --- usage.go: debounce, guards, error paths -----------------------------

func TestSA150_RecordUsageDebounceAndEmptyName(t *testing.T) {
	tmp := withIsolatedHome(t)
	resetUsageWriteLog(t)
	usagePath := filepath.Join(tmp, ".ggcode", "skill_usage.json")

	if err := RecordUsage("deb"); err != nil {
		t.Fatalf("first RecordUsage: %v", err)
	}
	if err := RecordUsage("deb"); err != nil {
		t.Fatalf("debounced RecordUsage should be a no-op, got %v", err)
	}
	data, err := os.ReadFile(usagePath)
	if err != nil || !strings.Contains(string(data), `"usage_count": 1`) {
		t.Fatalf("debounce not applied: %v %q", err, data)
	}

	// Expire the debounce window: the next call must land.
	skillUsageMu.Lock()
	lastWriteByName["deb"] = time.Now().Add(-2 * time.Minute)
	skillUsageMu.Unlock()
	if err := RecordUsage("deb"); err != nil {
		t.Fatalf("post-debounce RecordUsage: %v", err)
	}
	data, err = os.ReadFile(usagePath)
	if err != nil || !strings.Contains(string(data), `"usage_count": 2`) {
		t.Fatalf("second write missing: %v %q", err, data)
	}

	// Empty after normalization: no error, no write.
	if err := RecordUsage("   "); err != nil {
		t.Errorf("blank RecordUsage should be nil, got %v", err)
	}
	if err := RecordUsage("/"); err != nil {
		t.Errorf("slash-only RecordUsage should be nil, got %v", err)
	}
}

func TestSA150_UsageScoreGuards(t *testing.T) {
	now := time.Now()
	if got := usageScore(skillUsageEntry{}, now); got != 0 {
		t.Errorf("zero entry score = %v, want 0", got)
	}
	if got := usageScore(skillUsageEntry{UsageCount: 3}, now); got != 0 {
		t.Errorf("missing timestamp score = %v, want 0", got)
	}
	if got := usageScore(skillUsageEntry{UsageCount: 3, LastUsedAt: now.UnixMilli() + 60000}, now); got != 3 {
		t.Errorf("future timestamp score = %v, want 3 (recency clamped to 0)", got)
	}
	old := now.Add(-30 * 24 * time.Hour)
	if got := usageScore(skillUsageEntry{UsageCount: 3, LastUsedAt: old.UnixMilli()}, now); math.Abs(got-0.3) > 1e-9 {
		t.Errorf("aged entry score = %v, want ~0.3 (min recency floor)", got)
	}
	if got := usageScoreForCommand(nil, nil, now); got != 0 {
		t.Errorf("nil command score = %v, want 0", got)
	}
	// Empty after normalization short-circuits before any disk access.
	if got := UsageScore(""); got != 0 {
		t.Errorf("UsageScore(empty) = %v, want 0", got)
	}
	if got := UsageScore("/"); got != 0 {
		t.Errorf("UsageScore(slash) = %v, want 0", got)
	}
}

func TestSA150_UsageLoadSaveErrorPaths(t *testing.T) {
	tmp := withIsolatedHome(t)
	resetUsageWriteLog(t)
	ggDir := filepath.Join(tmp, ".ggcode")
	if err := os.MkdirAll(ggDir, 0o755); err != nil {
		t.Fatal(err)
	}
	usageFile := filepath.Join(ggDir, "skill_usage.json")

	// Corrupt JSON: treated as empty usage, not an error.
	if err := os.WriteFile(usageFile, []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	usage, err := loadUsageLocked()
	if err != nil || len(usage) != 0 {
		t.Errorf("corrupt usage JSON: got (%v, %v), want (empty, nil)", usage, err)
	}

	// JSON null: must come back as a non-nil empty map.
	if err := os.WriteFile(usageFile, []byte("null"), 0o644); err != nil {
		t.Fatal(err)
	}
	usage, err = loadUsageLocked()
	if err != nil || usage == nil {
		t.Errorf("null usage JSON: got (%v, %v), want (non-nil empty, nil)", usage, err)
	}

	// Unreadable (directory instead of file): error propagates.
	os.Remove(usageFile)
	if err := os.MkdirAll(usageFile, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := loadUsageLocked(); err == nil {
		t.Error("expected ReadFile error when usage path is a directory")
	}
	if err := RecordUsage("blocked"); err == nil {
		t.Error("RecordUsage should surface load errors")
	}
	if got := UsageScore("blocked"); got != 0 {
		t.Errorf("UsageScore on unreadable file = %v, want 0", got)
	}
	if got := loadUsageSnapshot(); len(got) != 0 {
		t.Errorf("loadUsageSnapshot on unreadable file = %v, want empty", got)
	}

	// Save path: ~/.ggcode as a regular file breaks MkdirAll.
	os.RemoveAll(ggDir)
	if err := os.WriteFile(ggDir, []byte("file"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := saveUsageLocked(map[string]skillUsageEntry{}); err == nil {
		t.Error("expected MkdirAll error when ~/.ggcode is a file")
	}
}
