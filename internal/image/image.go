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

const MaxSize = 20 * 1024 * 1024

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

var (
	MIMEJPEG = "image/jpeg"
	MIMEPNG  = "image/png"
	MIMEGIF  = "image/gif"
	MIMEWEBP = "image/webp"
)

type Image struct {
	Data   []byte
	MIME   string
	Width  int
	Height int
}

func DetectMIME(data []byte) string {
	if len(data) < 12 {
		return ""
	}

	if bytes.HasPrefix(data, []byte{0x89, 0x50, 0x4E, 0x47}) {
		return MIMEPNG
	}

	if bytes.HasPrefix(data, []byte{0xFF, 0xD8, 0xFF}) {
		return MIMEJPEG
	}

	if bytes.HasPrefix(data, []byte{0x47, 0x49, 0x46, 0x38}) {
		return MIMEGIF
	}

	if bytes.HasPrefix(data, []byte{0x52, 0x49, 0x46, 0x46}) && len(data) >= 12 &&
		bytes.Equal(data[8:12], []byte{0x57, 0x45, 0x42, 0x50}) {
		return MIMEWEBP
	}
	return ""
}

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

		if mime == MIMEWEBP {
			return Image{
				Data: data,
				MIME: mime,
			}, nil
		}
		return Image{}, fmt.Errorf("failed to decode image: %w", err)
	}

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

func EncodeBase64(img Image) string {
	return base64.StdEncoding.EncodeToString(img.Data)
}

func DataURI(img Image) string {
	return fmt.Sprintf("data:%s;base64,%s", img.MIME, EncodeBase64(img))
}

func ReadFile(path string) (Image, error) {

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

func IsImageFile(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp":
		return true
	}
	return false
}

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

func displayScreenIndex(display int) (index int, ok bool) {
	if display > 1 {
		return display - 1, true
	}
	return 0, false
}
