package im

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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

func TestTGSendPhotoUploadRejectsOversized(t *testing.T) {
	a := &tgAdapter{}
	big := make([]byte, 10<<20+1)
	err := a.sendPhotoByUpload(context.Background(), "12345", big, "big.png", "", "")
	if err == nil || !strings.Contains(err.Error(), "Telegram sendPhoto limit") {
		t.Fatalf("expected oversize error, got %v", err)
	}
}

func TestFeishuUploadImageRejectsOversized(t *testing.T) {
	a := &feishuAdapter{}
	big := make([]byte, 10<<20+1)
	_, err := a.uploadImage(context.Background(), big, "big.png")
	if err == nil || !strings.Contains(err.Error(), "Feishu image upload limit") {
		t.Fatalf("expected oversize error, got %v", err)
	}
}
