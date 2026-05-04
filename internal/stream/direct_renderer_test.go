package stream

import (
	"image"
	"image/color"
	"testing"
)

func TestDirectRendererOutputSize(t *testing.T) {
	r := NewDirectRenderer(640, 480, 14, "", 0, 0)
	img, err := r.Render("Hello World")
	if err != nil {
		t.Fatalf("Render error: %v", err)
	}
	if img.Rect.Dx() != 640 || img.Rect.Dy() != 480 {
		t.Errorf("size = %dx%d, want 640x480", img.Rect.Dx(), img.Rect.Dy())
	}
}

func TestDirectRendererChinese(t *testing.T) {
	r := NewDirectRenderer(640, 480, 14, "", 0, 0)
	img, err := r.Render("你好世界 Hello World 测试")
	if err != nil {
		t.Fatalf("Render CJK error: %v", err)
	}
	if img.Rect.Dx() != 640 || img.Rect.Dy() != 480 {
		t.Errorf("size = %dx%d, want 640x480", img.Rect.Dx(), img.Rect.Dy())
	}
	// Verify non-zero pixels (content was rendered)
	if !hasNonZeroPixels(img) {
		t.Error("rendered image is all black/zero")
	}
}

func TestDirectRendererEmptyInput(t *testing.T) {
	r := NewDirectRenderer(640, 480, 14, "", 0, 0)
	img, err := r.Render("")
	if err != nil {
		t.Fatalf("Render empty error: %v", err)
	}
	if img.Rect.Dx() != 640 || img.Rect.Dy() != 480 {
		t.Errorf("size = %dx%d, want 640x480", img.Rect.Dx(), img.Rect.Dy())
	}
}

func TestDirectRendererMultiLine(t *testing.T) {
	r := NewDirectRenderer(640, 480, 14, "", 0, 0)
	text := "Line 1\nLine 2\nLine 3\nLine 4"
	img, err := r.Render(text)
	if err != nil {
		t.Fatalf("Render error: %v", err)
	}
	if !hasNonZeroPixels(img) {
		t.Error("multi-line render produced no content")
	}
}

func TestDirectRendererFontInfo(t *testing.T) {
	r := NewDirectRenderer(640, 480, 14, "", 0, 0)
	info := r.FontInfo()
	t.Logf("FontInfo: %s", info)
}

func TestDirectRendererGridSize(t *testing.T) {
	r := NewDirectRenderer(640, 480, 14, "", 0, 0)
	// Force font load
	_, _ = r.Render("x")

	cols, rows := r.Cols(), r.Rows()
	t.Logf("Grid: %dx%d (char=%dx%d)", cols, rows, r.charWidth, r.charHeight)
	if cols <= 0 || rows <= 0 {
		t.Errorf("invalid grid: %dx%d", cols, rows)
	}
}

func TestDirectRendererPixelData(t *testing.T) {
	r := NewDirectRenderer(100, 50, 14, "", 0, 0)
	img, err := r.Render("AB")
	if err != nil {
		t.Fatalf("Render error: %v", err)
	}
	// Verify it returns *image.RGBA with correct stride
	if img.Stride != 100*4 {
		t.Errorf("stride = %d, want %d", img.Stride, 100*4)
	}
	if len(img.Pix) != 100*50*4 {
		t.Errorf("pix len = %d, want %d", len(img.Pix), 100*50*4)
	}
}

func TestDirectRendererNoFontPath(t *testing.T) {
	// This should still work (FindCJKFont will search system fonts)
	r := NewDirectRenderer(100, 50, 14, "", 0, 0)
	img, err := r.Render("test")
	if err != nil {
		// May fail on systems without fonts — acceptable
		t.Logf("Render without font path: %v (may be expected)", err)
		return
	}
	if img == nil {
		t.Error("expected non-nil image")
	}
}

// Benchmark: DirectRenderer vs old Renderer
func BenchmarkDirectRendererRender(b *testing.B) {
	r := NewDirectRenderer(640, 480, 14, "", 0, 0)
	text := "Hello World 测试中文 Line 2 content"
	// Warm up
	_, _ = r.Render(text)

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _ = r.Render(text)
	}
}

func BenchmarkDirectRendererLargeText(b *testing.B) {
	r := NewDirectRenderer(1280, 720, 14, "", 0, 0)
	// Simulate a full terminal screen
	lines := make([]string, 40)
	for i := range lines {
		lines[i] = "This is a line of terminal content with some 中文字符 mixed in"
	}
	text := ""
	for i, l := range lines {
		if i > 0 {
			text += "\n"
		}
		text += l
	}
	_, _ = r.Render(text)

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _ = r.Render(text)
	}
}

func hasNonZeroPixels(img *image.RGBA) bool {
	for i := 0; i < len(img.Pix); i += 4 {
		if img.Pix[i] > 0 || img.Pix[i+1] > 0 || img.Pix[i+2] > 0 {
			return true
		}
	}
	return false
}

// Ensure color import is used
var _ color.NRGBA
