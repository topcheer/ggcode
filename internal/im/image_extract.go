package im

import (
	"net/http"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var (
	markdownImageRe = regexp.MustCompile(`(?i)!\[([^\]]*)\]\(([^)]+)\)`)
	bareImageURLRe  = regexp.MustCompile(`(?i)(?:^|[\s(])(https?://[^\s)"'<>?#]+\.(?:png|jpe?g|gif|webp)(?:\?[^\s"'<>]*)?)`)
	dataURLRe       = regexp.MustCompile(`(?i)(data:image/(?:png|jpe?g|gif|webp);base64,[A-Za-z0-9+/=]+)`)

	// localImagePathRe matches bare local image file paths in prose:
	// Unix absolute (/tmp/shot.png, requires at least one directory level),
	// relative (./shot.png, ../out/shot.png), and Windows (C:\Users\x\shot.png).
	// Extracted as Kind "url": every adapter's sendExtractedImage already
	// routes IsLocalFilePath payloads to local-file upload, so no per-adapter
	// changes are needed. Bare names without a directory component
	// ("shot.png") are deliberately NOT matched - too easy to be prose
	// mentions rather than real paths (false-positive guard).
	localImagePathRe = regexp.MustCompile(`(?i)(?:(?:/[\w.@\-]+)+|\.{1,2}/(?:[\w.@\-]+/)*[\w.@\-]*|[A-Za-z]:[\\/](?:[\w.@\-]+[\\/])*[\w.@\-]*)\.(?:png|jpe?g|gif|webp)\b`)

	// imageDownloadClient is used by adapters to download images from URLs.
	// It has an explicit 60s timeout to prevent slow servers from blocking
	// the send path indefinitely. Context cancellation provides a secondary
	// timeout via the 30s defaultSendTimeout.
	imageDownloadClient = &http.Client{Timeout: 60 * time.Second}
)

// ExtractedImage represents an image found in message text.
type ExtractedImage struct {
	Kind string // "url", "data_url", "local_path"
	Data string // URL, base64 data URL, or local file path
}

// appendImage trims, dedupes, and appends an extracted image. Returns
// false when the payload is empty or already seen.
func appendImage(images []ExtractedImage, seen map[string]bool, kind, data string) ([]ExtractedImage, bool) {
	data = strings.TrimSpace(data)
	if data == "" || seen[data] {
		return images, false
	}
	seen[data] = true
	return append(images, ExtractedImage{Kind: kind, Data: data}), true
}

// ExtractImagesFromText finds markdown images, bare image URLs, and data URLs in text.
// Returns extracted images and the text with image references replaced by their alt text
// (for markdown images) or removed (for bare URLs and data URLs). Line breaks are preserved.
func ExtractImagesFromText(text string) ([]ExtractedImage, string) {
	var images []ExtractedImage
	seen := make(map[string]bool)

	markdownMatches := markdownImageRe.FindAllStringSubmatch(text, -1)
	for _, m := range markdownMatches {
		if len(m) < 3 {
			continue
		}
		imgURL := strings.TrimSpace(m[2])
		kind := "url"
		if strings.HasPrefix(imgURL, "data:image/") {
			kind = "data_url"
		}
		images, _ = appendImage(images, seen, kind, imgURL)
	}

	text = markdownImageRe.ReplaceAllString(text, "$1")

	urlMatches := bareImageURLRe.FindAllStringSubmatch(text, -1)
	for _, m := range urlMatches {
		if len(m) < 2 {
			continue
		}
		url := strings.TrimSpace(m[1])
		images, _ = appendImage(images, seen, "url", url)
	}

	text = bareImageURLRe.ReplaceAllString(text, " ")

	dataMatches := dataURLRe.FindAllStringSubmatch(text, -1)
	for _, m := range dataMatches {
		if len(m) < 2 {
			continue
		}
		dataURL := m[1]
		images, _ = appendImage(images, seen, "data_url", dataURL)
	}
	text = dataURLRe.ReplaceAllString(text, "")

	localMatches := localImagePathRe.FindAllString(text, -1)
	for _, p := range localMatches {
		if IsLocalFilePath(p) {
			images, _ = appendImage(images, seen, "url", p)
		}
	}

	text = localImagePathRe.ReplaceAllString(text, " ")

	text = strings.TrimSpace(text)

	return images, text
}

// IsLocalFilePath checks if a string looks like a local file path.
func IsLocalFilePath(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}

	if strings.HasPrefix(s, "/") {
		return true
	}

	if strings.HasPrefix(s, "./") || strings.HasPrefix(s, "../") {
		return true
	}

	if strings.Contains(s, "://") {
		return false
	}
	ext := strings.ToLower(filepath.Ext(s))
	switch ext {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp":
		return true
	}
	return false
}
