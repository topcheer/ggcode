package tool

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/commands"
)

// fakeRegistryLookup is a minimal SkillLookup for overwrite-detection tests.
type fakeRegistryLookup struct {
	skills map[string]*commands.Command
}

func (f *fakeRegistryLookup) Get(name string) (*commands.Command, bool) {
	cmd, ok := f.skills[name]
	return cmd, ok
}

// writeTestRegistryBundle builds a minimal .ggskill bundle on disk and returns
// its path plus the sha256 hex digest of the file contents.
func writeTestRegistryBundle(t *testing.T, dir, name, version string) (string, string) {
	t.Helper()
	skillDir := createTestSkillDir(t, dir, name)
	bundlePath := filepath.Join(dir, name+".ggskill")
	cmd := &commands.Command{
		Name:    name,
		Path:    filepath.Join(skillDir, "SKILL.md"),
		Version: version,
	}
	if _, _, err := exportSkill(cmd, bundlePath); err != nil {
		t.Fatalf("exportSkill failed: %v", err)
	}
	data, err := os.ReadFile(bundlePath)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	return bundlePath, hex.EncodeToString(sum[:])
}

// writeTestRegistryIndex writes a registry index JSON to disk and sets
// GGCODE_SKILL_REGISTRY for the duration of the test. Entry URLs use local
// bundle paths so no network is involved.
func writeTestRegistryIndex(t *testing.T, skills []RegistryIndexEntry) string {
	t.Helper()
	indexPath := filepath.Join(t.TempDir(), "registry.json")
	data, err := buildRegistryIndexJSON(&RegistryIndex{Name: "test-registry", Skills: skills})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(indexPath, data, 0644); err != nil {
		t.Fatal(err)
	}
	t.Setenv(registryEnvVar, indexPath)
	return indexPath
}

func TestRegistryNoSourceConfigured(t *testing.T) {
	t.Setenv(registryEnvVar, "")
	res := SkillTool{}.handleRegistrySkill("search deploy", "")
	if !res.IsError {
		t.Fatalf("expected error when registry not configured, got: %s", res.Content)
	}
	if !strings.Contains(res.Content, registryEnvVar) {
		t.Errorf("error should mention %s, got: %s", registryEnvVar, res.Content)
	}
}

func TestRegistryUsageOnEmptySubcommand(t *testing.T) {
	res := SkillTool{}.handleRegistrySkill("", "")
	if !res.IsError {
		t.Fatal("expected usage error for empty subcommand")
	}
	for _, want := range []string{"#registry:search", "#registry:install", "#registry:list"} {
		if !strings.Contains(res.Content, want) {
			t.Errorf("usage text missing %q", want)
		}
	}
}

func TestRegistryInvalidIndexJSON(t *testing.T) {
	bad := filepath.Join(t.TempDir(), "bad.json")
	if err := os.WriteFile(bad, []byte("{not json"), 0644); err != nil {
		t.Fatal(err)
	}
	t.Setenv(registryEnvVar, bad)
	res := SkillTool{}.registrySearch("anything")
	if !res.IsError || !strings.Contains(res.Content, "invalid registry index JSON") {
		t.Fatalf("expected invalid JSON error, got: %+v", res)
	}
}

func TestRegistrySearchRanksNameOverDescription(t *testing.T) {
	writeTestRegistryIndex(t, []RegistryIndexEntry{
		{Name: "other", Description: "helps you deploy things", URL: "https://example.com/other.ggskill"},
		{Name: "deploy-helper", Description: "git flow helper", URL: "https://example.com/deploy-helper.ggskill"},
		{Name: "unrelated", Description: "nothing here", URL: "https://example.com/un.ggskill"},
	})
	res := SkillTool{}.registrySearch("deploy")
	if res.IsError {
		t.Fatalf("search failed: %s", res.Content)
	}
	iName := strings.Index(res.Content, "deploy-helper")
	iDesc := strings.Index(res.Content, "other")
	if iName < 0 || iDesc < 0 {
		t.Fatalf("missing expected entries:\n%s", res.Content)
	}
	if iName > iDesc {
		t.Errorf("name match should rank above description match:\n%s", res.Content)
	}
	if strings.Contains(res.Content, "unrelated") {
		t.Errorf("non-matching entry must not appear:\n%s", res.Content)
	}
	if !strings.Contains(res.Content, "#registry:install") {
		t.Errorf("search output should hint install usage:\n%s", res.Content)
	}
}

func TestRegistryListShowsAllEntries(t *testing.T) {
	writeTestRegistryIndex(t, []RegistryIndexEntry{
		{Name: "alpha", Version: "1.0.0", Description: "first", URL: "https://example.com/a.ggskill"},
		{Name: "beta", Description: "second", URL: "https://example.com/b.ggskill"},
	})
	res := SkillTool{}.registryList()
	if res.IsError {
		t.Fatalf("list failed: %s", res.Content)
	}
	for _, want := range []string{"alpha@1.0.0", "beta", "test-registry"} {
		if !strings.Contains(res.Content, want) {
			t.Errorf("list output missing %q:\n%s", want, res.Content)
		}
	}
}

func TestRegistrySearchNoMatch(t *testing.T) {
	writeTestRegistryIndex(t, []RegistryIndexEntry{
		{Name: "alpha", Description: "aaa", URL: "https://example.com/a.ggskill"},
	})
	res := SkillTool{}.registrySearch("zzz-no-such")
	if res.IsError {
		t.Fatalf("no-match should not be an error: %s", res.Content)
	}
	if !strings.Contains(res.Content, "No skills matching") {
		t.Errorf("unexpected output: %s", res.Content)
	}
}

func TestRegistryInstallLatestAndPinned(t *testing.T) {
	// Each version gets its own dir so both bundles carry manifest name
	// "commit-helper" (exportSkill derives the manifest name from cmd.Name).
	bOld, _ := writeTestRegistryBundle(t, t.TempDir(), "commit-helper", "1.0.0")
	bNew, _ := writeTestRegistryBundle(t, t.TempDir(), "commit-helper", "2.0.0")
	writeTestRegistryIndex(t, []RegistryIndexEntry{
		{Name: "commit-helper", Version: "1.0.0", URL: bOld},
		{Name: "commit-helper", Version: "2.0.0", URL: bNew},
	})
	dest := t.TempDir()
	t.Setenv("HOME", dest) // defaultSkillDestDir uses os.UserHomeDir

	// Bare name -> latest (2.0.0).
	res := SkillTool{}.registryInstall("commit-helper")
	if res.IsError {
		t.Fatalf("install latest failed: %s", res.Content)
	}
	if !strings.Contains(res.Content, "Version: 2.0.0") {
		t.Errorf("expected latest version 2.0.0 installed:\n%s", res.Content)
	}
	installed := filepath.Join(dest, ".ggcode", "skills", "commit-helper", "SKILL.md")
	if _, err := os.Stat(installed); err != nil {
		t.Fatalf("skill not installed to user dir: %v", err)
	}

	// Pinned old version with a locally-installed 2.0.0 -> replacement note.
	lookup := &fakeRegistryLookup{skills: map[string]*commands.Command{
		"commit-helper": {Name: "commit-helper", Version: "2.0.0"},
	}}
	res = SkillTool{Skills: lookup}.registryInstall("commit-helper@1.0.0")
	if res.IsError {
		t.Fatalf("pinned install failed: %s", res.Content)
	}
	if !strings.Contains(res.Content, "Version: 1.0.0") {
		t.Errorf("expected pinned 1.0.0 installed:\n%s", res.Content)
	}
	if !strings.Contains(res.Content, "replaced previously installed version 2.0.0") {
		t.Errorf("expected replacement note:\n%s", res.Content)
	}

	// Same-version reinstall.
	lookupSame := &fakeRegistryLookup{skills: map[string]*commands.Command{
		"commit-helper": {Name: "commit-helper", Version: "1.0.0"},
	}}
	res = SkillTool{Skills: lookupSame}.registryInstall("commit-helper@1.0.0")
	if res.IsError || !strings.Contains(res.Content, "already installed") {
		t.Errorf("expected already-installed note:\n%s", res.Content)
	}

	// Nonexistent pinned version.
	res = SkillTool{}.registryInstall("commit-helper@9.9.9")
	if !res.IsError || !strings.Contains(res.Content, "not found in registry") {
		t.Errorf("expected not-found error for bad pin:\n%s", res.Content)
	}

	// Unknown skill.
	res = SkillTool{}.registryInstall("no-such-skill")
	if !res.IsError || !strings.Contains(res.Content, "not found in registry") {
		t.Errorf("expected not-found error:\n%s", res.Content)
	}
}

func TestRegistryInstallChecksumMismatch(t *testing.T) {
	bundleDir := t.TempDir()
	b1, _ := writeTestRegistryBundle(t, bundleDir, "checked-skill", "1.0.0")
	writeTestRegistryIndex(t, []RegistryIndexEntry{
		{Name: "checked-skill", Version: "1.0.0", URL: b1, SHA256: strings.Repeat("ab", 32)},
	})
	dest := t.TempDir()
	t.Setenv("HOME", dest)
	res := SkillTool{}.registryInstall("checked-skill")
	if !res.IsError {
		t.Fatalf("expected checksum failure:\n%s", res.Content)
	}
	if !strings.Contains(res.Content, "checksum verification failed") || !strings.Contains(res.Content, "nothing was installed") {
		t.Errorf("unexpected error text:\n%s", res.Content)
	}
	// Nothing may be installed on checksum failure.
	if _, err := os.Stat(filepath.Join(dest, ".ggcode", "skills", "checked-skill")); !os.IsNotExist(err) {
		t.Errorf("skill must not be installed after checksum mismatch (err=%v)", err)
	}
}

func TestRegistryInstallValidChecksum(t *testing.T) {
	bundleDir := t.TempDir()
	b1, sum := writeTestRegistryBundle(t, bundleDir, "checked-skill", "1.0.0")
	writeTestRegistryIndex(t, []RegistryIndexEntry{
		{Name: "checked-skill", Version: "1.0.0", URL: b1, SHA256: sum},
	})
	dest := t.TempDir()
	t.Setenv("HOME", dest)
	res := SkillTool{}.registryInstall("checked-skill")
	if res.IsError {
		t.Fatalf("install with valid checksum failed: %s", res.Content)
	}
	if _, err := os.Stat(filepath.Join(dest, ".ggcode", "skills", "checked-skill", "SKILL.md")); err != nil {
		t.Fatalf("skill not installed: %v", err)
	}
}

func TestRegistryPrivateURLRefused(t *testing.T) {
	// A private/loopback http(s) registry must be refused by the SSRF guards
	// inherited from openSkillSource.
	t.Setenv(registryEnvVar, "http://127.0.0.1:1/registry.json")
	res := SkillTool{}.registryList()
	if !res.IsError {
		t.Fatalf("expected refusal for private registry URL:\n%s", res.Content)
	}
	if !strings.Contains(res.Content, "private") {
		t.Errorf("expected private-host refusal, got:\n%s", res.Content)
	}
}

func TestSelectRegistryEntryPicksHighestVersion(t *testing.T) {
	idx := &RegistryIndex{Skills: []RegistryIndexEntry{
		{Name: "x", Version: "1.10.0", URL: "u1"},
		{Name: "x", Version: "1.9.0", URL: "u2"},
		{Name: "x", Version: "2.0.0", URL: "u3"},
		{Name: "y", Version: "5.0.0", URL: "u4"},
	}}
	// Bare name -> highest.
	e, err := selectRegistryEntry(idx, commands.ParseDependency("x"))
	if err != nil || e.URL != "u3" {
		t.Fatalf("expected u3 (2.0.0), got %+v err=%v", e, err)
	}
	// Constraint filters.
	e, err = selectRegistryEntry(idx, commands.ParseDependency("x@<2.0.0"))
	if err != nil || e.URL != "u1" {
		t.Fatalf("expected u1 (1.10.0) for <2.0.0, got %+v err=%v", e, err)
	}
	// Unsatisfiable.
	if _, err = selectRegistryEntry(idx, commands.ParseDependency("x@>=9.0")); err == nil {
		t.Fatal("expected error for unsatisfiable constraint")
	}
	// Case-insensitive name match.
	if _, err = selectRegistryEntry(idx, commands.ParseDependency("X")); err != nil {
		t.Fatalf("expected case-insensitive match, got %v", err)
	}
}

func TestRegistryOversizedIndexRejected(t *testing.T) {
	huge := strings.Repeat("a", maxRegistryIndexSize+10)
	p := filepath.Join(t.TempDir(), "huge.json")
	if err := os.WriteFile(p, []byte(huge), 0644); err != nil {
		t.Fatal(err)
	}
	t.Setenv(registryEnvVar, p)
	res := SkillTool{}.registryList()
	if !res.IsError || !strings.Contains(res.Content, "exceeds max size") {
		t.Fatalf("expected size-cap error, got: %+v", res)
	}
}

func TestRegistryInstallBundleFromBytes(t *testing.T) {
	bundleDir := t.TempDir()
	b1, _ := writeTestRegistryBundle(t, bundleDir, "bytes-skill", "1.2.3")
	data, err := os.ReadFile(b1)
	if err != nil {
		t.Fatal(err)
	}
	dest := t.TempDir()
	manifest, skillDir, err := installBundleFromBytes(data, dest)
	if err != nil {
		t.Fatalf("installBundleFromBytes failed: %v", err)
	}
	if manifest.Name != "bytes-skill" || manifest.Version != "1.2.3" {
		t.Errorf("unexpected manifest: %+v", manifest)
	}
	if _, err := os.Stat(filepath.Join(skillDir, "SKILL.md")); err != nil {
		t.Fatalf("extracted SKILL.md missing: %v", err)
	}
}

func TestReadRegistryBundleRejectsOversize(t *testing.T) {
	// A local "bundle" file longer than MaxSkillBundleSize must be rejected.
	p := filepath.Join(t.TempDir(), "big.ggskill")
	if err := os.WriteFile(p, make([]byte, MaxSkillBundleSize+1), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := readRegistryBundle(p); err == nil {
		t.Fatal("expected oversize rejection")
	}
}

func TestReadRegistryBundleEmptyURL(t *testing.T) {
	if _, err := readRegistryBundle("  "); err == nil {
		t.Fatal("expected error for empty bundle URL")
	}
}

// TestRegistryDispatchViaExecute verifies the "#registry:" prefix routes
// through SkillTool.Execute to the registry handlers (wiring test).
func TestRegistryDispatchViaExecute(t *testing.T) {
	// Skills must be non-nil to pass the availability gate; an empty lookup
	// suffices since #registry: never resolves local skills.
	tool := SkillTool{Skills: &fakeRegistryLookup{skills: map[string]*commands.Command{}}}
	t.Setenv(registryEnvVar, "") // no source -> actionable error, proves dispatch

	input, _ := json.Marshal(map[string]string{"skill": "#registry:list"})
	res, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}
	if !res.IsError || !strings.Contains(res.Content, registryEnvVar) {
		t.Fatalf("expected no-source-configured error via Execute, got: %+v", res)
	}

	// Empty subcommand reaches the usage handler.
	input, _ = json.Marshal(map[string]string{"skill": "#registry:"})
	res, err = tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}
	if !res.IsError || !strings.Contains(res.Content, "#registry:install") {
		t.Fatalf("expected usage via Execute, got: %+v", res)
	}
}
