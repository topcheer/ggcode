package config

// #3519 probe: removeKeysEnv must rewrite keys.env ATOMICALLY (temp+rename,
// same as writeKeysEnvTo) so a crash mid-write cannot destroy every
// remaining API key at once.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIssue3519_RemoveKeysEnvPreservesRemainingAtomically(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	path := KeysEnvPath()
	seed := "# Managed by ggcode - DO NOT EDIT manually.\n" +
		"export KEEPA='one'\n" +
		"export DROPB='two'\n" +
		"export KEEPC='three'\n"
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(seed), 0600); err != nil {
		t.Fatal(err)
	}

	if err := removeKeysEnv([]string{"DROPB"}); err != nil {
		t.Fatalf("removeKeysEnv error = %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	if strings.Contains(got, "DROPB") {
		t.Fatalf("key DROPB must be removed, got:\n%s", got)
	}
	for _, keep := range []string{"KEEPA='one'", "KEEPC='three'"} {
		if !strings.Contains(got, keep) {
			t.Fatalf("remaining key %s lost (silent history erasure class):\n%s", keep, got)
		}
	}
	// No temp residue from the atomic write.
	entries, _ := os.ReadDir(filepath.Dir(path))
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Fatalf("atomic write left tmp residue: %s", e.Name())
		}
	}
	// Mode survives the rewrite (secrets stay owner-only).
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != secureConfigFileMode {
		t.Fatalf("keys.env mode = %v, want %v", info.Mode().Perm(), secureConfigFileMode)
	}
}

func TestIssue3519_RemoveKeysEnvNoopWhenAbsent(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	path := KeysEnvPath()
	seed := "export ONLY='x'\n"
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(seed), 0600); err != nil {
		t.Fatal(err)
	}
	// Removing a key that does not exist must not touch the file at all.
	if err := removeKeysEnv([]string{"GHOST"}); err != nil {
		t.Fatalf("removeKeysEnv(ghost) error = %v", err)
	}
	data, _ := os.ReadFile(path)
	if string(data) != seed {
		t.Fatalf("no-op removal must leave the file byte-identical, got:\n%s", string(data))
	}
}
