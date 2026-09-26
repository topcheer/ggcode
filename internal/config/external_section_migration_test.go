package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMigrateIMFilePlaintextAPIKeys: plaintext adapter secrets in the
// standalone im.yaml are moved to keys.env and replaced with ${VAR}
// references in the file. Before this existed, im.yaml had no migration
// path at all (#250 covered vendors.yaml only).
func TestMigrateIMFilePlaintextAPIKeys(t *testing.T) {
	withTestHome(t)
	dir := t.TempDir()
	imPath := filepath.Join(dir, "im.yaml")
	keysPath := filepath.Join(dir, "keys.env")

	content := "enabled: true\nadapters:\n  mybot:\n    platform: telegram\n    extra:\n      token: tok-plain-123\n      label: not-a-secret\n"
	if err := os.WriteFile(imPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	findings, err := MigrateIMFilePlaintextAPIKeys(imPath, keysPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 1 {
		t.Fatalf("expected 1 finding, got %d: %+v", len(findings), findings)
	}
	if findings[0].Section != "im" {
		t.Fatalf("expected section im, got %q", findings[0].Section)
	}

	data, _ := os.ReadFile(imPath)
	rewritten := string(data)
	if strings.Contains(rewritten, "tok-plain-123") {
		t.Fatalf("plaintext token still in im.yaml:\n%s", rewritten)
	}
	if !strings.Contains(rewritten, "${GGCODE_IM_MYBOT_TOKEN}") {
		t.Fatalf("token not rewritten as env reference:\n%s", rewritten)
	}
	// Non-secret extra fields must survive untouched.
	if !strings.Contains(rewritten, "label: not-a-secret") {
		t.Fatalf("non-secret extra field lost:\n%s", rewritten)
	}

	keysData, _ := os.ReadFile(keysPath)
	if !strings.Contains(string(keysData), "GGCODE_IM_MYBOT_TOKEN='tok-plain-123'") {
		t.Fatalf("token missing from keys.env:\n%s", keysData)
	}

	// Idempotent: second run finds nothing and leaves the file untouched.
	findings2, err := MigrateIMFilePlaintextAPIKeys(imPath, keysPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings2) != 0 {
		t.Fatalf("expected no findings on second run, got %+v", findings2)
	}
}

// TestMigrateIMFilePlaintextAPIKeys_NoFindings: im.yaml without plaintext
// secrets is left untouched.
func TestMigrateIMFilePlaintextAPIKeys_NoFindings(t *testing.T) {
	withTestHome(t)
	dir := t.TempDir()
	imPath := filepath.Join(dir, "im.yaml")
	original := "enabled: true\nadapters:\n  mybot:\n    extra:\n      token: ${GGCODE_IM_MYBOT_TOKEN}\n"
	if err := os.WriteFile(imPath, []byte(original), 0644); err != nil {
		t.Fatal(err)
	}

	findings, err := MigrateIMFilePlaintextAPIKeys(imPath, filepath.Join(dir, "keys.env"))
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 0 {
		t.Fatalf("expected no findings, got %+v", findings)
	}
	data, _ := os.ReadFile(imPath)
	if string(data) != original {
		t.Fatalf("file unexpectedly rewritten:\n%s", data)
	}
}

// TestMigrateMCPServersFilePlaintextAPIKeys: plaintext env and header values
// in the standalone mcp_servers.yaml (a top-level YAML sequence) are moved
// to keys.env and replaced with ${VAR} references.
func TestMigrateMCPServersFilePlaintextAPIKeys(t *testing.T) {
	withTestHome(t)
	dir := t.TempDir()
	mcpPath := filepath.Join(dir, "mcp_servers.yaml")
	keysPath := filepath.Join(dir, "keys.env")

	content := "- name: myserver\n  type: stdio\n  command: foo\n  env:\n    API_TOKEN: srv-env-secret-1\n  headers:\n    Authorization: srv-hdr-secret-2\n"
	if err := os.WriteFile(mcpPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	findings, err := MigrateMCPServersFilePlaintextAPIKeys(mcpPath, keysPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 2 {
		t.Fatalf("expected 2 findings, got %d: %+v", len(findings), findings)
	}

	data, _ := os.ReadFile(mcpPath)
	rewritten := string(data)
	if strings.Contains(rewritten, "srv-env-secret-1") || strings.Contains(rewritten, "srv-hdr-secret-2") {
		t.Fatalf("plaintext secrets still in mcp_servers.yaml:\n%s", rewritten)
	}
	if !strings.Contains(rewritten, "${GGCODE_MCP_MYSERVER_API_TOKEN}") {
		t.Fatalf("env value not rewritten as env reference:\n%s", rewritten)
	}
	if !strings.Contains(rewritten, "${GGCODE_MCP_MYSERVER_HEADER_AUTHORIZATION}") {
		t.Fatalf("header value not rewritten as env reference:\n%s", rewritten)
	}

	keysData, _ := os.ReadFile(keysPath)
	if !strings.Contains(string(keysData), "GGCODE_MCP_MYSERVER_API_TOKEN='srv-env-secret-1'") {
		t.Fatalf("env secret missing from keys.env:\n%s", keysData)
	}
	if !strings.Contains(string(keysData), "GGCODE_MCP_MYSERVER_HEADER_AUTHORIZATION='srv-hdr-secret-2'") {
		t.Fatalf("header secret missing from keys.env:\n%s", keysData)
	}
}

// TestDetectPlaintextAPIKeysCoversExternalFiles: DetectPlaintextAPIKeys must
// report findings from external section files, not just the main config, so
// the startup warning sees plaintext secrets living in im.yaml or
// mcp_servers.yaml.
func TestDetectPlaintextAPIKeysCoversExternalFiles(t *testing.T) {
	withTestHome(t)
	dir := t.TempDir()
	mainPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(mainPath, []byte("vendor: openai\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "im.yaml"), []byte("adapters:\n  b1:\n    extra:\n      token: tok-detect-1\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "mcp_servers.yaml"), []byte("- name: s1\n  env:\n    KEY: mcp-detect-2\n"), 0644); err != nil {
		t.Fatal(err)
	}

	findings, err := DetectPlaintextAPIKeys(mainPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 2 {
		t.Fatalf("expected 2 findings from external files, got %d: %+v", len(findings), findings)
	}
	seen := map[string]bool{}
	for _, f := range findings {
		seen[f.Section] = true
		if !strings.Contains(f.KeyPath, "${") && f.EnvVar == "" {
			t.Fatalf("finding missing env var: %+v", f)
		}
	}
	if !seen["im"] || !seen["mcp_env"] {
		t.Fatalf("expected im and mcp_env findings, got %+v", findings)
	}
	// Read-only: the external files must be untouched by detection.
	data, _ := os.ReadFile(filepath.Join(dir, "im.yaml"))
	if strings.Contains(string(data), "${") {
		t.Fatalf("detection must not rewrite files:\n%s", data)
	}
}
