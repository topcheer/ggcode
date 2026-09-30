package stream

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

//go:embed embedfonts/DejaVuSansMono.ttf
var dejaVuMonoRegular []byte

//go:embed embedfonts/DejaVuSansMono-Bold.ttf
var dejaVuMonoBold []byte

// DejaVuMonoRegular returns the embedded DejaVu Sans Mono regular font bytes.
func DejaVuMonoRegular() []byte { return dejaVuMonoRegular }

// DejaVuMonoBold returns the embedded DejaVu Sans Mono bold font bytes.
func DejaVuMonoBold() []byte { return dejaVuMonoBold }

// IsWide returns true if the rune is a wide (CJK) character.
func IsWide(r rune) bool {
	return runeWidth(r) > 1
}

// IsWideCol returns the display column width of a rune (1 or 2).
func IsWideCol(r rune) int {
	return runeWidth(r)
}

// wideRanges lists inclusive [lo, hi] rune ranges that render two columns wide,
// sorted ascending. It covers East Asian Wide/Fullwidth blocks plus the emoji
// ranges that render wide in modern terminals. init builds the direct-lookup
// bmpWidth table from this list; astral planes keep a small filtered slice.
var wideRanges = [][2]rune{
	{0x1100, 0x115F},   // Hangul Jamo
	{0x231A, 0x231B},   // watch, hourglass
	{0x23E9, 0x23FA},   // media controls
	{0x2614, 0x2615},   // umbrella, hot beverage
	{0x2648, 0x2653},   // zodiac signs
	{0x2693, 0x2693},   // anchor
	{0x26A0, 0x26A0},   // warning sign
	{0x26AA, 0x26AB},   // circles
	{0x26BD, 0x26BE},   // soccer, baseball
	{0x26C4, 0x26C5},   // snowman, sun behind cloud
	{0x26CE, 0x26CE},   // Ophiuchus
	{0x26D4, 0x26D4},   // no entry
	{0x26EA, 0x26EA},   // church
	{0x26F0, 0x26FA},   // mountain..flag in hole
	{0x26FD, 0x26FD},   // fuel pump
	{0x2702, 0x2702},   // scissors
	{0x2705, 0x2705},   // check mark button
	{0x2708, 0x2709},   // airplane, envelope
	{0x270A, 0x270D},   // hand gestures
	{0x270F, 0x270F},   // pencil
	{0x2712, 0x2712},   // black nib
	{0x2714, 0x2714},   // check mark
	{0x2716, 0x2716},   // multiplication X
	{0x271D, 0x271D},   // latin cross
	{0x2721, 0x2721},   // star of David
	{0x2728, 0x2728},   // sparkles
	{0x2733, 0x2734},   // eight-spoked asterisk, eight-pointed star
	{0x2744, 0x2744},   // snowflake
	{0x2747, 0x2747},   // sparkle
	{0x274C, 0x274C},   // cross mark
	{0x274E, 0x274E},   // negative cross mark
	{0x2753, 0x2755},   // question/exclamation marks
	{0x2757, 0x2757},   // exclamation mark
	{0x2763, 0x2764},   // heart exclamation, red heart
	{0x2795, 0x2797},   // plus, minus, divide
	{0x27A1, 0x27A1},   // right arrow
	{0x27B0, 0x27B0},   // curly loop
	{0x27BF, 0x27BF},   // double curly loop
	{0x2B05, 0x2B07},   // arrows
	{0x2B1B, 0x2B1C},   // squares
	{0x2B50, 0x2B50},   // star
	{0x2B55, 0x2B55},   // circle
	{0x2E80, 0x303E},   // CJK Misc
	{0x3040, 0x33BF},   // Hiragana/Katakana + CJK punctuation (303F is narrow)
	{0x3400, 0x4DBF},   // CJK Unified Ideographs Extension A
	{0x4E00, 0x9FFF},   // CJK Unified Ideographs
	{0xAC00, 0xD7AF},   // Hangul Syllables
	{0xF900, 0xFAFF},   // CJK Compatibility Ideographs
	{0xFE30, 0xFE6F},   // CJK Compatibility Forms
	{0xFF01, 0xFF60},   // Fullwidth Forms
	{0xFFE0, 0xFFE6},   // Fullwidth Signs
	{0x1F300, 0x1F64F}, // Misc Symbols/Pictographs + Emoticons
	{0x1F680, 0x1F6FF}, // Transport and Map
	{0x1F7E0, 0x1F7FF}, // Geometric Shapes Extended (colored circles)
	{0x1F900, 0x1F9FF}, // Supplemental Symbols and Pictographs
	{0x1FA00, 0x1FAFF}, // Symbols and Pictographs Extended-A
	{0x20000, 0x2FFEF}, // CJK Extensions B-I
	{0x30000, 0x3FFEF}, // CJK Extension G
}

// bmpWidth is a direct-lookup width table for the entire BMP, built once at
// init (64 KiB, lives in BSS; init touches only non-default cells). A lookup
// is a single indexed load — measured ~2x faster than the legacy comparison
// chain on mixed CJK/emoji render batches.
var bmpWidth [0x10000]uint8

// astralRanges holds the wide ranges at or above U+10000 (emoji + CJK ext).
var astralRanges [][2]rune

func init() {
	for i := range bmpWidth {
		bmpWidth[i] = 1
	}
	bmpWidth[0x200D] = 0 // ZWJ
	bmpWidth[0xFE0F] = 0 // Variation Selector 16
	for _, p := range wideRanges {
		if p[1] < 0x10000 {
			for r := p[0]; r <= p[1]; r++ {
				bmpWidth[r] = 2
			}
			continue
		}
		if p[0] < 0x10000 { // range straddles the BMP boundary
			for r := p[0]; r < 0x10000; r++ {
				bmpWidth[r] = 2
			}
			p = [2]rune{0x10000, p[1]}
		}
		astralRanges = append(astralRanges, p)
	}
}

// runeWidth returns the display width of a rune: 2 for East-Asian Wide/Fullwidth,
// 0 for zero-width modifiers (ZWJ, VS16), 1 otherwise.
func runeWidth(r rune) int {
	if uint32(r) < 0x10000 {
		return int(bmpWidth[r])
	}
	for _, p := range astralRanges { // sorted; 9 entries max
		if r < p[0] {
			break
		}
		if r <= p[1] {
			return 2
		}
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
	case r == 0x200D: // ZWJ (used in compound emoji) -- exact codepoint;
		// #794: r >= 0x200D with no upper bound classified every CJK, kana,
		// hangul, fullwidth and box-drawing rune as emoji.
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
