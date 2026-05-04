package stream

import (
	"image"
	"image/color"
	"testing"
)

// BenchmarkImageToRGBAGeneric benchmarks the slow path (generic image.Image).
func BenchmarkImageToRGBAGeneric(b *testing.B) {
	img := image.NewRGBA(image.Rect(0, 0, 640, 480))
	for y := 0; y < 480; y++ {
		for x := 0; x < 640; x++ {
			img.SetRGBA(x, y, color.RGBA{R: 30, G: 30, B: 30, A: 255})
		}
	}
	// Wrap in a non-RGBA type to force generic path
	var generic image.Image = img

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = imageToRGBA(generic)
	}
}

// BenchmarkImageToRGBAFast benchmarks the fast path (*image.RGBA direct copy).
func BenchmarkImageToRGBAFast(b *testing.B) {
	img := image.NewRGBA(image.Rect(0, 0, 640, 480))
	for y := 0; y < 480; y++ {
		for x := 0; x < 640; x++ {
			img.SetRGBA(x, y, color.RGBA{R: 30, G: 30, B: 30, A: 255})
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = imageToRGBA(img)
	}
}

// BenchmarkResizeImage benchmarks the resize operation.
func BenchmarkResizeImage(b *testing.B) {
	src := image.NewRGBA(image.Rect(0, 0, 1300, 740))
	for y := 0; y < 740; y++ {
		for x := 0; x < 1300; x++ {
			src.SetRGBA(x, y, color.RGBA{R: 30, G: 144, B: 255, A: 255})
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = resizeImage(src, 1280, 720)
	}
}

// BenchmarkRender benchmarks the direct render pipeline.
func BenchmarkRender(b *testing.B) {
	r := NewDirectRenderer(640, 480, 14, "", 0, 0)
	text := "Hello World 测试中文\r\nLine 2: some content\r\nLine 3: more text"

	// Warm up to load fonts
	_, err := r.Render(text)
	if err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := r.Render(text)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkDirectRender benchmarks the new direct renderer.
func BenchmarkDirectRender(b *testing.B) {
	r := NewDirectRenderer(640, 480, 14, "", 0, 0)
	text := "Hello World 测试中文\r\nLine 2: some content\r\nLine 3: more text"

	// Warm up to load fonts
	_, err := r.Render(text)
	if err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := r.Render(text)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkCaptureAndEncodeFrame benchmarks the imageToRGBA step.
func BenchmarkCaptureAndEncodeFrame(b *testing.B) {
	r := NewDirectRenderer(640, 480, 14, "", 0, 0)
	img, err := r.Render("Hello World benchmark 测试\r\nLine 2\r\nLine 3")
	if err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = imageToRGBA(img)
	}
}
