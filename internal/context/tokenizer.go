package context

// EstimateTokens provides a rough token estimation.
// Uses ~3.5 chars/token for ASCII and ~1.5 chars/token for CJK, which matches
// common BPE tokenizer behavior more closely than a flat len/4.
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

	// Mixed ASCII/CJK: must iterate runes to count CJK characters.
	ascii := 0
	cjk := 0
	for _, r := range text {
		if r > 127 {
			cjk++
		} else {
			ascii++
		}
	}
	// ASCII: ~3.5 chars/token, CJK: ~1.5 chars/token
	return int(float64(ascii)/3.5) + cjk*2/3 + 1
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
	// Slow path: mixed ASCII/CJK
	ascii := 0
	cjk := 0
	for _, r := range text {
		if r > 127 {
			cjk++
		} else {
			ascii++
		}
	}
	return int(float64(ascii)/asciiRatio) + int(float64(cjk)/cjkRatio) + 1
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
