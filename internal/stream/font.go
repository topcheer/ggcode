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
	// Emoji ranges — these render as 2 columns wide in all modern terminals
	case r >= 0x1F600 && r <= 0x1F64F: // Emoticons
		return 2
	case r >= 0x1F300 && r <= 0x1F5FF: // Misc Symbols and Pictographs
		return 2
	case r >= 0x1F680 && r <= 0x1F6FF: // Transport and Map
		return 2
	case r >= 0x1F900 && r <= 0x1F9FF: // Supplemental Symbols and Pictographs
		return 2
	case r >= 0x1FA00 && r <= 0x1FAFF: // Symbols and Pictographs Extended-A
		return 2
	case r >= 0x1F7E0 && r <= 0x1F7FF: // Geometric Shapes Extended (colored circles)
		return 2
	case r >= 0x2300 && r <= 0x23FF: // Misc Technical
		if r == 0x231A || r == 0x231B || (r >= 0x23E9 && r <= 0x23FA) {
			return 2
		}
		return 1
	case r >= 0x2600 && r <= 0x26FF: // Misc Symbols
		if r == 0x26A0 || r == 0x2614 || r == 0x2615 || r == 0x26AA || r == 0x26AB ||
			r == 0x26BD || r == 0x26BE || r == 0x26C4 || r == 0x26C5 ||
			(r >= 0x2648 && r <= 0x2653) || r == 0x26CE || r == 0x26D4 ||
			r == 0x26EA || (r >= 0x26F0 && r <= 0x26FA) || r == 0x26FD ||
			r == 0x2693 || r == 0x26F1 || r == 0x26F2 || r == 0x26F3 {
			return 2
		}
		return 1 // ⚙▶●✓ etc are single-width
	case r >= 0x2700 && r <= 0x27BF: // Dingbats
		if r == 0x2702 || r == 0x2705 || r == 0x2708 || r == 0x2709 ||
			(r >= 0x270A && r <= 0x270D) || r == 0x270F ||
			r == 0x2712 || r == 0x2714 || r == 0x2716 || r == 0x271D ||
			r == 0x2721 || r == 0x2728 || r == 0x2733 || r == 0x2734 ||
			r == 0x2744 || r == 0x2747 || r == 0x274C || r == 0x274E ||
			(r >= 0x2753 && r <= 0x2755) || r == 0x2757 ||
			(r >= 0x2763 && r <= 0x2764) || (r >= 0x2795 && r <= 0x2797) ||
			r == 0x27A1 || r == 0x27B0 || r == 0x27BF {
			return 2
		}
		return 1
	case r >= 0x2B00 && r <= 0x2BFF: // Misc Symbols and Arrows
		if r == 0x2B05 || r == 0x2B06 || r == 0x2B07 ||
			(r >= 0x2B1B && r <= 0x2B1C) || r == 0x2B50 || r == 0x2B55 {
			return 2
		}
		return 1
	case r == 0xFE0F: // Variation Selector 16 (emoji presentation)
		return 0 // Zero-width modifier
	case r == 0x200D: // ZWJ (zero-width joiner)
		return 0
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

// FindEmojiFont finds a system font capable of rendering emoji (color or monochrome).
func FindEmojiFont() string {
	dirs := fontDirectories()

	emojiNames := []string{
		// macOS / iOS
		"apple color emoji",
		// Linux
		"noto color emoji", "notoemoji", "emoji",
		// Windows
		"seguiemj", "segoe ui emoji", "segoeuisymbol",
		// Fallback: any font with "emoji" in the name
		"emoji",
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
			for _, emoji := range emojiNames {
				if strings.Contains(name, emoji) {
					return filepath.Join(dir, entry.Name())
				}
			}
		}
	}
	return ""
}

// IsEmoji returns true if the rune is in an emoji Unicode block.
func IsEmoji(r rune) bool {
	switch {
	case r >= 0x1F600 && r <= 0x1F64F: // Emoticons
		return true
	case r >= 0x1F300 && r <= 0x1F5FF: // Misc Symbols and Pictographs
		return true
	case r >= 0x1F680 && r <= 0x1F6FF: // Transport and Map
		return true
	case r >= 0x1F900 && r <= 0x1F9FF: // Supplemental Symbols and Pictographs
		return true
	case r >= 0x1FA00 && r <= 0x1FA6F: // Chess Symbols
		return true
	case r >= 0x1FA70 && r <= 0x1FAFF: // Symbols and Pictographs Extended-A
		return true
	case r >= 0x2600 && r <= 0x26FF: // Misc Symbols (includes ⚙)
		return true
	case r >= 0x2700 && r <= 0x27BF: // Dingbats
		return true
	case r >= 0xFE00 && r <= 0xFE0F: // Variation Selectors
		return true
	case r >= 0x200D: // ZWJ (used in compound emoji)
		return true
	}
	return false
}

// replaceEmojiForRender replaces emoji and other problematic Unicode characters
// with visually similar outline characters that DejaVu Mono can render.
// This preserves terminal display while ensuring stream frames render correctly.
// It also strips Variation Selector 16 (U+FE0F) and handles ZWJ sequences.
func replaceEmojiForRender(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	runes := []rune(s)
	for i := 0; i < len(runes); i++ {
		r := runes[i]

		// Strip variation selectors and ZWJ — they have no visual representation
		if r == 0xFE0F || r == 0x200D {
			continue
		}

		// Braille patterns render as empty/hollow boxes in Go's font renderer.
		// Map spinner chars to circle quadrants; others to middle dot.
		if r >= 0x2800 && r <= 0x28FF {
			switch r {
			case '⠋':
				b.WriteRune('◐')
			case '⠙':
				b.WriteRune('◓')
			case '⠹':
				b.WriteRune('◑')
			case '⠸':
				b.WriteRune('◒')
			case '⠼':
				b.WriteRune('◐')
			case '⠴':
				b.WriteRune('◓')
			case '⠦':
				b.WriteRune('◑')
			case '⠧':
				b.WriteRune('◒')
			case '⠇':
				b.WriteRune('◐')
			case '⠏':
				b.WriteRune('◓')
			default:
				b.WriteRune('·')
			}
			continue
		}

		// Check specific emoji replacements
		if replacement, ok := emojiReplacement(r); ok {
			b.WriteString(replacement)
			// Emoji is 2 columns wide; most replacements are 1 column.
			// Pad with a space to preserve column alignment.
			repRunes := []rune(replacement)
			if runeWidth(r) == 2 && len(repRunes) > 0 && runeWidth(repRunes[0]) == 1 {
				b.WriteRune(' ')
			}
			// Skip VS16 that commonly follows emoji
			if i+1 < len(runes) && runes[i+1] == 0xFE0F {
				i++
			}
			continue
		}

		// Any remaining emoji-range character → generic bullet + space
		if isEmojiRenderRange(r) {
			b.WriteString("• ")
			if i+1 < len(runes) && runes[i+1] == 0xFE0F {
				i++
			}
			continue
		}

		b.WriteRune(r)
	}
	return b.String()
}

// isEmojiRenderRange returns true for Unicode ranges that are bitmap/color emoji
// and cannot be rendered by Go's outline font renderer.
func isEmojiRenderRange(r rune) bool {
	switch {
	case r >= 0x1F600 && r <= 0x1F64F: // Emoticons
		return true
	case r >= 0x1F300 && r <= 0x1F5FF: // Misc Symbols and Pictographs
		return true
	case r >= 0x1F680 && r <= 0x1F6FF: // Transport and Map
		return true
	case r >= 0x1F900 && r <= 0x1F9FF: // Supplemental Symbols and Pictographs
		return true
	case r >= 0x1FA00 && r <= 0x1FAFF: // Symbols and Pictographs Extended-A
		return true
	case r >= 0x1F000 && r <= 0x1F02F: // Mahjong Tiles
		return true
	case r >= 0x1F0A0 && r <= 0x1F0FF: // Playing Cards
		return true
	case r >= 0x1F100 && r <= 0x1F1FF: // Enclosed Alphanumeric Supplement / Flags
		return true
	case r >= 0x1F200 && r <= 0x1F2FF: // Enclosed CJK
		return true
	case r >= 0x2300 && r <= 0x23FF: // Misc Technical
		// Only the problematic ones (⏳⏰⏸⌚⌛ etc)
		return r == 0x231A || r == 0x231B || (r >= 0x23E9 && r <= 0x23FA)
	case r >= 0x2B50 && r <= 0x2B55: // Star, Circle
		return true
	}
	return false
}

// emojiReplacement returns a safe string replacement for known emoji.
func emojiReplacement(r rune) (string, bool) {
	replacements := map[rune]string{
		// Status / activity
		0x23F3: "◑", // ⏳ hourglass → right-half circle
		0x23F0: "◷", // ⏰ alarm clock → clock face
		0x23F8: "‖", // ⏸ pause → double bar
		0x231A: "○", // ⌚ watch
		0x231B: "◑", // ⌛ hourglass → right-half circle
		// Objects
		0x1F4CB: "≡", // 📋 clipboard → triple bar
		0x1F4CA: "▦", // 📊 chart → grid
		0x1F4C4: "▭", // 📄 document → rectangle
		0x1F4C1: "▸", // 📁 folder → triangle
		0x1F4C2: "▸", // 📂 folder → triangle
		0x1F4DD: "✎", // 📝 memo → pencil
		0x1F4D6: "▭", // 📖 book → rectangle
		0x1F4BE: "□", // 💾 floppy → square
		0x1F5BC: "▣", // 🖼 picture → small square in square
		0x1F4E6: "◻", // 📦 package → square
		0x1F4C9: "↘", // 📉 chart decreasing
		// Tools / tech
		0x1F527: "⚙", // 🔧 wrench → gear
		0x1F50D: "⊙", // 🔍 search → circled dot
		0x1F50E: "⊙", // 🔎 search → circled dot
		0x1F310: "◉", // 🌐 globe → fisheye
		0x1F517: "⊕", // 🔗 link → circled plus
		0x1F5C2: "≡", // 🗂 index → triple bar
		0x1F9F0: "□", // 🧰 toolbox → square
		0x1F6E0: "⚙", // 🛠 tools → gear
		0x1F500: "⇄", // 🔀 shuffle → reverse arrows
		0x1F9EA: "△", // 🧪 test tube → triangle
		0x1FA9C: "△", // 🪜 ladder → triangle
		0x1FA9E: "◇", // 🪞 mirror
		0x1F916: "◉", // 🤖 robot → fisheye
		// Symbols
		0x1F3AF: "◈", // 🎯 target → diamond
		0x1F4B0: "◆", // 💰 money bag → diamond
		0x1F319: "☽", // 🌙 crescent moon
		0x1F4F1: "▢", // 📱 phone → square
		0x1F4A1: "☀", // 💡 bulb → sun
		0x1F6D1: "⊘", // 🛑 stop → circled slash
		0x1F4AC: "◁", // 💬 speech → left triangle
		0x1F4ED: "◃", // 📭 mailbox
		0x1F4EC: "▹", // 📬 mailbox
		0x1F4E1: "⇈", // 📡 satellite
		0x1F507: "⊘", // 🔇 muted → circled slash
		0x1F504: "↻", // 🔄 refresh
		0x1F512: "◉", // 🔒 lock
		0x1F513: "◦", // 🔓 unlock
		0x1F4E8: "▷", // 📨 sent → right triangle
		0x1F33F: "♣", // 🌿 herb → club
		0x1F9CA: "◇", // 🧊 ice → diamond
		0x1F5D1: "✕", // 🗑 trash
		// Colored circles
		0x1F534: "●", // 🔴 red circle
		0x1F535: "○", // 🔵 blue circle
		0x1F7E2: "●", // 🟢 green circle
		0x1F7E3: "●", // 🟣 purple circle
		// Common emoji
		0x2705: "✓", // ✅ check mark button
		0x274C: "✗", // ❌ cross mark
		0x2757: "!", // ❗ exclamation
		0x2049: "!", // ⁉ exclamation question
		0x2753: "?", // ❓ question
		0x2B50: "★", // ⭐ star
		0x2B55: "○", // ⭕ circle
	}
	if v, ok := replacements[r]; ok {
		return v, true
	}
	return "", false
}
