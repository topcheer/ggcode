package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestComputeEnvFingerprintStable(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x\n"), 0644)
	os.WriteFile(filepath.Join(dir, "go.sum"), []byte("hash1\n"), 0644)

	old := goVersionCmd
	goVersionCmd = func() string { return "go1.27.4" }
	t.Cleanup(func() { goVersionCmd = old })

	h1 := computeEnvFingerprint(dir)
	h2 := computeEnvFingerprint(dir)
	if h1 != h2 || h1 == "" {
		t.Fatalf("fingerprint must be deterministic, got %q vs %q", h1, h2)
	}

	// Changing go.sum content must change the fingerprint.
	os.WriteFile(filepath.Join(dir, "go.sum"), []byte("hash2\n"), 0644)
	if h3 := computeEnvFingerprint(dir); h3 == h1 {
		t.Fatal("manifest change must shift the fingerprint")
	}

	// Changing toolchain must shift it too.
	goVersionCmd = func() string { return "go1.28.0" }
	if h4 := computeEnvFingerprint(dir); h4 == h1 {
		t.Fatal("toolchain change must shift the fingerprint")
	}
}

func TestCheckEnvDriftBaselineQuietThenWarnOnce(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x\n"), 0644)

	old := goVersionCmd
	goVersionCmd = func() string { return "go1.27.4" }
	t.Cleanup(func() { goVersionCmd = old })

	if msg := checkEnvDrift(dir); msg != "" {
		t.Fatalf("first run must establish baseline quietly, got %q", msg)
	}
	if msg := checkEnvDrift(dir); msg != "" {
		t.Fatalf("unchanged environment must stay quiet, got %q", msg)
	}

	// Toolchain drift -> warn once, and the baseline is updated so a
	// repeated check stays quiet.
	goVersionCmd = func() string { return "go1.28.0" }
	msg := checkEnvDrift(dir)
	if !strings.Contains(msg, "[env-drift]") || !strings.Contains(msg, "stale") {
		t.Fatalf("drift must warn, got %q", msg)
	}
	if msg2 := checkEnvDrift(dir); msg2 != "" {
		t.Fatalf("re-check after baseline update must be quiet, got %q", msg2)
	}

	// Persisted state survives "restart": re-reading from disk warns again
	// on further change without any in-memory state.
	goVersionCmd = func() string { return "go1.29.1" }
	if msg3 := checkEnvDrift(dir); !strings.Contains(msg3, "[env-drift]") {
		t.Fatalf("cross-restart drift must warn, got %q", msg3)
	}
}

func TestCheckEnvDriftEmptyDirQuiet(t *testing.T) {
	if msg := checkEnvDrift(t.TempDir()); msg != "" {
		t.Fatalf("fresh workspace must establish baseline quietly, got %q", msg)
	}
}
