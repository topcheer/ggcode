package context

import "unicode"

// Script-classified chars/token ratios (#535).
//
// Before #535 every rune > 127 was priced at the CJK ratio (1.0 chars/token
// after #515). That was correct for Han/Hangul/Kana but wildly wrong for
// other scripts: Cyrillic is ≈2.5 chars/token with modern BPE tokenizers
// (~0.4 token/char), so pure Russian/Greek sessions were overestimated ~2.5x
// and auto-compact fired ~60K tokens early in a 200K window. European text
// with Latin accents was ~1.5x over. Script classes now get their own tiers:
//
//   - ascii (r < 0x80):                    3.5 chars/token (unchanged)
//   - CJK (Han, Hangul, Kana, compat):     1.0 chars/token (#515 semantics — DO NOT CHANGE)
//   - Latin-1 Supplement / Latin Extended: 3.0 chars/token (slightly denser than ASCII)
//   - Cyrillic:                            2.5 chars/token
//   - Greek:                               2.0 chars/token
//   - other non-ASCII (emoji, symbols...): 1.0 chars/token (conservative, as before)
const (
	tierLatinExtCharsPerToken = 3.0
	tierCyrillicCharsPerToken = 2.5
	tierGreekCharsPerToken    = 2.0
	// tierOtherCharsPerToken stays 1.0: unknown high-bit scripts (emoji,
	// symbols, rare scripts) keep the conservative pre-#535 pricing.
	tierOtherCharsPerToken = 1.0
)

// scriptTokenClasses buckets a rune into one of the estimation tiers.
// Exported for composition-aware calibration callers.
func scriptTokenClasses(text string) (ascii, cjk, latinExt, cyrillic, greek, other int) {
	for _, r := range text {
		switch {
		case r < 0x80:
			ascii++
		case r >= 0x2E80 && r <= 0x9FFF, // CJK, Kangxi radicals, Hiragana/Katakana
			r >= 0xAC00 && r <= 0xD7AF, // Hangul syllables
			r >= 0xF900 && r <= 0xFAFF, // CJK compatibility ideographs
			r >= 0xFF66 && r <= 0xFF9D: // halfwidth Kana
			cjk++
		case unicode.Is(unicode.Latin, r):
			// non-ASCII Latin: Latin-1 Supplement (à é ü ñ), Latin Extended
			// (š ž ę), Latin Extended Additional (ẞ...). 3.0 chars/token.
			latinExt++
		case unicode.Is(unicode.Cyrillic, r):
			cyrillic++
		case unicode.Is(unicode.Greek, r):
			greek++
		default:
			other++
		}
	}
	return
}

// tieredTokens converts per-tier char counts into a token estimate using the
// given asciiRatio/cjkRatio (calibrated or default) and the fixed tiers for
// Latin-extended/Cyrillic/Greek/other scripts (#535).
func tieredTokens(ascii, cjk, latinExt, cyrillic, greek, other int, asciiRatio, cjkRatio float64) int {
	return int(float64(ascii)/asciiRatio) +
		int(float64(cjk)/cjkRatio) +
		int(float64(latinExt)/tierLatinExtCharsPerToken) +
		int(float64(cyrillic)/tierCyrillicCharsPerToken) +
		int(float64(greek)/tierGreekCharsPerToken) +
		int(float64(other)/tierOtherCharsPerToken) + 1
}

// EstimateTokens provides a rough token estimation.
// Uses ~3.5 chars/token for ASCII, ~1.0 chars/token for CJK (Han/Hangul/
// Kana), and script-specific tiers for Latin-extended (3.0), Cyrillic (2.5),
// Greek (2.0) and other non-ASCII scripts (1.0), which matches common BPE
// tokenizer behavior more closely than a flat len/4 (#535).
//
// Fast path: for pure-ASCII text (the common case for code/logs), it uses
// a simple len/3.5 calculation without iterating every rune. This is 5-10x
// faster than the rune-iteration approach on large strings.
// Slow path: only iterates runes when non-ASCII bytes are detected.
func EstimateTokens(text string) int {
	// Fast path: if all bytes are ASCII (< 128), skip rune iteration entirely.
	if !stringsHasNonASCII(text) {
		// Pure ASCII: ~3.5 bytes/token.
		return int(float64(len(text))/3.5) + 1
	}

	// Mixed scripts: must iterate runes to classify characters.
	a, c, le, cy, g, o := scriptTokenClasses(text)
	return tieredTokens(a, c, le, cy, g, o, 3.5, defaultCJKRatio)
}

// EstimateTokensCalibrated uses calibrator ratios if available for a more
// accurate estimate. Falls back to default ratios when calibrator is nil.
func EstimateTokensCalibrated(text string, c *TokenCalibrator) int {
	if c == nil {
		return EstimateTokens(text)
	}
	asciiRatio := c.ASCIICharsPerToken()
	cjkRatio := c.CJKCharsPerToken()

	// Fast path: pure ASCII
	if !stringsHasNonASCII(text) {
		return int(float64(len(text))/asciiRatio) + 1
	}
	// Slow path: mixed scripts — only ASCII and CJK tiers are calibrated;
	// other script tiers keep their fixed ratios (#535).
	a, ck, le, cy, g, o := scriptTokenClasses(text)
	return tieredTokens(a, ck, le, cy, g, o, asciiRatio, cjkRatio)
}

// stringsHasNonASCII scans raw bytes rather than decoding runes, which is faster for the
// common case where the text is pure ASCII and returns false immediately.
func stringsHasNonASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 128 {
			return true
		}
	}
	return false
}
