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

func DejaVuMonoRegular() []byte { return dejaVuMonoRegular }

func DejaVuMonoBold() []byte { return dejaVuMonoBold }

func IsWide(r rune) bool {
	return runeWidth(r) > 1
}

func IsWideCol(r rune) int {
	return runeWidth(r)
}

func runeWidth(r rune) int {
	if r >= 0x20 && r <= 0x7E {
		return 1
	}
	switch {
	case r >= 0x1100 && r <= 0x115F:
		return 2
	case r >= 0x2E80 && r <= 0x303E:
		return 2
	case r >= 0x3040 && r <= 0x33BF:
		return 2
	case r >= 0x3400 && r <= 0x4DBF:
		return 2
	case r >= 0x4E00 && r <= 0x9FFF:
		return 2
	case r >= 0xAC00 && r <= 0xD7AF:
		return 2
	case r >= 0xF900 && r <= 0xFAFF:
		return 2
	case r >= 0xFE30 && r <= 0xFE6F:
		return 2
	case r >= 0xFF01 && r <= 0xFF60:
		return 2
	case r >= 0xFFE0 && r <= 0xFFE6:
		return 2
	case r >= 0x20000 && r <= 0x2FFEF:
		return 2
	case r >= 0x30000 && r <= 0x3FFEF:
		return 2

	case r >= 0x1F600 && r <= 0x1F64F:
		return 2
	case r >= 0x1F300 && r <= 0x1F5FF:
		return 2
	case r >= 0x1F680 && r <= 0x1F6FF:
		return 2
	case r >= 0x1F900 && r <= 0x1F9FF:
		return 2
	case r >= 0x1FA00 && r <= 0x1FAFF:
		return 2
	case r >= 0x1F7E0 && r <= 0x1F7FF:
		return 2
	case r >= 0x2300 && r <= 0x23FF:
		if r == 0x231A || r == 0x231B || (r >= 0x23E9 && r <= 0x23FA) {
			return 2
		}
		return 1
	case r >= 0x2600 && r <= 0x26FF:
		if r == 0x26A0 || r == 0x2614 || r == 0x2615 || r == 0x26AA || r == 0x26AB ||
			r == 0x26BD || r == 0x26BE || r == 0x26C4 || r == 0x26C5 ||
			(r >= 0x2648 && r <= 0x2653) || r == 0x26CE || r == 0x26D4 ||
			r == 0x26EA || (r >= 0x26F0 && r <= 0x26FA) || r == 0x26FD ||
			r == 0x2693 || r == 0x26F1 || r == 0x26F2 || r == 0x26F3 {
			return 2
		}
		return 1
	case r >= 0x2700 && r <= 0x27BF:
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
	case r >= 0x2B00 && r <= 0x2BFF:
		if r == 0x2B05 || r == 0x2B06 || r == 0x2B07 ||
			(r >= 0x2B1B && r <= 0x2B1C) || r == 0x2B50 || r == 0x2B55 {
			return 2
		}
		return 1
	case r == 0xFE0F:
		return 0
	case r == 0x200D:
		return 0
	}
	return 1
}

func stripOSCHyperlinks(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	i := 0
	for i < len(s) {

		if i+4 < len(s) && s[i] == '\x1b' && s[i+1] == ']' && s[i+2] == '8' && s[i+3] == ';' && s[i+4] == ';' {

			j := i + 5
			for j < len(s) {
				if s[j] == '\x1b' && j+1 < len(s) && s[j+1] == '\\' {
					j += 2
					break
				}
				if s[j] == '\a' {
					j++
					break
				}
				j++
			}

			labelStart := j
			for j < len(s) {
				if j+6 < len(s) && s[j] == '\x1b' && s[j+1] == ']' && s[j+2] == '8' && s[j+3] == ';' && s[j+4] == ';' {

					b.WriteString(s[labelStart:j])

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

			b.WriteString(s[i:])
			break
		}
		b.WriteByte(s[i])
		i++
	next:
	}
	return b.String()
}

type FontSearchResult struct {
	Path  string
	Name  string
	IsCJK bool
}

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

	sortResults(results)
	return results
}

func FindCJKFont() string {
	fonts := FindSystemFonts()
	for _, f := range fonts {
		if f.IsCJK {
			return f.Path
		}
	}

	cjkPaths := searchCJKFonts()
	if len(cjkPaths) > 0 {
		return cjkPaths[0]
	}
	return ""
}

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

		"pingfang", "heiti", "hiragino", "stheiti", "stfangsong", "songti",

		"noto sans cjk", "notoserif", "wqy", "wenquanyi", "droid sans fallback",
		"wanted sans", "lxgw", "sarasa",

		"msyh", "simhei", "simsun", "microsoft yahei",

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

var monoFontKeywords = []string{
	"mono", "courier", "consolas", "menlo", "dejavu", "liberation mono",
	"source code", "fira code", "firacode", "jetbrains", "hack", "iosevka",
	"inconsolata", "anonymous pro", "ubuntu mono", "droid sans mono",
	"roboto mono", "cascadia", "sarasa", "lxgw mono",
}

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

	if strings.HasSuffix(lower, ".ttf") {
		score += 10
	}
	return score
}

func StatFile(path string) (os.FileInfo, error) {
	return os.Stat(path)
}

func ReadFontFile(path string) ([]byte, error) {
	if path == "" {
		return nil, fmt.Errorf("no font path provided")
	}
	return os.ReadFile(path)
}

func FindEmojiFont() string {
	dirs := fontDirectories()

	emojiNames := []string{

		"apple color emoji",

		"noto color emoji", "notoemoji", "emoji",

		"seguiemj", "segoe ui emoji", "segoeuisymbol",

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

func IsEmoji(r rune) bool {
	switch {
	case r >= 0x1F600 && r <= 0x1F64F:
		return true
	case r >= 0x1F300 && r <= 0x1F5FF:
		return true
	case r >= 0x1F680 && r <= 0x1F6FF:
		return true
	case r >= 0x1F900 && r <= 0x1F9FF:
		return true
	case r >= 0x1FA00 && r <= 0x1FA6F:
		return true
	case r >= 0x1FA70 && r <= 0x1FAFF:
		return true
	case r >= 0x2600 && r <= 0x26FF:
		return true
	case r >= 0x2700 && r <= 0x27BF:
		return true
	case r >= 0xFE00 && r <= 0xFE0F:
		return true
	case r == 0x200D:

		return true
	}
	return false
}

func replaceEmojiForRender(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	runes := []rune(s)
	for i := 0; i < len(runes); i++ {
		r := runes[i]

		if r == 0xFE0F || r == 0x200D {
			continue
		}

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

		if replacement, ok := emojiReplacement(r); ok {
			b.WriteString(replacement)

			repRunes := []rune(replacement)
			if runeWidth(r) == 2 && len(repRunes) > 0 && runeWidth(repRunes[0]) == 1 {
				b.WriteRune(' ')
			}

			if i+1 < len(runes) && runes[i+1] == 0xFE0F {
				i++
			}
			continue
		}

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

func isEmojiRenderRange(r rune) bool {
	switch {
	case r >= 0x1F600 && r <= 0x1F64F:
		return true
	case r >= 0x1F300 && r <= 0x1F5FF:
		return true
	case r >= 0x1F680 && r <= 0x1F6FF:
		return true
	case r >= 0x1F900 && r <= 0x1F9FF:
		return true
	case r >= 0x1FA00 && r <= 0x1FAFF:
		return true
	case r >= 0x1F000 && r <= 0x1F02F:
		return true
	case r >= 0x1F0A0 && r <= 0x1F0FF:
		return true
	case r >= 0x1F100 && r <= 0x1F1FF:
		return true
	case r >= 0x1F200 && r <= 0x1F2FF:
		return true
	case r >= 0x2300 && r <= 0x23FF:

		return r == 0x231A || r == 0x231B || (r >= 0x23E9 && r <= 0x23FA)
	case r >= 0x2B50 && r <= 0x2B55:
		return true
	}
	return false
}

func emojiReplacement(r rune) (string, bool) {
	replacements := map[rune]string{

		0x23F3: "◑",
		0x23F0: "◷",
		0x23F8: "‖",
		0x231A: "○",
		0x231B: "◑",

		0x1F4CB: "≡",
		0x1F4CA: "▦",
		0x1F4C4: "▭",
		0x1F4C1: "▸",
		0x1F4C2: "▸",
		0x1F4DD: "✎",
		0x1F4D6: "▭",
		0x1F4BE: "□",
		0x1F5BC: "▣",
		0x1F4E6: "◻",
		0x1F4C9: "↘",

		0x1F527: "⚙",
		0x1F50D: "⊙",
		0x1F50E: "⊙",
		0x1F310: "◉",
		0x1F517: "⊕",
		0x1F5C2: "≡",
		0x1F9F0: "□",
		0x1F6E0: "⚙",
		0x1F500: "⇄",
		0x1F9EA: "△",
		0x1FA9C: "△",
		0x1FA9E: "◇",
		0x1F916: "◉",

		0x1F3AF: "◈",
		0x1F4B0: "◆",
		0x1F319: "☽",
		0x1F4F1: "▢",
		0x1F4A1: "☀",
		0x1F6D1: "⊘",
		0x1F4AC: "◁",
		0x1F4ED: "◃",
		0x1F4EC: "▹",
		0x1F4E1: "⇈",
		0x1F507: "⊘",
		0x1F504: "↻",
		0x1F512: "◉",
		0x1F513: "◦",
		0x1F4E8: "▷",
		0x1F33F: "♣",
		0x1F9CA: "◇",
		0x1F5D1: "✕",

		0x1F534: "●",
		0x1F535: "○",
		0x1F7E2: "●",
		0x1F7E3: "●",

		0x2705: "✓",
		0x274C: "✗",
		0x2757: "!",
		0x2049: "!",
		0x2753: "?",
		0x2B50: "★",
		0x2B55: "○",
	}
	if v, ok := replacements[r]; ok {
		return v, true
	}
	return "", false
}
