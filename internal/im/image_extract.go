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

	localImagePathRe = regexp.MustCompile(`(?i)(?:(?:/[\w.@\-]+)+|\.{1,2}/(?:[\w.@\-]+/)*[\w.@\-]*|[A-Za-z]:[\\/](?:[\w.@\-]+[\\/])*[\w.@\-]*)\.(?:png|jpe?g|gif|webp)\b`)

	imageDownloadClient = &http.Client{Timeout: 60 * time.Second}
)

type ExtractedImage struct {
	Kind string
	Data string
}

func appendImage(images []ExtractedImage, seen map[string]bool, kind, data string) ([]ExtractedImage, bool) {
	data = strings.TrimSpace(data)
	if data == "" || seen[data] {
		return images, false
	}
	seen[data] = true
	return append(images, ExtractedImage{Kind: kind, Data: data}), true
}

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
