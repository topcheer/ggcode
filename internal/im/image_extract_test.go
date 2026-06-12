package im

import (
	"strings"
	"testing"
)

func TestExtractImagesFromText_Markdown(t *testing.T) {
	text := "See this image: ![alt text](https://example.com/img.png) and some text."
	images, remaining := ExtractImagesFromText(text)
	if len(images) != 1 {
		t.Fatalf("expected 1 image, got %d", len(images))
	}
	if images[0].Kind != "url" {
		t.Fatalf("expected kind url, got %s", images[0].Kind)
	}
	if images[0].Data != "https://example.com/img.png" {
		t.Fatalf("unexpected data: %s", images[0].Data)
	}
	if strings.Contains(remaining, "img.png") {
		t.Fatalf("expected image removed from text, got %q", remaining)
	}
	if !strings.Contains(remaining, "some text") {
		t.Fatalf("expected remaining text preserved, got %q", remaining)
	}
}

func TestExtractImagesFromText_BareURL(t *testing.T) {
	text := "Check this out https://example.com/photo.jpg nice pic"
	images, remaining := ExtractImagesFromText(text)
	if len(images) != 1 {
		t.Fatalf("expected 1 image, got %d", len(images))
	}
	if images[0].Kind != "url" {
		t.Fatalf("expected kind url, got %s", images[0].Kind)
	}
	if images[0].Data != "https://example.com/photo.jpg" {
		t.Fatalf("unexpected data: %s", images[0].Data)
	}
	if strings.Contains(remaining, "photo.jpg") {
		t.Fatalf("expected URL removed from text, got %q", remaining)
	}
}

func TestExtractImagesFromText_DataURL(t *testing.T) {
	text := "Here is data:image/png;base64,iVBORw0KGgo= end"
	images, remaining := ExtractImagesFromText(text)
	if len(images) != 1 {
		t.Fatalf("expected 1 image, got %d", len(images))
	}
	if images[0].Kind != "data_url" {
		t.Fatalf("expected kind data_url, got %s", images[0].Kind)
	}
	if !strings.Contains(remaining, "end") {
		t.Fatalf("expected remaining text preserved, got %q", remaining)
	}
}

func TestExtractImagesFromText_Mixed(t *testing.T) {
	text := "![md](https://a.com/a.png) and https://b.com/b.jpg plus data:image/gif;base64,AAA= done"
	images, remaining := ExtractImagesFromText(text)
	if len(images) != 3 {
		t.Fatalf("expected 3 images, got %d: %+v", len(images), images)
	}
	if remaining == "" {
		t.Fatal("expected some remaining text")
	}
	for _, img := range images {
		if img.Data == "" {
			t.Fatal("expected non-empty data")
		}
	}
}

func TestExtractImagesFromText_NoDuplicates(t *testing.T) {
	text := "![a](https://example.com/img.png) and again ![b](https://example.com/img.png)"
	images, _ := ExtractImagesFromText(text)
	if len(images) != 1 {
		t.Fatalf("expected 1 image (dedup), got %d", len(images))
	}
}

func TestExtractImagesFromText_NoImages(t *testing.T) {
	text := "Just plain text without any images."
	images, remaining := ExtractImagesFromText(text)
	if len(images) != 0 {
		t.Fatalf("expected 0 images, got %d", len(images))
	}
	if remaining != text {
		t.Fatalf("expected text unchanged, got %q", remaining)
	}
}

func TestExtractImagesFromText_Empty(t *testing.T) {
	images, remaining := ExtractImagesFromText("")
	if len(images) != 0 {
		t.Fatalf("expected 0 images, got %d", len(images))
	}
	if remaining != "" {
		t.Fatalf("expected empty, got %q", remaining)
	}
}

func TestIsLocalFilePath(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"/tmp/img.png", true},
		{"./img.png", true},
		{"../img.png", true},
		{"photo.jpg", true},
		{"image.jpeg", true},
		{"pic.gif", true},
		{"pic.webp", true},
		{"https://example.com/img.png", false},
		{"http://example.com/img.png", false},
		{"", false},
		{"noimage.txt", false},
		{"noext", false},
	}
	for _, tt := range tests {
		got := IsLocalFilePath(tt.input)
		if got != tt.want {
			t.Errorf("IsLocalFilePath(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}
