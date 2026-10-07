package config

// #3522 probe: SetEndpointAPIKey on a NONEXISTENT endpoint must fail with
// ZERO side effects - no plaintext secret in keys.env, none in the process
// env. The old order persisted the secret first and validated the endpoint
// only afterwards.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIssue3522_BadEndpointZeroSideEffects(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	if err := os.MkdirAll(filepath.Join(dir, ".ggcode"), 0700); err != nil {
		t.Fatal(err)
	}
	keysPath := KeysEnvPath()
	if err := os.WriteFile(keysPath, []byte("# Managed by ggcode - DO NOT EDIT manually.\n"), 0600); err != nil {
		t.Fatal(err)
	}

	c := &Config{Vendors: map[string]VendorConfig{
		"zai": {Endpoints: map[string]EndpointConfig{
			"main": {},
		}},
	}}

	err := c.SetEndpointAPIKey("zai", "typo-endpoint", "sk-secret-3522", false)
	if err == nil {
		t.Fatal("typo'd endpoint must error")
	}
	if !strings.Contains(err.Error(), "not configured") {
		t.Fatalf("error should name the missing endpoint, got: %v", err)
	}

	// keys.env untouched: no secret persisted on the failure path.
	data, err := os.ReadFile(keysPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "sk-secret-3522") {
		t.Fatalf("plaintext secret persisted to keys.env on a FAILED call:\n%s", string(data))
	}
	// Process env untouched too.
	for _, ev := range os.Environ() {
		if strings.Contains(ev, "sk-secret-3522") {
			t.Fatalf("plaintext secret leaked into process env on a FAILED call: %s", ev)
		}
	}
	// And the config object itself was not mutated to a dangling reference.
	if ref := c.Vendors["zai"].Endpoints["main"].APIKey; ref != "" {
		t.Fatalf("endpoint main api_key mutated on failed call: %q", ref)
	}
}

func TestIssue3522_GoodEndpointStillPersists(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)

	c := &Config{Vendors: map[string]VendorConfig{
		"zai": {Endpoints: map[string]EndpointConfig{
			"main": {},
		}},
	}}
	if err := c.SetEndpointAPIKey("zai", "main", "sk-good-3522", false); err != nil {
		t.Fatalf("valid endpoint must succeed: %v", err)
	}
	if got := c.Vendors["zai"].Endpoints["main"].APIKey; !strings.HasPrefix(got, "${") {
		t.Fatalf("endpoint must hold an env reference, got %q", got)
	}
	data, err := os.ReadFile(KeysEnvPath())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "sk-good-3522") {
		t.Fatalf("valid call must persist the key to keys.env:\n%s", string(data))
	}
}
