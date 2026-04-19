package im

import (
	"path/filepath"
	"regexp"
	"strings"
)

var (
	markdownImageRe = regexp.MustCompile(`(?i)!\[([^\]]*)\]\(([^)]+)\)`)
	bareImageURLRe  = regexp.MustCompile(`(?i)(?:^|[\s(])(https?://[^\s)"'<>]+\.(?:png|jpe?g|gif|webp)(?:\?[^\s"'<>]*)?)`)
	dataURLRe       = regexp.MustCompile(`(?i)(data:image/(?:png|jpe?g|gif|webp);base64,[A-Za-z0-9+/=]+)`)
)

// ExtractedImage represents an image found in message text.
type ExtractedImage struct {
	Kind string // "url", "data_url", "local_path"
	Data string // URL, base64 data URL, or local file path
}

// ExtractImagesFromText finds markdown images, bare image URLs, and data URLs in text.
// Returns extracted images and the text with image references replaced by their alt text
// (for markdown images) or removed (for bare URLs and data URLs). Line breaks are preserved.
func ExtractImagesFromText(text string) ([]ExtractedImage, string) {
	var images []ExtractedImage
	seen := make(map[string]bool)

	// 1. Extract markdown images: ![alt](url)
	markdownMatches := markdownImageRe.FindAllStringSubmatch(text, -1)
	for _, m := range markdownMatches {
		if len(m) < 3 {
			continue
		}
		imgURL := strings.TrimSpace(m[2])
		if imgURL == "" || seen[imgURL] {
			continue
		}
		seen[imgURL] = true
		kind := "url"
		if strings.HasPrefix(imgURL, "data:image/") {
			kind = "data_url"
		}
		images = append(images, ExtractedImage{Kind: kind, Data: imgURL})
	}
	// Replace markdown images with just the alt text (preserve meaningful content)
	text = markdownImageRe.ReplaceAllString(text, "$1")

	// 2. Extract bare image URLs
	urlMatches := bareImageURLRe.FindAllStringSubmatch(text, -1)
	for _, m := range urlMatches {
		if len(m) < 2 {
			continue
		}
		imgURL := strings.TrimSpace(m[1])
		if imgURL == "" || seen[imgURL] {
			continue
		}
		seen[imgURL] = true
		images = append(images, ExtractedImage{Kind: "url", Data: imgURL})
	}
	// Remove matched bare URLs from text
	text = bareImageURLRe.ReplaceAllString(text, " ")

	// 3. Extract data URLs (base64 images not in markdown)
	dataMatches := dataURLRe.FindAllStringSubmatch(text, -1)
	for _, m := range dataMatches {
		if len(m) < 2 {
			continue
		}
		dataURL := strings.TrimSpace(m[1])
		if dataURL == "" || seen[dataURL] {
			continue
		}
		seen[dataURL] = true
		images = append(images, ExtractedImage{Kind: "data_url", Data: dataURL})
	}
	text = dataURLRe.ReplaceAllString(text, "")

	text = strings.TrimSpace(text)

	return images, text
}

// IsLocalFilePath checks if a string looks like a local file path.
func IsLocalFilePath(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	// Absolute paths
	if strings.HasPrefix(s, "/") {
		return true
	}
	// Relative paths with common prefixes
	if strings.HasPrefix(s, "./") || strings.HasPrefix(s, "../") {
		return true
	}
	// Check if it has a file extension and no URL scheme
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
