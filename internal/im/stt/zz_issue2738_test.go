package stt

import (
	"os"
	"path/filepath"
	"testing"
)

func TestModelDownloadedInProcessObservation(t *testing.T) {
	w := &LocalWhisper{}
	if w.modelDownloaded("turbo") && os.Getenv("HOME") != "" {

		t.Skip("host machine has turbo.pt cached; using a distinct model name instead")
	}
	w.markModelDownloaded("turbo")
	if !w.modelDownloaded("turbo") {
		t.Fatal("markModelDownloaded(turbo) must make modelDownloaded(turbo) true")
	}

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

	if err := os.Remove(filepath.Join(cacheDir, "turbo.pt")); err != nil {
		t.Fatal(err)
	}
	if !w.modelDownloaded("turbo") {
		t.Fatal("probe hit must be memoized in downloadedModels")
	}
}
