package image

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"testing"
)

func createTestPNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(img, img.Bounds(), &image.Uniform{color.RGBA{255, 0, 0, 255}}, image.Point{}, draw.Src)

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func createTestJPEG(t *testing.T) []byte {
	// Minimal JPEG: FF D8 FF E0 (JFIF header start)
	// We'll use a real JPEG encoding
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 10, 10))
	draw.Draw(img, img.Bounds(), &image.Uniform{color.RGBA{0, 128, 255, 255}}, image.Point{}, draw.Src)

	var buf bytes.Buffer
	// Can't easily encode JPEG without importing image/jpeg encoder
	// So just test PNG and GIF, and test DetectMIME with manual bytes
	return buf.Bytes()
}

func TestDetectMIME(t *testing.T) {
	tests := []struct {
		name string
		data []byte
		want string
	}{
		{"png", createTestPNG(t, 10, 10), MIMEPNG},
		{"empty", []byte{}, ""},
		{"too short", []byte{0x89, 0x50}, ""},
		{"jpeg magic", []byte{0xFF, 0xD8, 0xFF, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}, MIMEJPEG},
		{"gif magic", []byte{0x47, 0x49, 0x46, 0x38, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}, MIMEGIF},
		{"webp magic", []byte{0x52, 0x49, 0x46, 0x46, 0x00, 0x00, 0x00, 0x00, 0x57, 0x45, 0x42, 0x50}, MIMEWEBP},
		{"random", []byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DetectMIME(tt.data)
			if got != tt.want {
				t.Errorf("DetectMIME() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDecode(t *testing.T) {
	pngData := createTestPNG(t, 100, 50)

	img, err := Decode(pngData)
	if err != nil {
		t.Fatalf("Decode PNG failed: %v", err)
	}
	if img.MIME != MIMEPNG {
		t.Errorf("MIME = %q, want %q", img.MIME, MIMEPNG)
	}
	if img.Width != 100 {
		t.Errorf("Width = %d, want 100", img.Width)
	}
	if img.Height != 50 {
		t.Errorf("Height = %d, want 50", img.Height)
	}
}

func TestDecodeTooLarge(t *testing.T) {
	bigData := make([]byte, MaxSize+1)
	bigData[0] = 0x89 // PNG magic start
	bigData[1] = 0x50

	_, err := Decode(bigData)
	if err == nil {
		t.Fatal("expected error for too-large image")
	}
}

func TestDecodeUnsupported(t *testing.T) {
	data := []byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}
	_, err := Decode(data)
	if err == nil {
		t.Fatal("expected error for unsupported format")
	}
}

func TestEncodeBase64(t *testing.T) {
	data := []byte("hello")
	img := Image{Data: data, MIME: "image/png"}
	encoded := EncodeBase64(img)

	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("base64 decode failed: %v", err)
	}
	if !bytes.Equal(decoded, data) {
		t.Errorf("round-trip failed")
	}
}

func TestDataURI(t *testing.T) {
	img := Image{Data: []byte("test"), MIME: "image/png"}
	uri := DataURI(img)
	expected := "data:image/png;base64," + base64.StdEncoding.EncodeToString([]byte("test"))
	if uri != expected {
		t.Errorf("DataURI = %q, want %q", uri, expected)
	}
}

func TestPlaceholder(t *testing.T) {
	img := Image{Data: make([]byte, 1024*1024*2), MIME: "image/png", Width: 1920, Height: 1080}
	got := Placeholder("screenshot.png", img)
	want := "[Image: screenshot.png, 1920x1080, 2.0MB]"
	if got != want {
		t.Errorf("Placeholder = %q, want %q", got, want)
	}
}

func TestPlaceholderNoDimensions(t *testing.T) {
	img := Image{Data: make([]byte, 500), MIME: "image/webp"}
	got := Placeholder("photo.webp", img)
	want := "[Image: photo.webp, .webp, 500B]"
	if got != want {
		t.Errorf("Placeholder = %q, want %q", got, want)
	}
}

func TestIsImageFile(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"test.png", true},
		{"photo.jpg", true},
		{"photo.jpeg", true},
		{"anim.gif", true},
		{"pic.webp", true},
		{"doc.txt", false},
		{"archive.zip", false},
		{"PNG", false},     // case sensitive extension
		{"test.PNG", true}, // lowercase check
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := IsImageFile(tt.path)
			if got != tt.want {
				t.Errorf("IsImageFile(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}
