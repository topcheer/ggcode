package im

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 1x1 transparent PNG used to exercise local-file image resolution.
const onePixelPNGB64 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg=="

func writeTempPNG(t *testing.T) string {
	t.Helper()
	data, err := base64.StdEncoding.DecodeString(onePixelPNGB64)
	if err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	p := filepath.Join(t.TempDir(), "img.png")
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatalf("write temp png: %v", err)
	}
	return p
}

// #1016: ExtractImagesFromText emits bare local image paths with Kind "url".
// The url branch must route local paths to file reads instead of HTTP GET,
// which would fail with "unsupported protocol scheme".
func TestMatrixResolveImageToBytesLocalPath(t *testing.T) {
	p := writeTempPNG(t)
	a := &matrixAdapter{}
	data, mime, err := a.resolveImageToBytes(context.Background(), ExtractedImage{Kind: "url", Data: p})
	if err != nil {
		t.Fatalf("resolve local path: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("expected non-empty image data")
	}
	if !strings.HasPrefix(mime, "image/") {
		t.Fatalf("expected image mime, got %q", mime)
	}
}

func TestMattermostResolveImageToBytesLocalPath(t *testing.T) {
	p := writeTempPNG(t)
	a := &mattermostAdapter{}
	data, mime, err := a.resolveImageToBytes(context.Background(), ExtractedImage{Kind: "url", Data: p})
	if err != nil {
		t.Fatalf("resolve local path: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("expected non-empty image data")
	}
	if !strings.HasPrefix(mime, "image/") {
		t.Fatalf("expected image mime, got %q", mime)
	}
}

// Telegram caps multipart photos at 10MB; the extraction layer allows 20MB,
// so the adapter must reject oversized photos locally before uploading.
func TestTGSendPhotoUploadRejectsOversized(t *testing.T) {
	a := &tgAdapter{}
	big := make([]byte, 10<<20+1)
	err := a.sendPhotoByUpload(context.Background(), "12345", big, "big.png", "", "")
	if err == nil || !strings.Contains(err.Error(), "Telegram sendPhoto limit") {
		t.Fatalf("expected oversize error, got %v", err)
	}
}

// Feishu im/v1/images caps message images at 10MB; reject locally before upload.
func TestFeishuUploadImageRejectsOversized(t *testing.T) {
	a := &feishuAdapter{}
	big := make([]byte, 10<<20+1)
	_, err := a.uploadImage(context.Background(), big, "big.png")
	if err == nil || !strings.Contains(err.Error(), "Feishu image upload limit") {
		t.Fatalf("expected oversize error, got %v", err)
	}
}

// Note: exactly-10MB boundary (len == limit passes the guard) is intentionally
// not exercised here - proceeding past the precheck requires a live HTTP
// client on the adapter, which a zero-value test instance lacks.
