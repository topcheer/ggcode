package image

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// MaxSize is the maximum allowed image size (20MB).
const MaxSize = 20 * 1024 * 1024

// ReadLimited reads r fully but fails with a clear error when the data
// exceeds limit. A bare io.LimitReader silently truncates at limit — the
// truncated bytes then flow downstream as a corrupt half-image (#388).
func ReadLimited(r io.Reader, limit int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("data exceeds size limit (%d bytes)", limit)
	}
	return data, nil
}

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

	// #555: a valid header is not a valid image. DecodeConfig only parses the
	// header, so truncated/corrupt bodies (e.g. a 53-byte PNG) passed with
	// err=nil and bogus dimensions. Fully decode to verify the body before
	// handing dimensions to callers. GIF is excluded: image/gif allows multi-
	// frame streams where a later-frame decode error should not invalidate an
	// already-decodable first frame, and the standard gif decoder tolerates it.
	if mime != MIMEGIF {
		if _, _, derr := image.Decode(bytes.NewReader(data)); derr != nil {
			return Image{}, fmt.Errorf("image data corrupt or truncated: %w", derr)
		}
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
	// #438: pre-check size BEFORE reading — os.ReadFile loaded the entire
	// file into memory first, so a 2GB file OOM'd before the 20MB MaxSize
	// rejection inside Decode. Same pattern as tool/read_file's Stat guard.
	f, err := os.Open(path)
	if err != nil {
		return Image{}, fmt.Errorf("reading image file: %w", err)
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return Image{}, fmt.Errorf("reading image file: %w", err)
	}
	var data []byte
	if fi.Mode().IsRegular() {
		if fi.Size() > int64(MaxSize) {
			return Image{}, fmt.Errorf("image file %s too large: %d bytes (max %d)", path, fi.Size(), MaxSize)
		}
		data, err = io.ReadAll(f)
	} else {
		// #555: FIFOs, device files and other non-regular files report
		// Size()==0, which used to bypass the precheck above; io.ReadAll then
		// read unbounded data (the OOM #438 wanted to prevent). Read through
		// a limit+1 window so oversized special files are rejected after
		// MaxSize+1 bytes instead of being fully consumed.
		data, err = ReadLimited(f, int64(MaxSize))
		if err != nil {
			return Image{}, fmt.Errorf("image file %s too large or unreadable (max %d bytes): %w", path, MaxSize, err)
		}
	}
	if err != nil {
		return Image{}, fmt.Errorf("reading image file: %w", err)
	}
	img, err := Decode(data)
	if err != nil {
		return Image{}, err
	}
	return img, nil
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

// matchWindowQuery resolves a window title/app query to a window ID with
// exact (case-insensitive) matches strictly preferred over substring
// matches, and title preferred over app. A short query like "terminal" then
// lands on the window titled exactly "terminal" instead of an unrelated
// "terminal — Drafts" window that merely contains the text (#555).
func matchWindowQuery(windows []WindowInfo, query string) (int, error) {
	q := strings.ToLower(query)
	matchers := []func(WindowInfo) bool{
		func(w WindowInfo) bool { return strings.ToLower(w.Title) == q },
		func(w WindowInfo) bool { return strings.ToLower(w.App) == q },
		func(w WindowInfo) bool { return strings.Contains(strings.ToLower(w.Title), q) },
		func(w WindowInfo) bool { return strings.Contains(strings.ToLower(w.App), q) },
	}
	for _, m := range matchers {
		for _, w := range windows {
			if m(w) {
				return w.ID, nil
			}
		}
	}
	return 0, fmt.Errorf("no window matching %q", query)
}

// displayScreenIndex translates a 1-based display number (as in
// DisplayInfo.Index / ScreenshotOptions.Display) into a zero-based
// AllScreens/screen-array index. ok=false means "primary screen" (Display 0
// or 1); ok=true means the Nth screen in enumeration order (#555, used by the
// Windows full-display capture path).
func displayScreenIndex(display int) (index int, ok bool) {
	if display > 1 {
		return display - 1, true
	}
	return 0, false
}
