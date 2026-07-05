package tui

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/topcheer/ggcode/internal/image"
)

func loadClipboardImage() (imageAttachedMsg, error) {
	img, err := image.ReadClipboard()
	if err != nil {
		return imageAttachedMsg{}, err
	}
	filename, err := newClipboardImageFilename()
	if err != nil {
		return imageAttachedMsg{}, err
	}
	sourcePath, err := persistAttachedImage(filename, img)
	if err != nil {
		return imageAttachedMsg{}, err
	}
	return imageAttachedMsg{
		placeholder: image.Placeholder(filename, img),
		img:         img,
		filename:    filename,
		sourcePath:  sourcePath,
	}, nil
}

func newClipboardImageFilename() (string, error) {
	var suffix [4]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return "", fmt.Errorf("generating clipboard image filename: %w", err)
	}
	return "ggcode-image-" + hex.EncodeToString(suffix[:]) + ".png", nil
}

func persistAttachedImage(filename string, img image.Image) (string, error) {
	cacheDir := filepath.Join(os.TempDir(), "ggcode-images")
	if err := os.MkdirAll(cacheDir, 0o700); err != nil {
		return "", fmt.Errorf("creating image cache dir: %w", err)
	}
	path := filepath.Join(cacheDir, filepath.Base(filename))
	if err := os.WriteFile(path, img.Data, 0o600); err != nil {
		return "", fmt.Errorf("writing attached image: %w", err)
	}
	return path, nil
}

// cleanupOldTempImages removes image files older than 24 hours from the
// ggcode-images temp directory. Called once at startup to prevent
// unbounded disk usage from accumulated clipboard/tunnel images.
func cleanupOldTempImages() {
	cacheDir := filepath.Join(os.TempDir(), "ggcode-images")
	entries, err := os.ReadDir(cacheDir)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-24 * time.Hour)
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.ModTime().Before(cutoff) {
			os.Remove(filepath.Join(cacheDir, e.Name()))
		}
	}
}
