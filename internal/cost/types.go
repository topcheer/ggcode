package cost

// TokenUsage records token consumption for a single API call.
// Defined in the cost package to avoid circular imports.
type TokenUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	CacheRead    int `json:"cache_read_tokens"`
	CacheWrite   int `json:"cache_write_tokens"`
}
