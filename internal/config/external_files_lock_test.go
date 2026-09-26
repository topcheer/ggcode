package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The steady-state external section writers (SaveVendors, SaveIMConfig,
// SaveMCPServers, MigrateVendorsFilePlaintextAPIKeys) perform full-file
// read-modify-write or full-file overwrite cycles. Before this fix they ran
// without lockConfigFile, so they raced the locked patch* writers
// (patchExternalFile, patchIMAdapterFile) and same-process Saves from a
// different scope: the last full-file write silently dropped the other
// writer's fields. These tests pin the fix: each writer must block while the
// test holds the per-file lock and complete after release, following the
// TestMigrateSectionToExternalHoldsLock pattern.

// TestExternalSectionWritersHoldConfigLock verifies the Save* external
// section writers acquire lockConfigFile(<file>) before touching the file.
func TestExternalSectionWritersHoldConfigLock(t *testing.T) {
	dir := t.TempDir()

	t.Run("SaveVendors", func(t *testing.T) {
		path := VendorsPath(dir)
		unlock := lockConfigFile(path)

		done := make(chan struct{})
		var err error
		go func() {
			defer close(done)
			err = SaveVendors(dir, map[string]VendorConfig{
				"acme": {
					DisplayName: "Acme",
					Endpoints: map[string]EndpointConfig{
						"main": {Protocol: "openai", BaseURL: "https://api.acme.example", MaxTokens: 4096},
					},
				},
			})
		}()

		select {
		case <-done:
			unlock()
			t.Fatal("SaveVendors completed while the vendors.yaml lock was held; it does not acquire lockConfigFile")
		case <-time.After(300 * time.Millisecond):
			// Blocked as expected.
		}
		unlock()
		select {
		case <-done:
			if err != nil {
				t.Fatalf("SaveVendors: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("SaveVendors did not complete after the lock was released")
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil || !strings.Contains(string(data), "acme") {
			t.Fatalf("vendors.yaml not written after unlock: %v", readErr)
		}
	})

	t.Run("SaveIMConfig", func(t *testing.T) {
		path := IMPath(dir)
		writeTestFile(t, path, "output_mode: quiet\n")
		unlock := lockConfigFile(path)

		done := make(chan struct{})
		var err error
		go func() {
			defer close(done)
			err = SaveIMConfig(dir, &IMConfig{})
		}()

		select {
		case <-done:
			unlock()
			t.Fatal("SaveIMConfig completed while the im.yaml lock was held; it does not acquire lockConfigFile")
		case <-time.After(300 * time.Millisecond):
			// Blocked as expected (empty IMConfig still takes the lock before
			// the remove branch).
		}
		unlock()
		select {
		case <-done:
			if err != nil {
				t.Fatalf("SaveIMConfig: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("SaveIMConfig did not complete after the lock was released")
		}
		if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
			t.Fatalf("empty IMConfig should remove im.yaml, stat err: %v", statErr)
		}
	})

	t.Run("SaveMCPServers", func(t *testing.T) {
		path := MCPServersPath(dir)
		unlock := lockConfigFile(path)

		done := make(chan struct{})
		var err error
		go func() {
			defer close(done)
			err = SaveMCPServers(dir, []MCPServerConfig{{Name: "srv1", Command: "echo"}})
		}()

		select {
		case <-done:
			unlock()
			t.Fatal("SaveMCPServers completed while the mcp_servers.yaml lock was held; it does not acquire lockConfigFile")
		case <-time.After(300 * time.Millisecond):
			// Blocked as expected.
		}
		unlock()
		select {
		case <-done:
			if err != nil {
				t.Fatalf("SaveMCPServers: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("SaveMCPServers did not complete after the lock was released")
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil || !strings.Contains(string(data), "srv1") {
			t.Fatalf("mcp_servers.yaml not written after unlock: %v", readErr)
		}
	})
}

// TestMigrateVendorsFilePlaintextAPIKeysHoldsConfigLock verifies the
// vendors.yaml plaintext-key migration holds the per-file lock across its
// read-modify-write window (read vendors.yaml, persist to keys.env, rewrite
// with ${VAR} references).
func TestMigrateVendorsFilePlaintextAPIKeysHoldsConfigLock(t *testing.T) {
	withTestHome(t)
	dir := t.TempDir()
	vendorsPath := filepath.Join(dir, "vendors.yaml")
	keysPath := filepath.Join(dir, "keys.env")
	writeTestFile(t, vendorsPath, "lockvendor:\n  display_name: Lock Vendor\n  api_key: sk-lock-secret-789\n")

	unlock := lockConfigFile(vendorsPath)

	done := make(chan struct{})
	var findings []APIKeyFinding
	var err error
	go func() {
		defer close(done)
		findings, err = MigrateVendorsFilePlaintextAPIKeys(vendorsPath, keysPath)
	}()

	select {
	case <-done:
		unlock()
		t.Fatal("migration completed while the vendors.yaml lock was held; it does not acquire lockConfigFile")
	case <-time.After(300 * time.Millisecond):
		// Blocked as expected.
	}
	unlock()
	select {
	case <-done:
		if err != nil {
			t.Fatalf("MigrateVendorsFilePlaintextAPIKeys: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("migration did not complete after the lock was released")
	}

	if len(findings) != 1 {
		t.Fatalf("expected 1 finding, got %d: %+v", len(findings), findings)
	}
	data, readErr := os.ReadFile(vendorsPath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	content := string(data)
	if strings.Contains(content, "sk-lock-secret-789") {
		t.Fatalf("plaintext key still in vendors.yaml:\n%s", content)
	}
	if !strings.Contains(content, "${LOCKVENDOR_API_KEY}") {
		t.Fatalf("key not rewritten as env reference:\n%s", content)
	}
	keysData, keysErr := os.ReadFile(keysPath)
	if keysErr != nil || !strings.Contains(string(keysData), "LOCKVENDOR_API_KEY='sk-lock-secret-789'") {
		t.Fatalf("keys.env missing migrated key: %v", keysErr)
	}
}
