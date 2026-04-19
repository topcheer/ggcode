package image

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// MaxSize is the maximum allowed image size (20MB).
const MaxSize = 20 * 1024 * 1024

// Supported MIME types.
var (
	MIMEJPEG = "image/jpeg"
	MIMEPNG  = "image/png"
	MIMEGIF  = "image/gif"
	MIMEWEBP = "image/webp"
)

// Image represents a decoded image ready for sending to providers.
type Image struct {
	Data   []byte
	MIME   string // "image/png", "image/jpeg", etc.
	Width  int
	Height int
}

// DetectMIME detects image MIME type from magic bytes.
func DetectMIME(data []byte) string {
	if len(data) < 12 {
		return ""
	}
	// PNG: 89 50 4E 47
	if bytes.HasPrefix(data, []byte{0x89, 0x50, 0x4E, 0x47}) {
		return MIMEPNG
	}
	// JPEG: FF D8 FF
	if bytes.HasPrefix(data, []byte{0xFF, 0xD8, 0xFF}) {
		return MIMEJPEG
	}
	// GIF: 47 49 46 38
	if bytes.HasPrefix(data, []byte{0x47, 0x49, 0x46, 0x38}) {
		return MIMEGIF
	}
	// WebP: RIFF....WEBP
	if bytes.HasPrefix(data, []byte{0x52, 0x49, 0x46, 0x46}) && len(data) >= 12 &&
		bytes.Equal(data[8:12], []byte{0x57, 0x45, 0x42, 0x50}) {
		return MIMEWEBP
	}
	return ""
}

// Decode decodes raw image data, detecting MIME type and dimensions.
// Returns an error if data exceeds MaxSize or format is unsupported.
func Decode(data []byte) (Image, error) {
	if len(data) > MaxSize {
		return Image{}, fmt.Errorf("image too large: %d bytes (max %d)", len(data), MaxSize)
	}

	mime := DetectMIME(data)
	if mime == "" {
		return Image{}, fmt.Errorf("unsupported image format (magic bytes not recognized)")
	}

	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		// WebP is not supported by Go's standard image decoders
		if mime == MIMEWEBP {
			return Image{
				Data: data,
				MIME: mime,
			}, nil
		}
		return Image{}, fmt.Errorf("failed to decode image: %w", err)
	}

	return Image{
		Data:   data,
		MIME:   mime,
		Width:  cfg.Width,
		Height: cfg.Height,
	}, nil
}

// EncodeBase64 returns the base64-encoded representation of image data.
func EncodeBase64(img Image) string {
	return base64.StdEncoding.EncodeToString(img.Data)
}

// DataURI returns a data URI for the image.
func DataURI(img Image) string {
	return fmt.Sprintf("data:%s;base64,%s", img.MIME, EncodeBase64(img))
}

// ReadFile reads an image from a file path.
func ReadFile(path string) (Image, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Image{}, fmt.Errorf("reading image file: %w", err)
	}
	img, err := Decode(data)
	if err != nil {
		return Image{}, err
	}
	return img, nil
}

// ReadFromReader reads an image from an io.Reader.
func ReadFromReader(r io.Reader) (Image, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return Image{}, fmt.Errorf("reading image data: %w", err)
	}
	return Decode(data)
}

// Placeholder returns a TUI-friendly placeholder string for an image.
func Placeholder(filename string, img Image) string {
	ext := strings.ToLower(filepath.Ext(filename))
	size := len(img.Data)
	var sizeStr string
	if size >= 1024*1024 {
		sizeStr = fmt.Sprintf("%.1fMB", float64(size)/(1024*1024))
	} else if size >= 1024 {
		sizeStr = fmt.Sprintf("%.1fKB", float64(size)/1024)
	} else {
		sizeStr = fmt.Sprintf("%dB", size)
	}

	if img.Width > 0 && img.Height > 0 {
		return fmt.Sprintf("[Image: %s, %dx%d, %s]", filename, img.Width, img.Height, sizeStr)
	}
	return fmt.Sprintf("[Image: %s, %s, %s]", filename, ext, sizeStr)
}

// IsImageFile checks if a file path looks like an image based on extension.
func IsImageFile(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp":
		return true
	}
	return false
}
