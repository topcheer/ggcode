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
	imgs := []imageAttachedMsg{{
		img:        image.Image{Data: []byte{0x89, 0x50, 0x4E, 0x47}, MIME: image.MIMEPNG, Width: 10, Height: 10},
		filename:   "ggcode-image-deadbeef.png",
		sourcePath: "/tmp/ggcode-image-deadbeef.png",
	}}

	content := buildAgentSubmissionContent("帮我看看", imgs, false)
	if len(content) != 1 {
		t.Fatalf("expected text-only fallback content, got %d blocks", len(content))
	}
	if !strings.Contains(content[0].Text, "/tmp/ggcode-image-deadbeef.png") {
		t.Fatalf("expected prompt to include local image path, got %q", content[0].Text)
	}
}

func TestBuildAgentSubmissionContentAddsImageBlockWhenEnabled(t *testing.T) {
	imgs := []imageAttachedMsg{{
		img:        image.Image{Data: []byte{0x89, 0x50, 0x4E, 0x47}, MIME: image.MIMEPNG, Width: 10, Height: 10},
		filename:   "ggcode-image-deadbeef.png",
		sourcePath: "/tmp/ggcode-image-deadbeef.png",
	}}

	content := buildAgentSubmissionContent("帮我看看", imgs, true)
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

func TestBuildAgentSubmissionContentMultipleImages(t *testing.T) {
	imgs := []imageAttachedMsg{
		{img: image.Image{Data: []byte{0x89}, MIME: image.MIMEPNG, Width: 10, Height: 10}, filename: "a.png", sourcePath: "/tmp/a.png"},
		{img: image.Image{Data: []byte{0x89}, MIME: image.MIMEPNG, Width: 20, Height: 20}, filename: "b.png", sourcePath: "/tmp/b.png"},
	}

	content := buildAgentSubmissionContent("compare these", imgs, true)
	// Should be: 1 text block + 2 image blocks
	if len(content) != 3 {
		t.Fatalf("expected 3 blocks (text + 2 images), got %d", len(content))
	}
	if content[1].Type != "image" || content[2].Type != "image" {
		t.Fatalf("expected blocks 2,3 to be image type, got %q %q", content[1].Type, content[2].Type)
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
