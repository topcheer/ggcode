package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// #4643: the "existing file is unparseable" fallback branch used the raw
// currentData marshal (external sections + instance-only fields unstripped),
// so a corrupted main yaml caused Save() to write vendors/im/mcp_servers and
// instance secrets straight back into the main config file. The fallback must
// behave like the first-write branch: stripped currentRaw.
func TestIssue4643_UnparseableFallbackStripsExternalSections(t *testing.T) {
	home := t.TempDir()
	cfgDir := filepath.Join(home, ".ggcode")
	if err := os.MkdirAll(cfgDir, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	cfgPath := filepath.Join(cfgDir, "ggcode.yaml")
	globalYAML := `
vendor: testv
endpoint: main
model: m1
vendors:
  testv:
    api_key: key4643
    endpoints:
      main:
        protocol: openai
        base_url: https://v4643.example.com/v1
`
	if err := os.WriteFile(cfgPath, []byte(globalYAML), 0600); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadWithInstance(cfgPath, t.TempDir())
	if err != nil {
		t.Fatalf("LoadWithInstance: %v", err)
	}

	// Corrupt the main file so Save() takes the unparseable-fallback branch.
	if err := os.WriteFile(cfgPath, []byte("{{{ not: valid: yaml: [[["), 0600); err != nil {
		t.Fatal(err)
	}
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save on unparseable file: %v", err)
	}

	got, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, section := range []string{"vendors:", "im:", "mcp_servers:"} {
		if strings.Contains(string(got), section) {
			t.Fatalf("fallback write leaked external section %q into the main config file (#4643)", section)
		}
	}
	if !strings.Contains(string(got), "testv") || strings.Contains(string(got), "key4643") {
		// vendor selection may persist as a name reference, but never the key
		t.Fatalf("fallback write must not persist vendor credentials: %s", got)
	}
}
