//go:build goolm

package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// Both-exist steady state: the main ggcode.yaml still carries a vendors
// section while vendors.yaml also exists (failed external migration,
// restored backup, hand edit, downgrade write-back). Load prefers the
// main-file values, and pre-fix the Save tombstones for dotted endpoint
// names were silently skipped because the path was split on "." (#5779).
const issue5779MainYAML = "vendor: github-copilot\n" +
	"endpoint: github.com\n" +
	"model: gpt-4o\n" +
	"vendors:\n" +
	"  github-copilot:\n" +
	"    endpoints:\n" +
	"      github.com:\n" +
	"        display_name: GitHub.com\n" +
	"        protocol: copilot\n" +
	"        base_url: https://api.githubcopilot.com\n" +
	"        context_window: 123456\n" +
	"        max_tokens: 8192\n" +
	"      enterprise:\n" +
	"        display_name: GitHub Enterprise\n" +
	"        protocol: copilot\n" +
	"        base_url: https://ghe.example.com\n" +
	"        context_window: 777777\n" +
	"        max_tokens: 4096\n"

const issue5779VendorsYAML = "github-copilot:\n" +
	"  endpoints:\n" +
	"    github.com:\n" +
	"      display_name: GitHub.com\n" +
	"      protocol: copilot\n" +
	"      base_url: https://api.githubcopilot.com\n" +
	"    enterprise:\n" +
	"      display_name: GitHub Enterprise\n" +
	"      protocol: copilot\n" +
	"      base_url: https://ghe.example.com\n"

func issue5779Setup(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	cfgDir := filepath.Join(home, ".ggcode")
	if err := os.MkdirAll(cfgDir, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	cfgPath := filepath.Join(cfgDir, "ggcode.yaml")
	if err := os.WriteFile(cfgPath, []byte(issue5779MainYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "vendors.yaml"), []byte(issue5779VendorsYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	return cfgPath
}

func TestIssue5779DotEndpointZeroClearInBothExistState(t *testing.T) {
	cfgPath := issue5779Setup(t)

	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	eps := cfg.Vendors["github-copilot"].Endpoints
	if got := eps["github.com"].ContextWindow; got != 123456 {
		t.Fatalf("precondition: main-file residue should win at Load, github.com context_window = %d", got)
	}

	// Clear the model limits of BOTH endpoints: the dotted one (github.com)
	// is the #5779 repro, the dot-free one (enterprise) is the control.
	for _, name := range []string{"github.com", "enterprise"} {
		ep := cfg.Vendors["github-copilot"].Endpoints[name]
		ep.ContextWindow = 0
		ep.MaxTokens = 0
		cfg.Vendors["github-copilot"].Endpoints[name] = ep
	}
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// The main file must self-heal out of the both-exist state.
	mainRaw, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	mainMap := map[string]interface{}{}
	if err := yaml.Unmarshal(mainRaw, &mainMap); err != nil {
		t.Fatalf("re-parse main config: %v\n%s", err, mainRaw)
	}
	if _, still := mainMap["vendors"]; still {
		t.Fatalf("#5779: stale vendors section still in main config after Save:\n%s", mainRaw)
	}

	// The cleared pins must be gone from the persisted vendor data too.
	vendorsRaw, err := os.ReadFile(filepath.Join(filepath.Dir(cfgPath), "vendors.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(vendorsRaw), "123456") || strings.Contains(string(vendorsRaw), "8192") {
		t.Fatalf("#5779: stale limits leaked into vendors.yaml:\n%s", vendorsRaw)
	}

	cfg2, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("re-Load: %v", err)
	}
	// Load() seeds from DefaultConfig, so an explicitly-cleared ("unset")
	// limit may legitimately surface as an inferred default in memory; the
	// #5779 contract is that the cleared STALE value never resurrects.
	epDot := cfg2.Vendors["github-copilot"].Endpoints["github.com"]
	if epDot.ContextWindow == 123456 || epDot.MaxTokens == 8192 {
		t.Fatalf("#5779: dotted endpoint github.com stale limits resurrected: context_window=%d max_tokens=%d", epDot.ContextWindow, epDot.MaxTokens)
	}
	if epDot.BaseURL != "https://api.githubcopilot.com" || epDot.Protocol != "copilot" {
		t.Fatalf("#5779: dotted endpoint data lost on reload: %+v", epDot)
	}
	epCtl := cfg2.Vendors["github-copilot"].Endpoints["enterprise"]
	if epCtl.ContextWindow == 777777 || epCtl.MaxTokens == 4096 {
		t.Fatalf("#5779: control endpoint enterprise stale limits resurrected: context_window=%d max_tokens=%d", epCtl.ContextWindow, epCtl.MaxTokens)
	}
	if epCtl.BaseURL != "https://ghe.example.com" {
		t.Fatalf("#5779: control endpoint data lost on reload: %+v", epCtl)
	}
}

func TestIssue5779EndpointZeroClearPathsAreSegments(t *testing.T) {
	c := &Config{Vendors: map[string]VendorConfig{
		"github-copilot": {Endpoints: map[string]EndpointConfig{
			"github.com": {},
		}},
	}}
	paths := endpointZeroClearPaths(c)
	want := map[string]bool{
		tombstoneKey([]string{"vendors", "github-copilot", "endpoints", "github.com", "context_window"}): false,
		tombstoneKey([]string{"vendors", "github-copilot", "endpoints", "github.com", "max_tokens"}):     false,
	}
	if len(paths) != 2 {
		t.Fatalf("expected 2 zero-clear paths, got %d: %v", len(paths), paths)
	}
	for _, segs := range paths {
		key := tombstoneKey(segs)
		if _, ok := want[key]; !ok {
			t.Fatalf("unexpected zero-clear path: %v", segs)
		}
		want[key] = true
	}
	for key, seen := range want {
		if !seen {
			t.Fatalf("missing zero-clear path: %q", key)
		}
	}
}

func TestIssue5779ApplyZeroNumberClearsDottedEndpoint(t *testing.T) {
	existing := map[string]interface{}{
		"vendors": map[string]interface{}{
			"github-copilot": map[string]interface{}{
				"endpoints": map[string]interface{}{
					"github.com": map[string]interface{}{
						"protocol":       "copilot",
						"context_window": 123456,
						"max_tokens":     8192,
					},
				},
			},
		},
	}
	applyZeroNumberClears(existing, [][]string{
		{"vendors", "github-copilot", "endpoints", "github.com", "context_window"},
		{"vendors", "github-copilot", "endpoints", "github.com", "max_tokens"},
	})
	if _, exists := yamlPathLookup(existing, []string{"vendors", "github-copilot", "endpoints", "github.com", "context_window"}); exists {
		t.Fatalf("#5779: context_window survived zero-clear for dotted endpoint: %v", existing)
	}
	if _, exists := yamlPathLookup(existing, []string{"vendors", "github-copilot", "endpoints", "github.com", "max_tokens"}); exists {
		t.Fatalf("#5779: max_tokens survived zero-clear for dotted endpoint: %v", existing)
	}
	if v, exists := yamlPathLookup(existing, []string{"vendors", "github-copilot", "endpoints", "github.com", "protocol"}); !exists || v != "copilot" {
		t.Fatalf("protocol was collateral damage: exists=%v value=%v", exists, v)
	}
}

func TestIssue5779StringTombstoneDottedPath(t *testing.T) {
	disk := map[string]interface{}{
		"section": map[string]interface{}{
			"github.com": map[string]interface{}{
				"note": "stale-5779",
			},
		},
	}
	snap := snapshotDiskStringKeys(disk)
	if snap[tombstoneKey([]string{"section", "github.com", "note"})] != "stale-5779" {
		t.Fatalf("#5779: snapshot key shattered for dotted path: %v", snap)
	}
	applyClearedTombstones(disk, snap, [][]string{{"section", "github.com", "note"}})
	if _, exists := yamlPathLookup(disk, []string{"section", "github.com", "note"}); exists {
		t.Fatalf("#5779: string tombstone missed dotted path: %v", disk)
	}
}
