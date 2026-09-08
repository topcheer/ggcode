package image

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"
)

// Regression for #1866 case 2: pasted clipboard images need a PIXEL
// ceiling, not just the 20MB byte gate - a well-compressed PNG can be
// huge in pixels while sailing under the byte cap.
func TestDownscaleByPixels(t *testing.T) {
	// Build a 3000x3000 (9MP) PNG.
	src := image.NewRGBA(image.Rect(0, 0, 3000, 3000))
	src.Set(10, 10, color.RGBA{R: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, src); err != nil {
		t.Fatal(err)
	}
	big := Image{Data: buf.Bytes(), MIME: MIMEPNG}

	// Cap at 8MP: must downscale, output must decode and fit the cap.
	out := DownscaleByPixels(big, 8*1000*1000)
	decoded, err := decodeImageData(out.Data)
	if err != nil {
		t.Fatalf("downscaled output must decode: %v", err)
	}
	px := int64(decoded.Bounds().Dx()) * int64(decoded.Bounds().Dy())
	if px > 8*1000*1000 {
		t.Fatalf("pixel cap violated: %d px", px)
	}
	// Aspect ratio preserved (square stays square within rounding).
	if d := decoded.Bounds().Dx() - decoded.Bounds().Dy(); d > 2 || d < -2 {
		t.Fatalf("aspect ratio broken: %dx%d", decoded.Bounds().Dx(), decoded.Bounds().Dy())
	}
	if out.MIME != MIMEPNG {
		t.Fatalf("MIME must be png, got %s", out.MIME)
	}

	// Small image: returned unchanged (byte-identical).
	small := image.NewRGBA(image.Rect(0, 0, 100, 100))
	var sbuf bytes.Buffer
	png.Encode(&sbuf, small)
	smallImg := Image{Data: sbuf.Bytes(), MIME: MIMEPNG}
	outSmall := DownscaleByPixels(smallImg, 8*1000*1000)
	if !bytes.Equal(outSmall.Data, smallImg.Data) {
		t.Fatal("image within the cap must be returned byte-identical")
	}

	// Undecodable data: left alone.
	junk := Image{Data: []byte("not an image"), MIME: "application/octet-stream"}
	outJunk := DownscaleByPixels(junk, 8*1000*1000)
	if !bytes.Equal(outJunk.Data, junk.Data) {
		t.Fatal("undecodable data must be left unchanged")
	}
}
