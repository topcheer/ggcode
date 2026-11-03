package stream

import (
	"image"
	"image/color"
	"testing"
)

func BenchmarkImageToRGBAGeneric(b *testing.B) {
	img := image.NewRGBA(image.Rect(0, 0, 640, 480))
	for y := 0; y < 480; y++ {
		for x := 0; x < 640; x++ {
			img.SetRGBA(x, y, color.RGBA{R: 30, G: 30, B: 30, A: 255})
		}
	}

	var generic image.Image = img

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = imageToRGBA(generic)
	}
}

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

func BenchmarkRender(b *testing.B) {
	r := NewDirectRenderer(640, 480, 14, "", 0, 0)
	text := "Hello World 测试中文\r\nLine 2: some content\r\nLine 3: more text"

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

func BenchmarkDirectRender(b *testing.B) {
	r := NewDirectRenderer(640, 480, 14, "", 0, 0)
	text := "Hello World 测试中文\r\nLine 2: some content\r\nLine 3: more text"

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
