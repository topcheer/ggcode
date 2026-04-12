package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/image"
)

func TestBuildAgentSubmissionContentUsesLocalImagePathHint(t *testing.T) {
	img := &imageAttachedMsg{
		img:        image.Image{Data: []byte{0x89, 0x50, 0x4E, 0x47}, MIME: image.MIMEPNG, Width: 10, Height: 10},
		filename:   "ggcode-image-deadbeef.png",
		sourcePath: "/tmp/ggcode-image-deadbeef.png",
	}

	content := buildAgentSubmissionContent("帮我看看", img, false)
	if len(content) != 1 {
		t.Fatalf("expected text-only fallback content, got %d blocks", len(content))
	}
	if !strings.Contains(content[0].Text, "/tmp/ggcode-image-deadbeef.png") {
		t.Fatalf("expected prompt to include local image path, got %q", content[0].Text)
	}
}

func TestBuildAgentSubmissionContentAddsImageBlockWhenEnabled(t *testing.T) {
	img := &imageAttachedMsg{
		img:        image.Image{Data: []byte{0x89, 0x50, 0x4E, 0x47}, MIME: image.MIMEPNG, Width: 10, Height: 10},
		filename:   "ggcode-image-deadbeef.png",
		sourcePath: "/tmp/ggcode-image-deadbeef.png",
	}

	content := buildAgentSubmissionContent("帮我看看", img, true)
	if len(content) != 2 {
		t.Fatalf("expected text + image content, got %d blocks", len(content))
	}
	if content[1].Type != "image" {
		t.Fatalf("expected second block to be image, got %q", content[1].Type)
	}
	if !strings.Contains(content[0].Text, "Prefer native vision understanding first") {
		t.Fatalf("expected prompt to prefer native vision, got %q", content[0].Text)
	}
}

func TestActiveEndpointSupportsVisionUsesResolvedConfig(t *testing.T) {
	m := newTestModel()
	cfg := config.DefaultConfig()
	cfg.Vendor = "openai"
	cfg.Endpoint = "api"
	cfg.Model = "gpt-4o"
	m.config = cfg

	if !m.activeEndpointSupportsVision() {
		t.Fatal("expected active endpoint to report vision support")
	}
}

func TestPersistAttachedImageWritesStableFile(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	img := image.Image{Data: []byte{0x89, 0x50, 0x4E, 0x47}, MIME: image.MIMEPNG}

	path, err := persistAttachedImage("ggcode-image-test.png", img)
	if err != nil {
		t.Fatalf("persistAttachedImage() error = %v", err)
	}
	if filepath.Base(path) != "ggcode-image-test.png" {
		t.Fatalf("unexpected persisted filename %q", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%q) error = %v", path, err)
	}
	if string(data) != string(img.Data) {
		t.Fatal("expected persisted image data to match source bytes")
	}
}
