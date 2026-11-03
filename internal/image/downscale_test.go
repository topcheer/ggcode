package image

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"
)

func TestDownscaleByPixels(t *testing.T) {

	src := image.NewRGBA(image.Rect(0, 0, 3000, 3000))
	src.Set(10, 10, color.RGBA{R: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, src); err != nil {
		t.Fatal(err)
	}
	big := Image{Data: buf.Bytes(), MIME: MIMEPNG}

	out := DownscaleByPixels(big, 8*1000*1000)
	decoded, err := decodeImageData(out.Data)
	if err != nil {
		t.Fatalf("downscaled output must decode: %v", err)
	}
	px := int64(decoded.Bounds().Dx()) * int64(decoded.Bounds().Dy())
	if px > 8*1000*1000 {
		t.Fatalf("pixel cap violated: %d px", px)
	}

	if d := decoded.Bounds().Dx() - decoded.Bounds().Dy(); d > 2 || d < -2 {
		t.Fatalf("aspect ratio broken: %dx%d", decoded.Bounds().Dx(), decoded.Bounds().Dy())
	}
	if out.MIME != MIMEPNG {
		t.Fatalf("MIME must be png, got %s", out.MIME)
	}

	small := image.NewRGBA(image.Rect(0, 0, 100, 100))
	var sbuf bytes.Buffer
	png.Encode(&sbuf, small)
	smallImg := Image{Data: sbuf.Bytes(), MIME: MIMEPNG}
	outSmall := DownscaleByPixels(smallImg, 8*1000*1000)
	if !bytes.Equal(outSmall.Data, smallImg.Data) {
		t.Fatal("image within the cap must be returned byte-identical")
	}

	junk := Image{Data: []byte("not an image"), MIME: "application/octet-stream"}
	outJunk := DownscaleByPixels(junk, 8*1000*1000)
	if !bytes.Equal(outJunk.Data, junk.Data) {
		t.Fatal("undecodable data must be left unchanged")
	}
}
