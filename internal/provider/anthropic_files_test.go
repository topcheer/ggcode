package provider

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"strings"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
)

var testTinyPNG = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg=="

func TestFilesAPIAllowed(t *testing.T) {
	cases := []struct {
		baseURL string
		want    bool
	}{
		{"", true},
		{"https://api.anthropic.com", true},
		{"https://api.anthropic.com/", true},
		{"https://proxy.example.com/v1", false},
		{"https://api.anthropic.com.evil.io", false},
	}
	for _, tc := range cases {
		if got := filesAPIAllowed(tc.baseURL); got != tc.want {
			t.Errorf("filesAPIAllowed(%q) = %v, want %v", tc.baseURL, got, tc.want)
		}
	}
}

func TestFilesResolveCachesByID(t *testing.T) {
	u := newFileUploader(&anthropic.Client{}, "")
	data, _ := base64.StdEncoding.DecodeString(testTinyPNG)

	id, ok := u.resolve(context.Background(), "image/png", data)
	if ok {
		t.Fatalf("expected fallback (ok=false) when upload unavailable, got file_id %q", id)
	}

	sum := sha256.Sum256(data)
	key := hex.EncodeToString(sum[:])
	u.mu.Lock()
	u.cache[key] = filesCacheEntry{fileID: "file_abc123", uploadedAt: time.Now()}
	u.mu.Unlock()

	id, ok = u.resolve(context.Background(), "image/png", data)
	if !ok || id != "file_abc123" {
		t.Fatalf("cache hit: got (%q, %v), want (file_abc123, true)", id, ok)
	}
}

func TestFilesResolveNegativeCache(t *testing.T) {
	u := newFileUploader(&anthropic.Client{}, "")
	data, _ := base64.StdEncoding.DecodeString(testTinyPNG)

	if _, ok := u.resolve(context.Background(), "image/png", data); ok {
		t.Fatal("expected first resolve to fail")
	}
	u.mu.Lock()
	n := len(u.failed)
	u.mu.Unlock()
	if n != 1 {
		t.Fatalf("negative-cache entries = %d, want 1", n)
	}

	if _, ok := u.resolve(context.Background(), "image/png", data); ok {
		t.Fatal("expected negative cache to short-circuit retry")
	}
	u.mu.Lock()
	n = len(u.failed)
	u.mu.Unlock()
	if n != 1 {
		t.Fatalf("negative-cache entries = %d after short-circuit, want 1", n)
	}
}

func TestFilesImageSourceThreshold(t *testing.T) {
	p := &AnthropicProvider{files: newFileUploader(&anthropic.Client{}, "")}
	small := testTinyPNG
	if src, ok := p.filesImageSource(context.Background(), "image/png", small); ok {
		t.Fatalf("below-threshold image should stay base64, got %v", src)
	}

	big := strings.Repeat("A", filesUploadThresholdBytes+16)
	if _, ok := p.filesImageSource(context.Background(), "image/png", big); ok {
		t.Fatal("upload failure should fall back, not return file source")
	}
}

func TestImageContentBlockFallback(t *testing.T) {
	p := &AnthropicProvider{}
	blk := p.imageContentBlock(context.Background(), "image/png", testTinyPNG)
	if blk.OfImage == nil || blk.OfImage.Source.OfBase64 == nil {
		t.Fatal("expected base64 fallback block when uploader is unavailable")
	}
}

func TestFilesExtForMIME(t *testing.T) {
	cases := map[string]string{
		"image/png":  ".png",
		"image/jpeg": ".jpg",
		"image/gif":  ".gif",
		"image/webp": ".webp",
		"image/x":    ".png",
	}
	for mime, want := range cases {
		if got := filesExtForMIME(mime); got != want {
			t.Errorf("filesExtForMIME(%q) = %q, want %q", mime, got, want)
		}
	}
}
