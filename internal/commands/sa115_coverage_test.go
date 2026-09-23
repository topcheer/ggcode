package commands

// sa-115 coverage round: tests-only additions for internal/commands.
//
// Theoretical anchor: instruction-priority/registry regression safety and
// risk-based regression prioritization (2025-2026 QA literature) — the
// loader precedence chain, version-constraint comparison, usage scoring,
// and the disabled-skill cache are the registry's highest-risk resolution
// paths, so they get real-filesystem regression tests here.
//
// All tests exercise real behavior: TempDir homes, real files on disk,
// real lock-protected globals (saved/restored via t.Cleanup). No mocking
// of package-internal functions. t.Setenv forbids t.Parallel.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// sa115CleanDisabledCache snapshots and restores the package-global
// disabled-skill cache, then invalidates it so each test starts from disk.
func sa115CleanDisabledCache(t *testing.T) {
	t.Helper()
	disabledMu.Lock()
	savedCache, savedOK := disabledCache, disabledCacheOK
	disabledMu.Unlock()
	t.Cleanup(func() {
		disabledMu.Lock()
		disabledCache, disabledCacheOK = savedCache, savedOK
		disabledMu.Unlock()
	})
	InvalidateDisabledCache()
}

// sa115CleanLastWrite removes a skill's debounce entry after the test so
// usage-state globals don't leak across tests.
func sa115CleanLastWrite(t *testing.T, name string) {
	t.Helper()
	normalized := normalizeSkillName(name)
	t.Cleanup(func() {
		skillUsageMu.Lock()
		delete(lastWriteByName, normalized)
		skillUsageMu.Unlock()
	})
}

func sa115MustWriteFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll(%s): %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile(%s): %v", path, err)
	}
}

// --- command.go: nil receivers and fallbacks -------------------------------

func TestSA115CommandNilReceivers(t *testing.T) {
	var nilCmd *Command
	if got := nilCmd.SlashName(); got != "" {
		t.Fatalf("nil SlashName = %q, want empty", got)
	}
	if nilCmd.IsBuiltin() {
		t.Fatal("nil IsBuiltin = true, want false")
	}
	if got := nilCmd.Title(); got != "" {
		t.Fatalf("nil Title = %q, want empty", got)
	}

	blank := &Command{Name: "   "}
	if got := blank.SlashName(); got != "" {
		t.Fatalf("blank-name SlashName = %q, want empty", got)
	}
	// Title falls back to the raw (untrimmed) Name when DisplayName is blank.
	if got := blank.Title(); got != "   " {
		t.Fatalf("blank DisplayName Title = %q, want raw Name passthrough", got)
	}

	fallback := &Command{Name: "real", DisplayName: "   "}
	if got := fallback.Title(); got != "real" {
		t.Fatalf("Title with blank DisplayName = %q, want %q", got, "real")
	}
	named := &Command{Name: "real", DisplayName: "Pretty"}
	if got := named.Title(); got != "Pretty" {
		t.Fatalf("Title with DisplayName = %q, want %q", got, "Pretty")
	}
}

// --- loader.go: precedence chain, sort order, skip rules -------------------

func TestSA115LoaderListSortPrecedenceAndSkips(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	project := filepath.Join(tmp, "proj")
	t.Setenv("HOME", home)

	userCmds := filepath.Join(home, ".ggcode", "commands")
	sa115MustWriteFile(t, filepath.Join(userCmds, "ucmd-b.md"), "---\ndescription: B cmd\n---\nBody B")
	sa115MustWriteFile(t, filepath.Join(userCmds, "ucmd-a.md"), "plain body, no frontmatter")
	sa115MustWriteFile(t, filepath.Join(userCmds, "notes.txt"), "not markdown")
	sa115MustWriteFile(t, filepath.Join(userCmds, ".md"), "empty basename")
	sa115MustWriteFile(t, filepath.Join(userCmds, "subdir", "nested.md"), "directories are skipped")

	userSkills := filepath.Join(home, ".ggcode", "skills")
	sa115MustWriteFile(t, filepath.Join(userSkills, "usk1", "SKILL.md"), "---\ndescription: USK one\n---\nSkill one")
	// Empty body + empty description exercises firstNonEmptyMarkdownLine's
	// exhausted-input path (returns "").
	sa115MustWriteFile(t, filepath.Join(userSkills, "usk2", "SKILL.md"), "---\nname: USK2\n---\n\n")
	sa115MustWriteFile(t, filepath.Join(userSkills, "stray.md"), "file in skills dir is not a skill")
	sa115MustWriteFile(t, filepath.Join(userSkills, "noskill", "README.md"), "skill dir without SKILL.md")

	projCmds := filepath.Join(project, ".ggcode", "commands")
	sa115MustWriteFile(t, filepath.Join(projCmds, "pcmd.md"), "---\nuser-invocable: false\ndescription: P cmd\n---\nBody P")

	loader := NewLoader(project)
	list := loader.List()
	if len(list) != 5 {
		names := make([]string, 0, len(list))
		for _, c := range list {
			names = append(names, c.Name)
		}
		t.Fatalf("List() = %v, want exactly 5 commands", names)
	}
	wantOrder := []string{"pcmd", "ucmd-a", "ucmd-b", "usk1", "usk2"}
	for i, want := range wantOrder {
		if list[i].Name != want {
			t.Fatalf("List()[%d] = %q, want %q (full order: %v)", i, list[i].Name, want, wantOrder)
		}
	}

	// Sort-key semantics: project < user (string Source order), then
	// commands < skills within the same source, then name.
	if list[0].Source != SourceProject || list[1].Source != SourceUser {
		t.Fatalf("source ordering wrong: pcmd=%s ucmd-a=%s", list[0].Source, list[1].Source)
	}
	if list[1].LoadedFrom != LoadedFromCommands || list[3].LoadedFrom != LoadedFromSkills {
		t.Fatalf("LoadedFrom ordering wrong: ucmd-a=%s usk1=%s", list[1].LoadedFrom, list[3].LoadedFrom)
	}

	byName := loader.Load()
	if cmd := byName["pcmd"]; cmd.UserInvocable {
		t.Fatal("pcmd frontmatter user-invocable: false was ignored")
	}
	if cmd := byName["usk2"]; cmd.Description != "" || cmd.DisplayName != "USK2" {
		t.Fatalf("usk2 Description=%q DisplayName=%q, want empty/USK2", cmd.Description, cmd.DisplayName)
	}
	if cmd := byName["ucmd-a"]; cmd.Description != "plain body, no frontmatter" {
		t.Fatalf("ucmd-a Description=%q, want first markdown line of body", cmd.Description)
	}
	for _, absent := range []string{"notes.txt", ".md", "subdir", "stray.md", "noskill"} {
		if _, ok := byName[absent]; ok {
			t.Fatalf("%q should not be loaded", absent)
		}
	}
}

// --- disabled_state.go: cache lifecycle, corrupt state, precedence ---------

func TestSA115DisabledCacheHitUnderWriteLock(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	sa115CleanDisabledCache(t)

	cached := map[string]bool{"cached-skill": true}
	disabledMu.Lock()
	disabledCache, disabledCacheOK = cached, true
	disabledMu.Unlock()

	if got := loadDisabledSet(); !got["cached-skill"] || len(got) != 1 {
		t.Fatalf("loadDisabledSet = %v, want cached {cached-skill}", got)
	}
	// Second-level check: the locked fast path returns the same map.
	disabledMu.Lock()
	locked := loadDisabledSetLocked()
	disabledMu.Unlock()
	if !locked["cached-skill"] {
		t.Fatalf("loadDisabledSetLocked = %v, want cache hit", locked)
	}
}

func TestSA115DisabledMissingFileDefaultsEnabled(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	sa115CleanDisabledCache(t)

	if got := loadDisabledSet(); len(got) != 0 {
		t.Fatalf("loadDisabledSet with no state file = %v, want empty", got)
	}
	cmds := map[string]*Command{
		"fresh": {Name: "fresh", Enabled: true},
	}
	ApplyDisabledState(cmds)
	if !cmds["fresh"].Enabled {
		t.Fatal("new skill with no persisted state must default to enabled")
	}
}

func TestSA115DisabledCorruptJSONTreatedAsEmpty(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	sa115CleanDisabledCache(t)

	sa115MustWriteFile(t, filepath.Join(tmp, ".ggcode", "disabled_skills.json"), "{not valid json")
	cmds := map[string]*Command{"skill": {Name: "skill", Enabled: true}}
	ApplyDisabledState(cmds)
	if !cmds["skill"].Enabled {
		t.Fatal("corrupt state file must not disable skills")
	}
	if got := loadDisabledSet(); len(got) != 0 {
		t.Fatalf("loadDisabledSet after corrupt file = %v, want empty", got)
	}
}

func TestSA115DisabledNormalizationNilAndBuiltinPrecedence(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	sa115CleanDisabledCache(t)

	sa115MustWriteFile(t,
		filepath.Join(tmp, ".ggcode", "disabled_skills.json"),
		`["my-skill", "/other", "bi"]`)

	cmds := map[string]*Command{
		"my-skill": {Name: "my-skill", Enabled: true},
		"other":    {Name: "other", Enabled: true},
		"fresh":    {Name: "fresh", Enabled: true},
		"bi":       {Name: "bi", Source: SourceBundled, Enabled: true},
		"nilcmd":   nil,
	}
	ApplyDisabledState(cmds)
	if cmds["my-skill"].Enabled {
		t.Fatal("my-skill listed in state must be disabled")
	}
	if cmds["other"].Enabled {
		t.Fatal("/other in state must disable normalized name other")
	}
	if !cmds["fresh"].Enabled {
		t.Fatal("skill not listed must stay enabled")
	}
	if !cmds["bi"].Enabled {
		t.Fatal("bundled skill must ignore persisted disabled state")
	}
}

func TestSA115DisabledPersistWithBrokenGgcodeDir(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	sa115CleanDisabledCache(t)

	// .ggcode exists as a regular file: ReadFile misses (ENOTDIR) and
	// MkdirAll inside saveDisabledSet fails. Persist must not panic.
	if err := os.WriteFile(filepath.Join(tmp, ".ggcode"), []byte("plain file"), 0o644); err != nil {
		t.Fatalf("write .ggcode file: %v", err)
	}
	PersistEnabledState("sa115-broken", false)
	// Documented behavior: the in-memory cache records the disable even when
	// the disk write fails (saveDisabledSet error is ignored), so the runtime
	// state stays consistent within the process.
	if got := loadDisabledSet(); !got["sa115-broken"] {
		t.Fatalf("loadDisabledSet = %v, want in-memory disable kept despite failed persist", got)
	}
}

// --- usage.go: persistence, debounce, scoring boundaries --------------------

func TestSA115UsageSaveBrokenDirErrors(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	if err := os.WriteFile(filepath.Join(tmp, ".ggcode"), []byte("plain file"), 0o644); err != nil {
		t.Fatalf("write .ggcode file: %v", err)
	}

	skillUsageMu.Lock()
	err := saveUsageLocked(map[string]skillUsageEntry{"x": {UsageCount: 1}})
	skillUsageMu.Unlock()
	if err == nil {
		t.Fatal("saveUsageLocked with .ggcode as file must fail (MkdirAll)")
	}
	if err := RecordUsage("sa115-broken-usage"); err == nil {
		t.Fatal("RecordUsage with broken .ggcode must surface the load error")
	}
	if got := loadUsageSnapshot(); len(got) != 0 {
		t.Fatalf("loadUsageSnapshot with broken dir = %v, want empty", got)
	}
	if got := UsageScore("sa115-broken-usage"); got != 0 {
		t.Fatalf("UsageScore with broken dir = %v, want 0", got)
	}
}

func TestSA115UsageRecordDebounce(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	sa115CleanLastWrite(t, "sa115-deb")

	if err := RecordUsage("sa115-deb"); err != nil {
		t.Fatalf("first RecordUsage: %v", err)
	}
	if err := RecordUsage("/sa115-deb "); err != nil {
		t.Fatalf("second RecordUsage: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(tmp, ".ggcode", "skill_usage.json"))
	if err != nil {
		t.Fatalf("read usage file: %v", err)
	}
	var usage map[string]skillUsageEntry
	if err := json.Unmarshal(data, &usage); err != nil {
		t.Fatalf("unmarshal usage: %v", err)
	}
	entry, ok := usage["sa115-deb"]
	if !ok {
		t.Fatalf("usage file missing sa115-deb: %s", data)
	}
	if entry.UsageCount != 1 {
		t.Fatalf("UsageCount = %d, want 1 (debounce must suppress the immediate second record)", entry.UsageCount)
	}
}

func TestSA115UsageScoreBoundaries(t *testing.T) {
	now := time.Now()
	if got := usageScore(skillUsageEntry{}, now); got != 0 {
		t.Fatalf("zero entry score = %v, want 0", got)
	}
	if got := usageScore(skillUsageEntry{UsageCount: 2, LastUsedAt: 0}, now); got != 0 {
		t.Fatalf("zero-timestamp score = %v, want 0", got)
	}
	if got := usageScore(skillUsageEntry{UsageCount: 2, LastUsedAt: now.Add(time.Hour).UnixMilli()}, now); got != 2 {
		t.Fatalf("future timestamp score = %v, want 2 (recency clamped, full weight)", got)
	}
	ancient := usageScore(skillUsageEntry{UsageCount: 3, LastUsedAt: now.Add(-365 * 24 * time.Hour).UnixMilli()}, now)
	if min, max := 0.3*0.999, 0.3*1.001; ancient < min || ancient > max {
		t.Fatalf("ancient score = %v, want ~0.3 (3 uses x 0.1 recency floor)", ancient)
	}
	if got := UsageScore(""); got != 0 {
		t.Fatalf("UsageScore(\"\") = %v, want 0", got)
	}
	if got := UsageScore("   "); got != 0 {
		t.Fatalf("UsageScore(blank) = %v, want 0", got)
	}
}

func TestSA115UsageLoadCorruptNullAndMissing(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	skillUsageMu.Lock()
	got, err := loadUsageLocked()
	skillUsageMu.Unlock()
	if err != nil || len(got) != 0 {
		t.Fatalf("missing file: got=%v err=%v, want empty map, nil", got, err)
	}

	usageFile := filepath.Join(tmp, ".ggcode", "skill_usage.json")
	sa115MustWriteFile(t, usageFile, "not json at all")
	skillUsageMu.Lock()
	got, err = loadUsageLocked()
	skillUsageMu.Unlock()
	if err != nil || len(got) != 0 {
		t.Fatalf("corrupt json: got=%v err=%v, want empty map, nil", got, err)
	}

	sa115MustWriteFile(t, usageFile, "null")
	skillUsageMu.Lock()
	got, err = loadUsageLocked()
	skillUsageMu.Unlock()
	if err != nil || got == nil || len(got) != 0 {
		t.Fatalf("null json: got=%v err=%v, want non-nil empty map, nil", got, err)
	}
}

func TestSA115UsageSaveLoadRoundTrip(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	want := map[string]skillUsageEntry{
		"alpha": {UsageCount: 1, LastUsedAt: 1700000000000},
		"beta":  {UsageCount: 42, LastUsedAt: 1700000001000},
	}
	skillUsageMu.Lock()
	err := saveUsageLocked(want)
	got, loadErr := loadUsageLocked()
	skillUsageMu.Unlock()
	if err != nil {
		t.Fatalf("saveUsageLocked: %v", err)
	}
	if loadErr != nil {
		t.Fatalf("loadUsageLocked: %v", loadErr)
	}
	if len(got) != 2 || got["alpha"] != want["alpha"] || got["beta"] != want["beta"] {
		t.Fatalf("round trip mismatch: got=%v want=%v", got, want)
	}
}

// --- manager.go: nil receivers, watched dirs, persistence, filters ---------

func TestSA115ManagerNilReceivers(t *testing.T) {
	var nilMgr *Manager
	if nilMgr.Reload() {
		t.Fatal("nil Reload = true, want false")
	}
	nilMgr.SetEnabled("anything", true) // must not panic
	if got := nilMgr.WatchedDirs(); got != nil {
		t.Fatalf("nil WatchedDirs = %v, want nil", got)
	}
	loaderless := &Manager{}
	if got := loaderless.WatchedDirs(); got != nil {
		t.Fatalf("loader-less WatchedDirs = %v, want nil", got)
	}
}

func TestSA115ManagerWatchedDirs(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	project := filepath.Join(tmp, "proj")
	t.Setenv("HOME", home)

	mgr := NewManager(project)
	dirs := mgr.WatchedDirs()
	if len(dirs) != 5 {
		t.Fatalf("WatchedDirs = %v, want 5 distinct target dirs", dirs)
	}
	want := map[string]bool{
		filepath.Join(home, ".agents", "skills"):      false,
		filepath.Join(home, ".ggcode", "skills"):      false,
		filepath.Join(home, ".ggcode", "commands"):    false,
		filepath.Join(project, ".ggcode", "skills"):   false,
		filepath.Join(project, ".ggcode", "commands"): false,
	}
	for _, d := range dirs {
		if _, ok := want[d]; !ok {
			t.Fatalf("unexpected watched dir %q (all=%v)", d, dirs)
		}
		want[d] = true
	}
	for d, seen := range want {
		if !seen {
			t.Fatalf("missing watched dir %q (all=%v)", d, dirs)
		}
	}
}

func TestSA115ManagerSetEnabledPersists(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	sa115CleanDisabledCache(t)

	cmd := &Command{Name: "sa115e", Enabled: true, Source: SourceUser, LoadedFrom: LoadedFromSkills}
	mgr := &Manager{
		loader:   NewLoader(tmp),
		commands: map[string]*Command{"sa115e": cmd},
	}

	stateFile := filepath.Join(tmp, ".ggcode", "disabled_skills.json")
	readState := func() []string {
		t.Helper()
		data, err := os.ReadFile(stateFile)
		if err != nil {
			t.Fatalf("read state: %v", err)
		}
		var names []string
		if err := json.Unmarshal(data, &names); err != nil {
			t.Fatalf("unmarshal state: %v", err)
		}
		return names
	}
	contains := func(names []string, want string) bool {
		for _, n := range names {
			if n == want {
				return true
			}
		}
		return false
	}

	mgr.SetEnabled("sa115e", false)
	if cmd.Enabled {
		t.Fatal("SetEnabled(false) must flip the in-memory command")
	}
	if names := readState(); !contains(names, "sa115e") {
		t.Fatalf("state file = %v, want sa115e persisted as disabled", names)
	}

	// Slash-prefixed name normalizes to the same entry and re-enables it.
	mgr.SetEnabled("/sa115e", true)
	if !cmd.Enabled {
		t.Fatal("SetEnabled(true) must flip the in-memory command")
	}
	if names := readState(); contains(names, "sa115e") {
		t.Fatalf("state file = %v, want sa115e re-enabled (removed)", names)
	}

	// Unknown name: no panic, but the state is still persisted.
	mgr.SetEnabled("ghost-skill", false)
	if names := readState(); !contains(names, "ghost-skill") {
		t.Fatalf("state file = %v, want ghost-skill persisted", names)
	}
}

func TestSA115ManagerSkillNamesFilters(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	sa115CleanDisabledCache(t)

	mgr := &Manager{commands: map[string]*Command{
		"live":    {Name: "live", Enabled: true},
		"off":     {Name: "off", Enabled: false},
		"nomodel": {Name: "nomodel", Enabled: true, DisableModelInvocation: true},
		"nilcmd":  nil,
	}}
	got := mgr.SkillNames()
	// combinedCommands merges bundled skills, so they appear alongside "live".
	if !containsStr(got, "live") {
		t.Fatalf("SkillNames = %v, want it to contain live", got)
	}
	if !containsStr(got, "verify") {
		t.Fatalf("SkillNames = %v, want it to contain bundled verify", got)
	}
	for _, excluded := range []string{"off", "nomodel", "nilcmd"} {
		if containsStr(got, excluded) {
			t.Fatalf("SkillNames = %v, must exclude %q", got, excluded)
		}
	}
}

func containsStr(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

func TestSA115ManagerExtraProviders(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	sa115CleanDisabledCache(t)

	mgr := &Manager{
		loader:   NewLoader(tmp),
		commands: map[string]*Command{"local": {Name: "local", Enabled: true}},
	}
	mgr.SetExtraProviders(
		nil,
		func() []*Command {
			return []*Command{nil, {Name: "   "}, {Name: "plug", Enabled: true}}
		},
	)

	all := mgr.Commands()
	for _, want := range []string{"local", "plug", "verify"} { // verify = bundled
		if _, ok := all[want]; !ok {
			t.Fatalf("Commands() missing %q (have %v)", want, keysOf(all))
		}
	}
	if _, ok := all[""]; ok {
		t.Fatal(`Commands() must not contain an empty-name entry`)
	}
}

func TestSA115ManagerListTieBreaks(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	sa115CleanDisabledCache(t)

	mgr := &Manager{commands: map[string]*Command{
		"a1": {Name: "a1", Source: SourceUser, LoadedFrom: LoadedFromCommands, Enabled: true},
		"s1": {Name: "s1", Source: SourceUser, LoadedFrom: LoadedFromSkills, Enabled: true},
		"p1": {Name: "p1", Source: SourceProject, LoadedFrom: LoadedFromCommands, Enabled: true},
	}}
	list := mgr.List()
	index := map[string]int{}
	for i, c := range list {
		index[c.Name] = i
	}
	for _, name := range []string{"a1", "s1", "p1"} {
		if _, ok := index[name]; !ok {
			t.Fatalf("List() missing %q", name)
		}
	}
	if !(index["p1"] < index["a1"] && index["a1"] < index["s1"]) {
		t.Fatalf("tie-break order wrong: p1=%d a1=%d s1=%d (want p1 < a1 < s1)",
			index["p1"], index["a1"], index["s1"])
	}
}

func TestSA115ManagerReloadSignature(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	project := filepath.Join(tmp, "proj")
	t.Setenv("HOME", home)
	sa115CleanDisabledCache(t)

	mgr := NewManager(project)
	projCmds := filepath.Join(project, ".ggcode", "commands")
	sa115MustWriteFile(t, filepath.Join(projCmds, "reload-cmd.md"), "reload body")

	if !mgr.Reload() {
		t.Fatal("Reload after new command file must report change")
	}
	if _, ok := mgr.Commands()["reload-cmd"]; !ok {
		t.Fatal("Reload must pick up the new command")
	}
	if mgr.Reload() {
		t.Fatal("second Reload with unchanged files must report no change")
	}
}

func TestSA115UsageScoreForCommandNil(t *testing.T) {
	if got := usageScoreForCommand(nil, nil, time.Now()); got != 0 {
		t.Fatalf("usageScoreForCommand(nil) = %v, want 0", got)
	}
}

// --- version.go: pre-release identifiers and range constraints -------------

func TestSA115VersionPreReleaseIdentMatrix(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		// numeric identifiers compare numerically, both directions
		{"1.0.0-1", "1.0.0-2", -1},
		{"1.0.0-2", "1.0.0-1", 1},
		// numeric < alphanumeric, both directions
		{"1.0.0-1", "1.0.0-alpha", -1},
		{"1.0.0-alpha", "1.0.0-1", 1},
		// alphanumeric compares lexically, both directions
		{"1.0.0-alpha", "1.0.0-beta", -1},
		{"1.0.0-beta", "1.0.0-alpha", 1},
		// shorter identifier list ranks lower (missing tail guard)
		{"1.0.0-alpha", "1.0.0-alpha.1", -1},
		{"1.0.0-alpha.1", "1.0.0-alpha", 1},
		// nested numeric vs alphanumeric
		{"1.0.0-rc.1", "1.0.0-rc.beta", -1},
		{"1.0.0-alpha.1", "1.0.0-alpha.beta", -1},
		{"1.0.0-rc.1", "1.0.0-rc.2", -1},
	}
	for _, tc := range cases {
		if got := CompareVersions(tc.a, tc.b); got != tc.want {
			t.Errorf("CompareVersions(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestSA115VersionCaretTildeAndPrereleaseRanges(t *testing.T) {
	cases := []struct {
		actual, op, required string
		want                 bool
		note                 string
	}{
		{"1.5.0", "^", "1.2.0", true, "caret within major"},
		{"2.0.0", "^", "1.2.0", false, "caret above major"},
		{"1.1.9", "^", "1.2.0", false, "caret below range"},
		{"0.2.5", "^", "0.2.0", true, "caret zero-major bumps minor"},
		{"0.3.0", "^", "0.2.0", false, "caret zero-major upper bound"},
		{"0.0.3", "^", "0.0.3", true, "caret all-zero bumps patch"},
		{"0.0.4", "^", "0.0.3", false, "caret all-zero upper bound"},
		{"1.2.9", "~", "1.2.0", true, "tilde patch-level"},
		{"1.3.0", "~", "1.2.0", false, "tilde upper bound"},
		{"1.1.9", "~", "1.2.0", false, "tilde below range"},
		{"1.9.0", "~", "1", true, "tilde major-only range"},
		{"2.0.0", "~", "1", false, "tilde major-only upper bound"},
		{"1.2.9", "^", "^1.2.0", true, "operator embedded in required"},
		// npm pre-release exclusion for range comparators
		{"1.2.0-rc1", ">=", "1.2.0-beta", true, "same-tuple pre satisfies"},
		{"2.0.0-rc1", ">=", "1.2.0-beta", false, "different-tuple pre rejected"},
		{"2.0.0-rc1", "^", "1.2.0", false, "pre vs plain boundary rejected"},
		{"1.2.1", ">=", "1.2.0", true, "plain release ignores pre gate"},
		// equality ops are pure precedence compares, no pre-release gate
		{"1.2.0-rc1", "==", "1.2.0-rc1", true, "exact pre equality"},
		{"1.2.0-rc1", "!=", "1.2.0", true, "pre != release"},
		{"1.2.0-rc1", "<", "1.2.0", false, "< also applies npm pre-release exclusion"},
		{"1.2.0-beta", "<", "1.2.0-rc1", true, "pre vs same-tuple pre boundary sorts normally"},
	}
	for _, tc := range cases {
		if got := CheckVersionConstraint(tc.actual, tc.op, tc.required); got != tc.want {
			t.Errorf("CheckVersionConstraint(%q, %q, %q) = %v, want %v (%s)",
				tc.actual, tc.op, tc.required, got, tc.want, tc.note)
		}
	}
}

// keysOf is a small test helper for readable failure messages.
func keysOf(m map[string]*Command) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
