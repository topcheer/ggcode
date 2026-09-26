package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The quarantine guard (r143): an external config file that fails YAML parsing
// never loads (loadVendorsFile/loadIMFile/loadMCPServersFile return nil), so
// the in-memory section holds only defaults - and the auto "compact migration
// save" in Load would previously overwrite or os.Remove the user's last copy.
// The Save functions must refuse to touch an unparseable file.

const r143CorruptYAML = "adapters:\n  test:\n    - [unclosed\n"

func TestSaveIMConfigRefusesCorruptFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "im.yaml")
	original := r143CorruptYAML
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}

	err := SaveIMConfig(dir, &IMConfig{})
	if err == nil {
		t.Fatal("expected error when im.yaml is unparseable")
	}
	if !strings.Contains(err.Error(), "not valid YAML") {
		t.Fatalf("unexpected error: %v", err)
	}
	got, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(got) != original {
		t.Fatalf("corrupt im.yaml was modified:\n got: %q\nwant: %q", got, original)
	}
}

func TestSaveIMConfigStillRemovesValidEmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "im.yaml")
	if err := os.WriteFile(path, []byte("# comment only\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Valid (comment-only) file + zero-value IMConfig: the existing intended
	// cleanup path must keep working.
	if err := SaveIMConfig(dir, &IMConfig{}); err != nil {
		t.Fatalf("valid empty file should be removed, got error: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("expected comment-only im.yaml to be removed")
	}
}

func TestSaveIMConfigStillWritesOverValidFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "im.yaml")
	if err := os.WriteFile(path, []byte("adapters:\n  a:\n    enabled: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	im := &IMConfig{}
	im.Enabled = true
	if err := SaveIMConfig(dir, im); err != nil {
		t.Fatalf("valid file should be writable, got error: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected im.yaml to exist after write: %v", err)
	}
}

func TestSaveMCPServersRefusesCorruptFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mcp_servers.yaml")
	original := "servers: [unclosed\n"
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}

	err := SaveMCPServers(dir, nil)
	if err == nil || !strings.Contains(err.Error(), "not valid YAML") {
		t.Fatalf("expected quarantine error, got: %v", err)
	}
	got, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(got) != original {
		t.Fatalf("corrupt mcp_servers.yaml was modified: %q", got)
	}
}

func TestSaveVendorsRefusesCorruptFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "vendors.yaml")
	original := "myvendor:\n  base_url: [unclosed\n"
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}

	// Empty/defaults-derived vendor set: previously this hit the
	// "all vendors are defaults - remove the file" path and deleted the
	// user's (hand-recoverable) overrides.
	err := SaveVendors(dir, map[string]VendorConfig{})
	if err == nil || !strings.Contains(err.Error(), "not valid YAML") {
		t.Fatalf("expected quarantine error, got: %v", err)
	}
	got, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(got) != original {
		t.Fatalf("corrupt vendors.yaml was modified: %q", got)
	}
}

func TestExternalFileUnreadable(t *testing.T) {
	dir := t.TempDir()

	// Missing file: not unreadable.
	if externalFileUnreadable(filepath.Join(dir, "missing.yaml")) {
		t.Error("missing file must not be reported unreadable")
	}
	// Valid file.
	valid := filepath.Join(dir, "valid.yaml")
	if err := os.WriteFile(valid, []byte("a: 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if externalFileUnreadable(valid) {
		t.Error("valid YAML must not be reported unreadable")
	}
	// Comment-only file parses to nil - valid, not unreadable.
	comments := filepath.Join(dir, "comments.yaml")
	if err := os.WriteFile(comments, []byte("# just a comment\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if externalFileUnreadable(comments) {
		t.Error("comment-only YAML must not be reported unreadable")
	}
	// Corrupt file.
	corrupt := filepath.Join(dir, "corrupt.yaml")
	if err := os.WriteFile(corrupt, []byte(r143CorruptYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	if !externalFileUnreadable(corrupt) {
		t.Error("unclosed flow sequence must be reported unreadable")
	}
}
