package tool

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/topcheer/ggcode/internal/commands"
)

// Skill registry client: discovery and installation of .ggskill bundles from
// a registry index. Motivated by the 2025-2026 skill-registry ecosystem
// (skills.sh hosts tens of thousands of skills; see arXiv 2607.00911,
// "From Registry to Repository") - ggcode could already export/import
// individual .ggskill bundles, but there was no way to discover what a
// registry offers or install a skill by name with version pinning and
// integrity verification.
//
// Registry index format (a single JSON document at the registry URL):
//
//	{
//	  "name": "team-registry",
//	  "updated_at": "2026-01-02T15:04:05Z",
//	  "skills": [
//	    {
//	      "name": "commit-helper",
//	      "description": "Generates conventional commits",
//	      "version": "1.2.0",
//	      "url": "https://example.com/skills/commit-helper.ggskill",
//	      "sha256": "hex-encoded digest of the .ggskill bundle"
//	    }
//	  ]
//	}
//
// The registry source is configured via the GGCODE_SKILL_REGISTRY environment
// variable and may be an http(s):// URL (fetched with the same SSRF guards as
// #import) or a local file path (offline/team use and testing).

// maxRegistryIndexSize caps the registry index document (4 MB) so a hostile
// registry cannot balloon the context or memory.
const maxRegistryIndexSize = 4 << 20

// registryEnvVar names the environment variable holding the registry source.
const registryEnvVar = "GGCODE_SKILL_REGISTRY"

// RegistryIndexEntry is one skill advertised by a registry index.
type RegistryIndexEntry struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Version     string `json:"version,omitempty"`
	URL         string `json:"url"`
	SHA256      string `json:"sha256,omitempty"`
}

// RegistryIndex is the JSON document served at the registry source.
type RegistryIndex struct {
	Name        string               `json:"name,omitempty"`
	UpdatedAt   string               `json:"updated_at,omitempty"`
	Description string               `json:"description,omitempty"`
	Skills      []RegistryIndexEntry `json:"skills"`
}

// handleRegistrySkill dispatches "#registry:<subcommand> [args]".
// Subcommands: search <query> | install <name>[@<version-constraint>] | list.
func (t SkillTool) handleRegistrySkill(rest, args string) Result {
	rest = strings.TrimSpace(rest)
	sub, subArgs, _ := strings.Cut(rest, " ")
	sub = strings.ToLower(strings.TrimSpace(sub))
	subArgs = strings.TrimSpace(subArgs)
	if args != "" && subArgs == "" {
		subArgs = strings.TrimSpace(args)
	}
	switch sub {
	case "search":
		return t.registrySearch(subArgs)
	case "install":
		return t.registryInstall(subArgs)
	case "list":
		return t.registryList()
	default:
		return Result{IsError: true, Content: strings.Join([]string{
			"usage: skill \"#registry:<subcommand> [args]\"",
			"  #registry:search <query>              search the registry index",
			"  #registry:install <name>[@<version>]  install a skill (bare name = latest)",
			"  #registry:list                        list all registry entries",
			"",
			"Configure the registry source via the " + registryEnvVar + " environment variable",
			"(http(s):// URL or local path to a registry index JSON document).",
		}, "\n")}
	}
}

// resolveRegistrySource returns the configured registry index source, or an
// error explaining how to configure it.
func resolveRegistrySource() (string, error) {
	src := strings.TrimSpace(os.Getenv(registryEnvVar))
	if src == "" {
		return "", fmt.Errorf("no skill registry configured: set %s to an http(s):// URL or a local path of a registry index JSON document", registryEnvVar)
	}
	return src, nil
}

// fetchRegistryIndex loads and parses the registry index from src
// (http(s) URL via the SSRF-guarded openSkillSource, or a local path).
func fetchRegistryIndex(src string) (*RegistryIndex, error) {
	reader, cleanup, err := openSkillSource(src)
	if err != nil {
		return nil, fmt.Errorf("cannot open registry index %s: %w", src, err)
	}
	defer cleanup()
	idx, err := parseRegistryIndex(reader)
	if err != nil {
		return nil, err
	}
	if len(idx.Skills) == 0 {
		return nil, fmt.Errorf("registry index contains no skills")
	}
	return idx, nil
}

// parseRegistryIndex reads and validates the index document (size-capped).
func parseRegistryIndex(reader io.Reader) (*RegistryIndex, error) {
	data, err := io.ReadAll(io.LimitReader(reader, maxRegistryIndexSize+1))
	if err != nil {
		return nil, fmt.Errorf("cannot read registry index: %w", err)
	}
	if len(data) > maxRegistryIndexSize {
		return nil, fmt.Errorf("registry index exceeds max size of %d bytes", maxRegistryIndexSize)
	}
	var idx RegistryIndex
	if err := json.Unmarshal(data, &idx); err != nil {
		return nil, fmt.Errorf("invalid registry index JSON: %w", err)
	}
	return &idx, nil
}

// registrySearch searches the index by keyword over names and descriptions.
// Name matches rank above description-only matches; ties break by name.
func (t SkillTool) registrySearch(query string) Result {
	src, err := resolveRegistrySource()
	if err != nil {
		return Result{IsError: true, Content: err.Error()}
	}
	idx, err := fetchRegistryIndex(src)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("failed to fetch registry: %v", err)}
	}

	type hit struct {
		entry RegistryIndexEntry
		rank  int
	}
	query = strings.ToLower(strings.TrimSpace(query))
	var hits []hit
	for _, e := range idx.Skills {
		name := strings.ToLower(e.Name)
		desc := strings.ToLower(e.Description)
		var rank int
		switch {
		case query == "":
			rank = 1 // list-all fallback
		case strings.Contains(name, query):
			rank = 3
		case strings.Contains(desc, query):
			rank = 2
		default:
			continue
		}
		hits = append(hits, hit{entry: e, rank: rank})
	}
	if len(hits) == 0 {
		return Result{Content: fmt.Sprintf("No skills matching %q found in registry %q.", query, src)}
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].rank != hits[j].rank {
			return hits[i].rank > hits[j].rank
		}
		return hits[i].entry.Name < hits[j].entry.Name
	})
	const maxHits = 20
	if len(hits) > maxHits {
		hits = hits[:maxHits]
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Registry %q", src))
	if idx.Name != "" {
		sb.WriteString(fmt.Sprintf(" (%s)", idx.Name))
	}
	sb.WriteString(fmt.Sprintf(" - %d match(es):\n\n", len(hits)))
	for _, h := range hits {
		sb.WriteString(fmt.Sprintf("- %s", h.entry.Name))
		if v := strings.TrimSpace(h.entry.Version); v != "" {
			sb.WriteString(fmt.Sprintf("@%s", v))
		}
		if h.entry.Description != "" {
			sb.WriteString(fmt.Sprintf(" - %s", h.entry.Description))
		}
		sb.WriteString("\n")
	}
	sb.WriteString("\nInstall with: skill \"#registry:install <name>[@<version>]\"")
	return Result{Content: sb.String()}
}

// registryList lists every entry in the registry index.
func (t SkillTool) registryList() Result {
	return t.registrySearch("")
}

// parseInstallSpec splits "name" or "name@<constraint>" using the same
// dependency syntax as skill frontmatter (commands.ParseDependency).
func parseInstallSpec(spec string) (commands.DependencyConstraint, error) {
	dc := commands.ParseDependency(spec)
	if dc.Name == "" {
		return dc, fmt.Errorf("invalid install spec %q: expected <name> or <name>@<version>", spec)
	}
	return dc, nil
}

// registryInstall downloads and installs a skill from the registry by name,
// with optional version constraint. If the index provides a sha256 digest it
// is verified before extraction; on mismatch nothing is installed.
func (t SkillTool) registryInstall(spec string) Result {
	if spec == "" {
		return Result{IsError: true, Content: "usage: skill \"#registry:install <name>[@<version>]\""}
	}
	dc, err := parseInstallSpec(spec)
	if err != nil {
		return Result{IsError: true, Content: err.Error()}
	}
	src, err := resolveRegistrySource()
	if err != nil {
		return Result{IsError: true, Content: err.Error()}
	}
	idx, err := fetchRegistryIndex(src)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("failed to fetch registry: %v", err)}
	}
	entry, err := selectRegistryEntry(idx, dc)
	if err != nil {
		return Result{IsError: true, Content: err.Error()}
	}

	data, err := readRegistryBundle(entry.URL)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("failed to download skill %q: %v", entry.Name, err)}
	}
	if err := verifyRegistryChecksum(data, entry.SHA256); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("checksum verification failed for skill %q: %v; nothing was installed", entry.Name, err)}
	}

	destDir := defaultSkillDestDir()
	manifest, skillDir, err := installBundleFromBytes(data, destDir)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("failed to install skill %q: %v", entry.Name, err)}
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Skill %q installed from registry.\n", manifest.Name))
	sb.WriteString(fmt.Sprintf("Location: %s\n", skillDir))
	if v := strings.TrimSpace(manifest.Version); v != "" {
		sb.WriteString(fmt.Sprintf("Version: %s\n", v))
	}
	if existing, ok := t.installedVersion(manifest.Name); ok {
		if existing == strings.TrimSpace(manifest.Version) {
			sb.WriteString(fmt.Sprintf("Note: skill was already installed at this version (overwritten in place).\n"))
		} else {
			sb.WriteString(fmt.Sprintf("Note: replaced previously installed version %s.\n", nonEmptyVersion(existing)))
		}
	}
	sb.WriteString("\nThe skill is now available. Use the skill tool to load it.")
	return Result{Content: sb.String()}
}

// installedVersion returns the locally installed version of a skill, if any.
func (t SkillTool) installedVersion(name string) (string, bool) {
	if t.Skills == nil {
		return "", false
	}
	cmd, ok := t.Skills.Get(name)
	if !ok || cmd == nil {
		return "", false
	}
	return strings.TrimSpace(cmd.Version), true
}

// defaultSkillDestDir mirrors handleImportSkill's default destination.
func defaultSkillDestDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".ggcode/skills"
	}
	return filepath.Join(home, ".ggcode", "skills")
}

// selectRegistryEntry picks the entry for dc.Name: the highest version
// satisfying dc's constraint (bare name = latest overall).
func selectRegistryEntry(idx *RegistryIndex, dc commands.DependencyConstraint) (RegistryIndexEntry, error) {
	var best RegistryIndexEntry
	found := false
	for _, e := range idx.Skills {
		if !strings.EqualFold(strings.TrimSpace(e.Name), dc.Name) {
			continue
		}
		if dc.Version != "" && !commands.CheckVersionConstraint(e.Version, dc.Op, dc.Version) {
			continue
		}
		if !found || commands.CompareVersions(e.Version, best.Version) > 0 {
			best, found = e, true
		}
	}
	if !found {
		if dc.Version != "" {
			return RegistryIndexEntry{}, fmt.Errorf("skill %q not found in registry at version %s%s", dc.Name, dc.Op, dc.Version)
		}
		return RegistryIndexEntry{}, fmt.Errorf("skill %q not found in registry", dc.Name)
	}
	return best, nil
}

// readRegistryBundle downloads a registry bundle fully into memory with the
// same size cap as #import (needed to hash before extraction).
func readRegistryBundle(bundleURL string) ([]byte, error) {
	if strings.TrimSpace(bundleURL) == "" {
		return nil, fmt.Errorf("registry entry has no bundle URL")
	}
	reader, cleanup, err := openSkillSource(bundleURL)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	data, err := io.ReadAll(io.LimitReader(reader, MaxSkillBundleSize+1))
	if err != nil {
		return nil, fmt.Errorf("cannot download bundle: %w", err)
	}
	if len(data) > MaxSkillBundleSize {
		return nil, fmt.Errorf("bundle exceeds max size of %d bytes", MaxSkillBundleSize)
	}
	return data, nil
}

// verifyRegistryChecksum checks data against a hex-encoded sha256 digest.
// Empty digest = no verification (registry opted out).
func verifyRegistryChecksum(data []byte, want string) error {
	want = strings.ToLower(strings.TrimSpace(want))
	if want == "" {
		return nil
	}
	sum := sha256.Sum256(data)
	got := hex.EncodeToString(sum[:])
	if got != want {
		return fmt.Errorf("sha256 mismatch (got %s, want %s)", got, want)
	}
	return nil
}

// installBundleFromBytes extracts bundle bytes by handing them to the
// existing importSkill pipeline via a temp file (path traversal protection,
// size caps, manifest validation are all inherited unchanged).
func installBundleFromBytes(data []byte, destDir string) (*SkillManifest, string, error) {
	tmp, err := os.CreateTemp("", "ggcode-registry-*.ggskill")
	if err != nil {
		return nil, "", fmt.Errorf("cannot create temp file: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return nil, "", fmt.Errorf("cannot stage bundle: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return nil, "", fmt.Errorf("cannot stage bundle: %w", err)
	}
	return importSkill(tmpPath, destDir)
}

// buildRegistryIndexJSON is a test/export helper that serializes an index.
func buildRegistryIndexJSON(idx *RegistryIndex) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	if err := enc.Encode(idx); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
