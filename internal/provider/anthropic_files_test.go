package provider

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
)

// 1x1 PNG (67 bytes) below the upload threshold; a padded variant is used
// for above-threshold cases.
var testTinyPNG = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg=="

func TestFilesAPIAllowed(t *testing.T) {
	cases := []struct {
		baseURL string
		want    bool
	}{
		{"", true},                                   // default official API
		{"https://api.anthropic.com", true},          // official
		{"https://api.anthropic.com/", true},         // official with slash
		{"https://proxy.example.com/v1", false},      // third-party gateway
		{"https://api.anthropic.com.evil.io", false}, // lookalike domain
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

	// Cache miss without network access: upload would hit the API; with a
	// zero-value client this fails, and the uploader must fall back.
	id, ok := u.resolve(context.Background(), "image/png", data)
	if ok {
		t.Fatalf("expected fallback (ok=false) when upload unavailable, got file_id %q", id)
	}

	// Prime the cache the way a successful upload would and verify the
	// hit path returns the same file_id without any network activity.
	sum := sha256.Sum256(data)
	key := hex.EncodeToString(sum[:])
	u.mu.Lock()
	u.cache[key] = "file_abc123"
	u.mu.Unlock()

	id, ok = u.resolve(context.Background(), "image/png", data)
	if !ok || id != "file_abc123" {
		t.Fatalf("cache hit: got (%q, %v), want (file_abc123, true)", id, ok)
	}
}

func TestFilesResolveNegativeCache(t *testing.T) {
	u := newFileUploader(&anthropic.Client{}, "")
	data, _ := base64.StdEncoding.DecodeString(testTinyPNG)

	// First attempt fails (no network in unit tests) -> negative cache.
	if _, ok := u.resolve(context.Background(), "image/png", data); ok {
		t.Fatal("expected first resolve to fail")
	}
	u.mu.Lock()
	n := len(u.failed)
	u.mu.Unlock()
	if n != 1 {
		t.Fatalf("negative-cache entries = %d, want 1", n)
	}
	// Second attempt with identical bytes must short-circuit.
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
	small := testTinyPNG // below threshold -> base64 path
	if src, ok := p.filesImageSource(context.Background(), "image/png", small); ok {
		t.Fatalf("below-threshold image should stay base64, got %v", src)
	}

	big := strings.Repeat("A", filesUploadThresholdBytes+16) // over threshold
	if _, ok := p.filesImageSource(context.Background(), "image/png", big); ok {
		t.Fatal("upload failure should fall back, not return file source")
	}
}

func TestImageContentBlockFallback(t *testing.T) {
	p := &AnthropicProvider{} // files uploader nil -> must not panic
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
		"image/x":    ".png", // default
	}
	for mime, want := range cases {
		if got := filesExtForMIME(mime); got != want {
			t.Errorf("filesExtForMIME(%q) = %q, want %q", mime, got, want)
		}
	}
}
