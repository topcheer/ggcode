package stream

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"strings"
	"sync"

	ansi "github.com/leaanthony/go-ansi-parser"
	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

// DirectRenderer converts ANSI terminal strings directly to *image.RGBA
// without intermediate PNG encoding/decoding.
//
// Font strategy:
//   - Primary: embedded DejaVu Mono (true monospace, covers ASCII + box-drawing)
//   - Symbols fallback: system monospace font (covers ▶●✓❯█ etc.)
//   - CJK fallback: system CJK font (covers Chinese/Japanese/Korean)
type DirectRenderer struct {
	width      int
	height     int
	cols       int
	rows       int
	fontSize   int
	fontPath   string
	fontName   string
	charWidth  int
	charHeight int

	fontOnce   sync.Once
	fontErr    error
	face       font.Face // DejaVu Mono (primary)
	faceBold   font.Face
	symbolFace font.Face // system monospace font (symbols fallback)
	cjkFace    font.Face // CJK font (wide char fallback)
	fontLoaded bool
}

// NewDirectRenderer creates a renderer that produces *image.RGBA directly.
func NewDirectRenderer(width, height, fontSize int, fontPath string, termCols, termRows int) *DirectRenderer {
	r := &DirectRenderer{
		width:    width,
		height:   height,
		fontSize: fontSize,
		fontPath: fontPath,
	}

	// Initial estimates — will be refined after font loads
	r.charWidth = max(1, int(float64(fontSize)*0.6))
	r.charHeight = max(1, int(float64(fontSize)*1.2))

	// Use actual terminal grid size if provided
	if termCols > 0 && termRows > 0 {
		r.cols = termCols
		r.rows = termRows
	} else {
		r.cols = max(1, width/r.charWidth)
		r.rows = max(1, height/r.charHeight)
	}

	return r
}

// loadFonts initializes font faces using opentype.
func (r *DirectRenderer) loadFonts() {
	// 1. DejaVu Mono as primary (true monospace, embedded)
	dvData := DejaVuMonoRegular()
	dvParsed, err := opentype.Parse(dvData)
	if err != nil {
		r.fontErr = fmt.Errorf("parse DejaVu Mono: %w", err)
		return
	}
	r.face, err = opentype.NewFace(dvParsed, &opentype.FaceOptions{
		Size:    float64(r.fontSize),
		DPI:     72,
		Hinting: font.HintingFull,
	})
	if err != nil {
		r.fontErr = fmt.Errorf("create DejaVu face: %w", err)
		return
	}
	r.fontName = "DejaVuMono"

	// Measure from DejaVu Mono (true monospace)
	if adv, ok := r.face.GlyphAdvance('M'); ok && adv.Ceil() > 0 {
		r.charWidth = adv.Ceil()
	}
	metrics := r.face.Metrics()
	if h := metrics.Height.Ceil(); h > 0 {
		r.charHeight = h
	}

	// 2. Symbols fallback — system monospace font for Unicode symbols (▶●✓❯ etc.)
	// Try opentype.Parse first; if the font is TTC, truetype.Parse handles it.
	sysFontPath := findSymbolFallbackFont()
	if sysFontPath != "" {
		if data, err := ReadFontFile(sysFontPath); err == nil {
			if parsed, err := opentype.Parse(data); err == nil {
				if face, err := opentype.NewFace(parsed, &opentype.FaceOptions{
					Size:    float64(r.fontSize),
					DPI:     72,
					Hinting: font.HintingFull,
				}); err == nil {
					// Verify it's monospace (or close enough)
					if adv, ok := face.GlyphAdvance('M'); ok && adv.Ceil() == r.charWidth {
						r.symbolFace = face
					}
				}
			}
		}
	}

	// 3. CJK fallback
	cjkPath := FindCJKFont()
	if cjkPath != "" {
		if data, err := ReadFontFile(cjkPath); err == nil {
			if parsed, err := opentype.Parse(data); err == nil {
				if face, err := opentype.NewFace(parsed, &opentype.FaceOptions{
					Size:    float64(r.fontSize),
					DPI:     72,
					Hinting: font.HintingFull,
				}); err == nil {
					r.cjkFace = face
				}
			}
		}
	}

	// 4. Bold face from DejaVu Mono Bold
	boldData := DejaVuMonoBold()
	if boldParsed, err := opentype.Parse(boldData); err == nil {
		r.faceBold, _ = opentype.NewFace(boldParsed, &opentype.FaceOptions{
			Size:    float64(r.fontSize),
			DPI:     72,
			Hinting: font.HintingFull,
		})
	}

	// Grid already set from constructor (using terminal cols/rows)
	r.fontLoaded = true
}

// findSymbolFallbackFont finds a system monospace font that can render Unicode symbols.
// Must have the same charWidth as DejaVu Mono at the same point size.
func findSymbolFallbackFont() string {
	candidates := []string{
		// macOS
		"/System/Library/Fonts/SFNSMono.ttf",
		"/System/Library/Fonts/Monaco.ttf",
		// Linux
		"/usr/share/fonts/truetype/dejavu/DejaVuSansMono.ttf",
		"/usr/share/fonts/truetype/liberation/LiberationMono-Regular.ttf",
		// Windows
		"C:\\Windows\\Fonts\\consola.ttf",
	}
	for _, p := range candidates {
		if _, err := StatFile(p); err == nil {
			return p
		}
	}
	return ""
}

// Cols returns terminal column count.
func (r *DirectRenderer) Cols() int { return r.cols }

// Rows returns terminal row count.
func (r *DirectRenderer) Rows() int { return r.rows }

// FontInfo returns a human-readable description of the loaded font.
func (r *DirectRenderer) FontInfo() string {
	r.fontOnce.Do(r.loadFonts)
	if r.fontErr != nil {
		return fmt.Sprintf("font error: %v", r.fontErr)
	}
	if r.fontLoaded {
		parts := []string{r.fontName}
		if r.symbolFace != nil {
			parts = append(parts, "+Symbols")
		}
		if r.cjkFace != nil {
			parts = append(parts, "+CJK")
		}
		return fmt.Sprintf("%s (char=%dx%d grid=%dx%d)",
			strings.Join(parts, ""), r.charWidth, r.charHeight, r.cols, r.rows)
	}
	return "no font"
}

// Render converts ANSI text directly to an *image.RGBA.
func (r *DirectRenderer) Render(ansiText string) (*image.RGBA, error) {
	r.fontOnce.Do(r.loadFonts)
	if r.fontErr != nil {
		return nil, r.fontErr
	}
	if r.face == nil {
		return nil, fmt.Errorf("no font face")
	}

	// Strip OSC 8 hyperlink sequences — go-ansi-parser doesn't handle them.
	ansiText = stripOSCHyperlinks(ansiText)

	// Create output image
	img := image.NewRGBA(image.Rect(0, 0, r.width, r.height))

	// Fill background
	draw.Draw(img, img.Bounds(), &image.Uniform{C: color.NRGBA{R: 30, G: 30, B: 30, A: 255}}, image.Point{}, draw.Src)

	face := r.face
	metrics := face.Metrics()
	lineHeight := r.charHeight
	ascent := metrics.Ascent.Ceil()

	// Parse ANSI
	styledTexts, err := ansi.Parse(
		ansiText,
		ansi.WithDefaultForegroundColor("37"),
		ansi.WithDefaultBackgroundColor("30"),
		ansi.WithIgnoreInvalidCodes(),
	)
	if err != nil {
		styledTexts = nil
	}

	// Font drawer
	d := &font.Drawer{
		Dst:  img,
		Src:  image.NewUniform(color.NRGBA{R: 255, G: 255, B: 255, A: 255}),
		Face: face,
	}

	cursorX, cursorY := 0, 0

	// selectFace picks the right font face for a rune.
	selectFace := func(rne rune, bold bool) font.Face {
		if IsWide(rne) && r.cjkFace != nil {
			return r.cjkFace
		}
		// Check if primary font has this glyph
		if _, ok := face.GlyphAdvance(rne); !ok && r.symbolFace != nil {
			return r.symbolFace
		}
		if bold && r.faceBold != nil {
			return r.faceBold
		}
		return face
	}

	if len(styledTexts) > 0 {
		for _, st := range styledTexts {
			bold := st.Bold()

			// Foreground color
			if st.FgCol != nil {
				d.Src = image.NewUniform(color.NRGBA{
					R: uint8(st.FgCol.Rgb.R),
					G: uint8(st.FgCol.Rgb.G),
					B: uint8(st.FgCol.Rgb.B),
					A: 255,
				})
			} else {
				d.Src = image.NewUniform(color.NRGBA{R: 255, G: 255, B: 255, A: 255})
			}

			// Background
			if st.BgCol != nil {
				bgC := color.NRGBA{
					R: uint8(st.BgCol.Rgb.R),
					G: uint8(st.BgCol.Rgb.G),
					B: uint8(st.BgCol.Rgb.B),
					A: 255,
				}
				displayWidth := 0
				for _, rne := range st.Label {
					if rne == '\n' {
						displayWidth = r.cols - cursorX
						break
					}
					displayWidth += IsWideCol(rne)
				}
				if displayWidth > 0 {
					bgRect := image.Rect(
						cursorX*r.charWidth,
						cursorY*lineHeight,
						min((cursorX+displayWidth)*r.charWidth, r.width),
						min((cursorY+1)*lineHeight, r.height),
					)
					draw.Draw(img, bgRect, &image.Uniform{C: bgC}, image.Point{}, draw.Src)
				}
			}

			// Draw characters
			for _, rne := range []rune(st.Label) {
				if rne == '\n' {
					cursorX = 0
					cursorY++
					if cursorY >= r.rows {
						return img, nil
					}
					continue
				}
				if cursorX >= r.cols {
					cursorX = 0
					cursorY++
					if cursorY >= r.rows {
						return img, nil
					}
				}

				d.Face = selectFace(rne, bold)
				d.Dot = fixed.Point26_6{
					X: fixed.I(cursorX * r.charWidth),
					Y: fixed.I(cursorY*lineHeight + ascent),
				}
				d.DrawString(string(rne))
				cursorX += IsWideCol(rne)
			}
		}
	} else {
		// Plain text fallback
		lines := strings.Split(ansiText, "\n")
		for row, line := range lines {
			if row >= r.rows {
				break
			}
			col := 0
			for _, rne := range []rune(line) {
				if col >= r.cols {
					break
				}
				d.Face = selectFace(rne, false)
				d.Dot = fixed.Point26_6{
					X: fixed.I(col * r.charWidth),
					Y: fixed.I(row*lineHeight + ascent),
				}
				d.DrawString(string(rne))
				col += IsWideCol(rne)
			}
		}
	}

	return img, nil
}
