package image

import (
	"bytes"
	"image/png"
	"math"
)

func DownscaleByPixels(img Image, maxPixels int64) Image {
	if maxPixels <= 0 || len(img.Data) == 0 {
		return img
	}
	decoded, err := decodeImageData(img.Data)
	if err != nil {
		return img
	}
	w := int64(decoded.Bounds().Dx())
	h := int64(decoded.Bounds().Dy())
	if w <= 0 || h <= 0 || w*h <= maxPixels {
		return img
	}

	scale := math.Sqrt(float64(maxPixels) / float64(w*h))
	newW := int64(float64(w) * scale)
	if newW < 1 {
		newW = 1
	}

	target := resizeImage(decoded, int(newW))
	var buf bytes.Buffer
	if err := png.Encode(&buf, target); err != nil {
		return img
	}
	return Image{
		Data:   buf.Bytes(),
		MIME:   MIMEPNG,
		Width:  target.Bounds().Dx(),
		Height: target.Bounds().Dy(),
	}
}
