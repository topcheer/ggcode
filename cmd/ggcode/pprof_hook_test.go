package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPprofEndpoint_EnvWins(t *testing.T) {
	t.Setenv("GGCODE_PPROF", "6060")
	p, ok := pprofEndpoint()
	if !ok || p != "6060" {
		t.Fatalf("env: got %q ok=%v", p, ok)
	}
}

func TestPprofEndpoint_MarkerFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("GGCODE_PPROF", "")
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".ggcode"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".ggcode", "pprof-port"), []byte("6161\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	p, ok := pprofEndpoint()
	if !ok || p != "6161" {
		t.Fatalf("marker: got %q ok=%v", p, ok)
	}
}

func TestPprofEndpoint_MarkerInvalidAndMissing(t *testing.T) {
	home := t.TempDir()
	t.Setenv("GGCODE_PPROF", "")
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".ggcode"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, content := range []string{"notaport", "0", "70000", ""} {
		if err := os.WriteFile(filepath.Join(home, ".ggcode", "pprof-port"), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, ok := pprofEndpoint(); ok {
			t.Fatalf("invalid marker %q must not enable pprof", content)
		}
	}
	// Missing file: disabled.
	if err := os.Remove(filepath.Join(home, ".ggcode", "pprof-port")); err != nil {
		t.Fatal(err)
	}
	if _, ok := pprofEndpoint(); ok {
		t.Fatal("missing marker must not enable pprof")
	}
}
