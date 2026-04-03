package context

// EstimateTokens provides a more accurate token estimation than simple len/4.
// It distinguishes ASCII characters (~0.25 token/char) from CJK characters (~0.5 token/char).
func EstimateTokens(text string) int {
	count := 0
	for _, r := range text {
		if r > 127 {
			count += 2 // CJK
		} else {
			count++ // ASCII
		}
	}
	return count/4 + 1
}
