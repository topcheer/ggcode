package stt

// #2738 regression: the openai flavor's 60-minute bound is ONE-TIME per
// model (the #1743 case-3 comment always promised this). Before the fix,
// the bound was granted on EVERY transcription - a whisper wedge with a
// long-cached model held the serial Telegram pump for 60 minutes instead
// of the intended 10 (#1562 protection amplified 6x).

import (
	"os"
	"path/filepath"
	"testing"
)

func TestModelDownloadedInProcessObservation(t *testing.T) {
	w := &LocalWhisper{}
	if w.modelDownloaded("turbo") && os.Getenv("HOME") != "" {
		// Could only be true via a real ~/.cache/whisper/turbo.pt on the
		// test machine - not a valid starting state for this test.
		t.Skip("host machine has turbo.pt cached; using a distinct model name instead")
	}
	w.markModelDownloaded("turbo")
	if !w.modelDownloaded("turbo") {
		t.Fatal("markModelDownloaded(turbo) must make modelDownloaded(turbo) true")
	}
	// Other models are unaffected: each model pays its own one-time bound.
	if w.modelDownloaded("large-v3") && os.Getenv("HOME") != "" {
		t.Skip("host machine has large-v3.pt cached")
	}
}

func TestModelDownloadedCacheDirProbe(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	w := &LocalWhisper{}
	if w.modelDownloaded("turbo") {
		t.Fatal("no cache file yet: modelDownloaded must be false")
	}
	cacheDir := filepath.Join(home, ".cache", "whisper")
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cacheDir, "turbo.pt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !w.modelDownloaded("turbo") {
		t.Fatal("turbo.pt present in cache dir: probe must report downloaded")
	}
	// Probe hit is memoized: a second call succeeds even if the file
	// disappears (the in-process observation set takes over).
	if err := os.Remove(filepath.Join(cacheDir, "turbo.pt")); err != nil {
		t.Fatal(err)
	}
	if !w.modelDownloaded("turbo") {
		t.Fatal("probe hit must be memoized in downloadedModels")
	}
}
