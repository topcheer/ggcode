package stream

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/golang/freetype/truetype"
)

//go:embed embedfonts/DejaVuSansMono.ttf
var dejaVuMonoRegular []byte

//go:embed embedfonts/DejaVuSansMono-Bold.ttf
var dejaVuMonoBold []byte

// DejaVuMonoRegular returns the embedded DejaVu Sans Mono regular font bytes.
func DejaVuMonoRegular() []byte { return dejaVuMonoRegular }

// DejaVuMonoBold returns the embedded DejaVu Sans Mono bold font bytes.
func DejaVuMonoBold() []byte { return dejaVuMonoBold }

// DejaVuMonoAdvance returns the advance width and line height of DejaVu Mono at the given point size.
func DejaVuMonoAdvance(points float64) (charWidth, charHeight int) {
	ttf, err := truetype.Parse(dejaVuMonoRegular)
	if err != nil {
		return 10, 19
	}
	face := truetype.NewFace(ttf, &truetype.Options{Size: points, DPI: 72})
	if adv, ok := face.GlyphAdvance('M'); ok && adv.Ceil() > 0 {
		charWidth = adv.Ceil()
	} else {
		charWidth = 10
	}
	if h := face.Metrics().Height.Ceil(); h > 0 {
		charHeight = h
	} else {
		charHeight = 19
	}
	return
}

// IsWide returns true if the rune is a wide (CJK) character.
func IsWide(r rune) bool {
	return runeWidth(r) > 1
}

// IsWideCol returns the display column width of a rune (1 or 2).
func IsWideCol(r rune) int {
	return runeWidth(r)
}

// runeWidth returns the display width of a rune: 2 for East-Asian Wide/Fullwidth, 1 otherwise.
func runeWidth(r rune) int {
	if r >= 0x20 && r <= 0x7E {
		return 1
	}
	switch {
	case r >= 0x1100 && r <= 0x115F: // Hangul Jamo
		return 2
	case r >= 0x2E80 && r <= 0x303E: // CJK Misc
		return 2
	case r >= 0x3040 && r <= 0x33BF: // Hiragana + Katakana + CJK punctuation
		return 2
	case r >= 0x3400 && r <= 0x4DBF: // CJK Unified Ideographs Extension A
		return 2
	case r >= 0x4E00 && r <= 0x9FFF: // CJK Unified Ideographs
		return 2
	case r >= 0xAC00 && r <= 0xD7AF: // Hangul Syllables
		return 2
	case r >= 0xF900 && r <= 0xFAFF: // CJK Compatibility Ideographs
		return 2
	case r >= 0xFE30 && r <= 0xFE6F: // CJK Compatibility Forms
		return 2
	case r >= 0xFF01 && r <= 0xFF60: // Fullwidth Forms
		return 2
	case r >= 0xFFE0 && r <= 0xFFE6: // Fullwidth Signs
		return 2
	case r >= 0x20000 && r <= 0x2FFEF: // CJK Extensions B-I
		return 2
	case r >= 0x30000 && r <= 0x3FFEF: // CJK Extension G
		return 2
	}
	return 1
}

// stripOSCHyperlinks removes OSC 8 hyperlink escape sequences.
// Format: \x1b]8;;<url>\x1b\<label>\x1b]8;;\x1b\ → <label>
// go-ansi-parser doesn't understand OSC 8, so we strip them before rendering.
func stripOSCHyperlinks(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	i := 0
	for i < len(s) {
		// Look for OSC 8 start: \x1b]8;;
		if i+4 < len(s) && s[i] == '\x1b' && s[i+1] == ']' && s[i+2] == '8' && s[i+3] == ';' && s[i+4] == ';' {
			// Skip to the first \x1b\ (BEL/ST) after the URL params
			j := i + 5
			for j < len(s) {
				if s[j] == '\x1b' && j+1 < len(s) && s[j+1] == '\\' {
					j += 2 // skip past \x1b\
					break
				}
				if s[j] == '\a' { // BEL is also a valid ST
					j++
					break
				}
				j++
			}
			// Now j points to the start of the label text
			// Find the closing \x1b]8;;\x1b\
			labelStart := j
			for j < len(s) {
				if j+6 < len(s) && s[j] == '\x1b' && s[j+1] == ']' && s[j+2] == '8' && s[j+3] == ';' && s[j+4] == ';' {
					// Found closing OSC 8 — write label text only
					b.WriteString(s[labelStart:j])
					// Skip the closing sequence
					j += 5
					if j < len(s) && s[j] == '\x1b' && j+1 < len(s) && s[j+1] == '\\' {
						j += 2
					} else if j < len(s) && s[j] == '\a' {
						j++
					}
					i = j
					goto next
				}
				j++
			}
			// No closing found — write as-is
			b.WriteString(s[i:])
			break
		}
		b.WriteByte(s[i])
		i++
	next:
	}
	return b.String()
}

// FontSearchResult holds the result of a system font search.
type FontSearchResult struct {
	Path  string
	Name  string
	IsCJK bool
}

// FindSystemFonts searches for monospace fonts on the system.
// Returns CJK-capable fonts first, then fallback fonts.
func FindSystemFonts() []FontSearchResult {
	var results []FontSearchResult

	fontDirs := fontDirectories()
	seen := make(map[string]bool)

	for _, dir := range fontDirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			name := entry.Name()
			if seen[name] {
				continue
			}
			lower := strings.ToLower(name)
			if !strings.HasSuffix(lower, ".ttf") && !strings.HasSuffix(lower, ".otf") && !strings.HasSuffix(lower, ".ttc") {
				continue
			}

			isCJK := isCJKFont(lower)
			results = append(results, FontSearchResult{
				Path:  filepath.Join(dir, name),
				Name:  name,
				IsCJK: isCJK,
			})
			seen[name] = true
		}
	}

	// Sort: CJK fonts first
	sortResults(results)
	return results
}

// FindCJKFont finds the best CJK-capable monospace font on the system.
// Returns empty string if none found.
// findBestMonoFont finds the best monospace font on the system with wide Unicode coverage.
// Prefers fonts that cover ASCII + symbols + CJK in a single font.
func findBestMonoFont() string {
	// Candidates in priority order — must be true monospace with broad glyph coverage
	candidates := []string{
		// macOS
		"/System/Library/Fonts/Monaco.ttf",
		"/System/Library/Fonts/SFNSMono.ttf",
		// Linux — Noto Sans Mono CJK (covers CJK + ASCII in monospace)
		"/usr/share/fonts/opentype/noto/NotoSansCJK-Regular.ttc",
		"/usr/share/fonts/noto-cjk/NotoSansCJK-Regular.ttc",
		"/usr/share/fonts/truetype/noto/NotoSansCJK-Regular.ttc",
		"/usr/share/fonts/opentype/noto/NotoSansMonoCJK-Regular.ttc",
		"/usr/share/fonts/noto-cjk/NotoSansMonoCJK-Regular.ttc",
		// Linux — DejaVu Sans Mono (good ASCII/symbols, no CJK)
		"/usr/share/fonts/truetype/dejavu/DejaVuSansMono.ttf",
		"/usr/share/fonts/dejavu/DejaVuSansMono.ttf",
		// Linux — Liberation Mono
		"/usr/share/fonts/truetype/liberation/LiberationMono-Regular.ttf",
		"/usr/share/fonts/liberation/LiberationMono-Regular.ttf",
		// Windows
		"C:\\Windows\\Fonts\\consola.ttf", // Consolas
		"C:\\Windows\\Fonts\\cour.ttf",    // Courier New
		"C:\\Windows\\Fonts\\lucon.ttf",   // Lucida Console
	}
	for _, path := range candidates {
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	// Search system font list as last resort
	fonts := FindSystemFonts()
	for _, f := range fonts {
		if !f.IsCJK {
			return f.Path
		}
	}
	return ""
}

func FindCJKFont() string {
	fonts := FindSystemFonts()
	for _, f := range fonts {
		if f.IsCJK {
			return f.Path
		}
	}
	// Try non-monospace CJK fonts as well
	cjkPaths := searchCJKFonts()
	if len(cjkPaths) > 0 {
		return cjkPaths[0]
	}
	return ""
}

// FindMonoFont finds a monospace font (any language).
func FindMonoFont() string {
	fonts := FindSystemFonts()
	for _, f := range fonts {
		if !f.IsCJK {
			return f.Path
		}
	}
	if len(fonts) > 0 {
		return fonts[0].Path
	}
	return ""
}

func searchCJKFonts() []string {
	dirs := fontDirectories()
	var results []string

	cjkNames := []string{
		// macOS
		"pingfang", "heiti", "hiragino", "stheiti", "stfangsong", "songti",
		// Linux
		"noto sans cjk", "notoserif", "wqy", "wenquanyi", "droid sans fallback",
		"wanted sans", "lxgw", "sarasa",
		// Windows
		"msyh", "simhei", "simsun", "microsoft yahei",
		// Cross-platform
		"arial unicode",
	}

	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			name := strings.ToLower(entry.Name())
			if !strings.HasSuffix(name, ".ttf") && !strings.HasSuffix(name, ".otf") && !strings.HasSuffix(name, ".ttc") {
				continue
			}
			for _, cjk := range cjkNames {
				if strings.Contains(name, cjk) {
					results = append(results, filepath.Join(dir, entry.Name()))
					break
				}
			}
		}
	}
	return results
}

func fontDirectories() []string {
	var dirs []string
	switch runtime.GOOS {
	case "darwin":
		dirs = []string{
			"/System/Library/Fonts",
			"/System/Library/Fonts/Supplemental",
			"/Library/Fonts",
			filepath.Join(os.Getenv("HOME"), "Library/Fonts"),
		}
	case "linux":
		dirs = []string{
			"/usr/share/fonts",
			"/usr/local/share/fonts",
			filepath.Join(os.Getenv("HOME"), ".local/share/fonts"),
			filepath.Join(os.Getenv("HOME"), ".fonts"),
		}
	case "windows":
		windir := os.Getenv("WINDIR")
		if windir == "" {
			windir = `C:\Windows`
		}
		dirs = []string{
			filepath.Join(windir, "Fonts"),
			filepath.Join(os.Getenv("LOCALAPPDATA"), "Microsoft", "Windows", "Fonts"),
		}
	default:
		dirs = []string{
			"/usr/share/fonts",
			"/usr/local/share/fonts",
		}
	}
	return dirs
}

// monoFontKeywords identifies monospace fonts by filename (case-insensitive).
var monoFontKeywords = []string{
	"mono", "courier", "consolas", "menlo", "dejavu", "liberation mono",
	"source code", "fira code", "firacode", "jetbrains", "hack", "iosevka",
	"inconsolata", "anonymous pro", "ubuntu mono", "droid sans mono",
	"roboto mono", "cascadia", "sarasa", "lxgw mono",
}

// cjkFontKeywords identifies CJK-capable fonts by filename.
// All comparisons are case-insensitive.
var cjkFontKeywords = []string{
	"cjk", "pingfang", "heiti", "hiragino", "noto sans cjk",
	"noto serif cjk", "wenquanyi", "wqy", "droid sans fallback",
	"msyh", "simhei", "simsun", "microsoft yahei",
	"arial unicode", "lxgw", "sarasa", "songti", "fangsong",
}

func isCJKFont(filename string) bool {
	for _, kw := range cjkFontKeywords {
		if strings.Contains(filename, kw) {
			return true
		}
	}
	return false
}

func isMonoFont(filename string) bool {
	for _, kw := range monoFontKeywords {
		if strings.Contains(filename, kw) {
			return true
		}
	}
	return false
}

func sortResults(results []FontSearchResult) {
	// Simple sort: CJK+Mono first, then CJK, then Mono, then rest
	for i := 0; i < len(results); i++ {
		for j := i + 1; j < len(results); j++ {
			ri, rj := scoreFont(results[i]), scoreFont(results[j])
			if rj > ri {
				results[i], results[j] = results[j], results[i]
			}
		}
	}
}

func scoreFont(f FontSearchResult) int {
	lower := strings.ToLower(f.Name)
	score := 0
	if isCJKFont(lower) {
		score += 100
	}
	if isMonoFont(lower) {
		score += 50
	}
	// Prefer .ttf over .ttc (collection)
	if strings.HasSuffix(lower, ".ttf") {
		score += 10
	}
	return score
}

// ReadFontFile reads a font file and returns its bytes.
// ReadFontFile reads a font file and returns raw font data.
// Both TTF and TTC (TrueType Collection) formats are supported by truetype.Parse.
// StatFile checks if a file exists.
func StatFile(path string) (os.FileInfo, error) {
	return os.Stat(path)
}

func ReadFontFile(path string) ([]byte, error) {
	if path == "" {
		return nil, fmt.Errorf("no font path provided")
	}
	return os.ReadFile(path)
}
