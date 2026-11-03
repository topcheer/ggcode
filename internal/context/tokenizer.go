package context

import "unicode"

const (
	tierLatinExtCharsPerToken = 3.0
	tierCyrillicCharsPerToken = 2.5
	tierGreekCharsPerToken    = 2.0
	tierOtherCharsPerToken    = 1.0
)

func scriptTokenClasses(text string) (ascii, cjk, latinExt, cyrillic, greek, other int) {
	for _, r := range text {
		switch {
		case r < 0x80:
			ascii++
		case r >= 0x2E80 && r <= 0x9FFF,
			r >= 0xAC00 && r <= 0xD7AF,
			r >= 0xF900 && r <= 0xFAFF,
			r >= 0xFF66 && r <= 0xFF9D:
			cjk++
		case unicode.Is(unicode.Latin, r):
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

func tieredTokens(ascii, cjk, latinExt, cyrillic, greek, other int, asciiRatio, cjkRatio float64) int {
	return int(float64(ascii)/asciiRatio) +
		int(float64(cjk)/cjkRatio) +
		int(float64(latinExt)/tierLatinExtCharsPerToken) +
		int(float64(cyrillic)/tierCyrillicCharsPerToken) +
		int(float64(greek)/tierGreekCharsPerToken) +
		int(float64(other)/tierOtherCharsPerToken) + 1
}

func EstimateTokens(text string) int {
	if !stringsHasNonASCII(text) {
		return int(float64(len(text))/3.5) + 1
	}

	a, c, le, cy, g, o := scriptTokenClasses(text)
	return tieredTokens(a, c, le, cy, g, o, 3.5, defaultCJKRatio)
}

func EstimateTokensCalibrated(text string, c *TokenCalibrator) int {
	if c == nil {
		return EstimateTokens(text)
	}
	asciiRatio := c.ASCIICharsPerToken()
	cjkRatio := c.CJKCharsPerToken()

	if !stringsHasNonASCII(text) {
		return int(float64(len(text))/asciiRatio) + 1
	}
	a, ck, le, cy, g, o := scriptTokenClasses(text)
	return tieredTokens(a, ck, le, cy, g, o, asciiRatio, cjkRatio)
}

func stringsHasNonASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 128 {
			return true
		}
	}
	return false
}
