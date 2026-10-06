package image

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"
)

// zz_issue2995_test.go - regression probes for #2995: resizeImage recomputed
// newH from the width with integer division and no clamp, so extreme aspect
// ratios (5000x1) produced newH=0 -> empty dst -> png.Encode failure. The
// screenshot path swallowed that error and emitted Image{Data: original,
// Height: 0} metadata inconsistency; DownscaleByPixels fell back to original
// bytes (pixel ceiling silently lost). The caller-side newH<1 clamp in
// downscale.go was dead code - only newW reaches resizeImage.

func TestIssue2995ExtremeWideImageClampsHeightToOne(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 5000, 1))
	for x := 0; x < 5000; x++ {
		src.Set(x, 0, color.RGBA{R: 128, A: 255})
	}
	dst := resizeImage(src, 1000)
	if got := dst.Bounds().Dy(); got != 1 {
		t.Fatalf("#2995: 5000x1 resized must clamp height to 1, got %d (0 makes png.Encode fail)", got)
	}
	if got := dst.Bounds().Dx(); got != 1000 {
		t.Fatalf("#2995: width must be maxW, got %d", got)
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, dst); err != nil {
		t.Fatalf("#2995: clamped result must encode, got %v", err)
	}
}

func TestIssue2995NormalAspectUnchanged(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 2000, 1000))
	dst := resizeImage(src, 1000)
	if dst.Bounds().Dx() != 1000 || dst.Bounds().Dy() != 500 {
		t.Fatalf("#2995: normal 2:1 aspect must stay exact, got %dx%d", dst.Bounds().Dx(), dst.Bounds().Dy())
	}
	// Already-fitting images return unchanged.
	same := resizeImage(dst, 2000)
	if same != image.Image(dst) {
		t.Fatalf("#2995: oldW<=maxW must return src unchanged")
	}
}
