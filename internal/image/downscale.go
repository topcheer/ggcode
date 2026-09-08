package image

import (
	"bytes"
	"image/png"
	"math"
)

// DownscaleByPixels returns img downscaled so its pixel count does not
// exceed maxPixels (#1866 case 2). Pasted clipboard images had only a raw
// byte cap (MaxSize 20MB): a large-but-well-compressed PNG sails through
// the byte gate and then expands to ~27MB of base64 in Go plus a second
// copy in the JS layer, only to be rejected or throttled by the provider.
// A pixel ceiling bounds the payload regardless of how well the source
// compresses. Images already within the cap are returned unchanged
// (byte-identical Data, original MIME).
func DownscaleByPixels(img Image, maxPixels int64) Image {
	if maxPixels <= 0 || len(img.Data) == 0 {
		return img
	}
	decoded, err := decodeImageData(img.Data)
	if err != nil {
		return img // not decodable here: leave the caller's bytes alone
	}
	w := int64(decoded.Bounds().Dx())
	h := int64(decoded.Bounds().Dy())
	if w <= 0 || h <= 0 || w*h <= maxPixels {
		return img
	}
	// Preserve aspect ratio; target the largest dimensions that fit.
	scale := math.Sqrt(float64(maxPixels) / float64(w*h))
	newW := int64(float64(w) * scale)
	newH := int64(float64(h) * scale)
	if newW < 1 {
		newW = 1
	}
	if newH < 1 {
		newH = 1
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
