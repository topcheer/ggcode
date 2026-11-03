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

	"golang.org/x/image/font/sfnt"
)

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
	face       font.Face
	faceBold   font.Face
	symbolFace font.Face
	cjkFace    font.Face
	emojiFace  font.Face
	fontLoaded bool
}

func NewDirectRenderer(width, height, fontSize int, fontPath string, termCols, termRows int) *DirectRenderer {
	r := &DirectRenderer{
		width:    width,
		height:   height,
		fontSize: fontSize,
		fontPath: fontPath,
	}

	r.charWidth = max(1, int(float64(fontSize)*0.6))
	r.charHeight = max(1, int(float64(fontSize)*1.2))

	if termCols > 0 && termRows > 0 {
		r.cols = termCols
		r.rows = termRows
	} else {
		r.cols = max(1, width/r.charWidth)
		r.rows = max(1, height/r.charHeight)
	}

	return r
}

func (r *DirectRenderer) loadFonts() {

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

	if adv, ok := r.face.GlyphAdvance('M'); ok && adv.Ceil() > 0 {
		r.charWidth = adv.Ceil()
	}
	metrics := r.face.Metrics()
	if h := metrics.Height.Ceil(); h > 0 {
		r.charHeight = h
	}

	sysFontPath := findSymbolFallbackFont()
	if sysFontPath != "" {
		if data, err := ReadFontFile(sysFontPath); err == nil {
			if parsed, err := opentype.Parse(data); err == nil {
				if face, err := opentype.NewFace(parsed, &opentype.FaceOptions{
					Size:    float64(r.fontSize),
					DPI:     72,
					Hinting: font.HintingFull,
				}); err == nil {

					if adv, ok := face.GlyphAdvance('M'); ok && adv.Ceil() == r.charWidth {
						r.symbolFace = face
					}
				}
			}
		}
	}

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

	emojiPath := FindEmojiFont()
	if emojiPath != "" {
		r.emojiFace = r.loadFontFace(emojiPath)
	}

	boldData := DejaVuMonoBold()
	if boldParsed, err := opentype.Parse(boldData); err == nil {
		r.faceBold, _ = opentype.NewFace(boldParsed, &opentype.FaceOptions{
			Size:    float64(r.fontSize),
			DPI:     72,
			Hinting: font.HintingFull,
		})
	}

	r.fontLoaded = true
}

func findSymbolFallbackFont() string {
	candidates := []string{

		"/System/Library/Fonts/SFNSMono.ttf",
		"/System/Library/Fonts/Monaco.ttf",

		"/usr/share/fonts/truetype/dejavu/DejaVuSansMono.ttf",
		"/usr/share/fonts/truetype/liberation/LiberationMono-Regular.ttf",

		"C:\\Windows\\Fonts\\consola.ttf",
	}
	for _, p := range candidates {
		if _, err := StatFile(p); err == nil {
			return p
		}
	}
	return ""
}

func (r *DirectRenderer) loadFontFace(path string) font.Face {
	data, err := ReadFontFile(path)
	if err != nil {
		return nil
	}

	opts := &opentype.FaceOptions{
		Size:    float64(r.fontSize),
		DPI:     72,
		Hinting: font.HintingFull,
	}

	if parsed, err := opentype.Parse(data); err == nil {
		if face, err := opentype.NewFace(parsed, opts); err == nil {
			return face
		}
	}

	if sfntFont, err := sfnt.Parse(data); err == nil {
		if face, err := opentype.NewFace(sfntFont, opts); err == nil {
			return face
		}
	}

	return nil
}

func (r *DirectRenderer) Cols() int { return r.cols }

func (r *DirectRenderer) Rows() int { return r.rows }

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

func (r *DirectRenderer) Render(ansiText string) (*image.RGBA, error) {

	ansiText = replaceEmojiForRender(ansiText)

	r.fontOnce.Do(r.loadFonts)
	if r.fontErr != nil {
		return nil, r.fontErr
	}
	if r.face == nil {
		return nil, fmt.Errorf("no font face")
	}

	ansiText = stripOSCHyperlinks(ansiText)

	img := image.NewRGBA(image.Rect(0, 0, r.width, r.height))

	draw.Draw(img, img.Bounds(), &image.Uniform{C: color.NRGBA{R: 30, G: 30, B: 30, A: 255}}, image.Point{}, draw.Src)

	face := r.face
	metrics := face.Metrics()
	lineHeight := r.charHeight
	ascent := metrics.Ascent.Ceil()

	styledTexts, err := ansi.Parse(
		ansiText,
		ansi.WithDefaultForegroundColor("37"),
		ansi.WithDefaultBackgroundColor("30"),
		ansi.WithIgnoreInvalidCodes(),
	)
	if err != nil {
		styledTexts = nil
	}

	d := &font.Drawer{
		Dst:  img,
		Src:  image.NewUniform(color.NRGBA{R: 255, G: 255, B: 255, A: 255}),
		Face: face,
	}

	cursorX, cursorY := 0, 0

	selectFace := func(rne rune, bold bool) font.Face {
		if IsEmoji(rne) && r.emojiFace != nil {
			return r.emojiFace
		}
		if IsWide(rne) && r.cjkFace != nil {
			return r.cjkFace
		}

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
