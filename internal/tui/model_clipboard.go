package tui

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"

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
