package context

// EstimateTokens provides a rough token estimation.
// Uses ~4 chars/token for ASCII and ~1.5 chars/token for CJK, which matches
// common BPE tokenizer behavior more closely than a flat len/4.
func EstimateTokens(text string) int {
	ascii := 0
	cjk := 0
	for _, r := range text {
		if r > 127 {
			cjk++
		} else {
			ascii++
		}
	}
	// ASCII: ~4 chars/token, CJK: ~1.5 chars/token
	return ascii/4 + cjk*2/3 + 1
}
