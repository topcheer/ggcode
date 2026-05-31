//go:build !integration

package tunnel

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/skip2/go-qrcode"
)

func TestQRCodeForURL(t *testing.T) {
	s, err := QRCodeForURL("https://example.com")
	if err != nil {
		t.Fatal(err)
	}
	if s == "" {
		t.Error("QR code string should not be empty")
	}
	if !strings.Contains(s, "\n") {
		t.Error("QR code should be multi-line")
	}
}

func TestQRCodeLines(t *testing.T) {
	lines, err := QRCodeLines("https://example.com/connect")
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) == 0 {
		t.Error("should return at least one line")
	}
	for i, line := range lines {
		if line == "" && i < len(lines)-1 {
			t.Errorf("line %d is unexpectedly empty", i)
		}
	}
}

func TestQRCodePNG(t *testing.T) {
	png, err := QRCodePNG("https://example.com")
	if err != nil {
		t.Fatal(err)
	}
	if len(png) == 0 {
		t.Error("PNG bytes should not be empty")
	}
	// PNG magic bytes
	if len(png) < 8 || string(png[:4]) != "\x89PNG" {
		t.Error("output should be valid PNG data")
	}
}

func TestQRCodeForURLLong(t *testing.T) {
	longURL := "https://example.com/ws?role=client&token=a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6"
	s, err := QRCodeForURL(longURL)
	if err != nil {
		t.Fatal(err)
	}
	if s == "" {
		t.Error("QR code should handle long URLs")
	}
}

func TestQRCodeForURLMinimalData(t *testing.T) {
	// Test with a minimal but valid URL
	s, err := QRCodeForURL("a")
	if err != nil {
		t.Fatal(err)
	}
	if s == "" {
		t.Error("QR code should handle minimal data")
	}
}

func TestQRCodeForURLUsesCompactTerminalRendering(t *testing.T) {
	url := "https://example.com/compact"
	compact, err := QRCodeForURL(url)
	if err != nil {
		t.Fatal(err)
	}

	defaultQR, err := qrcode.New(url, qrcode.Low)
	if err != nil {
		t.Fatal(err)
	}
	defaultRendered := strings.TrimRight(defaultQR.ToSmallString(false), "\n")
	borderlessQR, err := newTerminalQRCode(url)
	if err != nil {
		t.Fatal(err)
	}
	bitmap := padTerminalQRBitmap(borderlessQR.Bitmap(), terminalQRQuietZoneModules)

	compactLines := strings.Split(compact, "\n")
	defaultLines := strings.Split(defaultRendered, "\n")
	if len(compactLines) >= len(defaultLines) {
		t.Fatalf("expected compact QR to use fewer lines, got %d >= %d", len(compactLines), len(defaultLines))
	}
	if len(compactLines[0]) >= len(defaultLines[0]) {
		t.Fatalf("expected compact QR to use fewer columns, got %d >= %d", len(compactLines[0]), len(defaultLines[0]))
	}
	expectedHeight := (len(bitmap) + 1) / 2
	if len(compactLines) != expectedHeight {
		t.Fatalf("expected compact QR height %d, got %d", expectedHeight, len(compactLines))
	}
	if gotWidth, wantWidth := utf8.RuneCountInString(compactLines[0]), len(bitmap[0]); gotWidth != wantWidth {
		t.Fatalf("expected compact QR width %d, got %d", wantWidth, gotWidth)
	}
}

func TestQRCodePNGConsistency(t *testing.T) {
	// Two calls with same URL should produce same PNG
	url := "https://example.com/test"
	p1, err := QRCodePNG(url)
	if err != nil {
		t.Fatal(err)
	}
	p2, err := QRCodePNG(url)
	if err != nil {
		t.Fatal(err)
	}
	if len(p1) != len(p2) {
		t.Errorf("PNG sizes differ: %d vs %d", len(p1), len(p2))
	}
}
