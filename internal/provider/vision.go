package provider

import (
	"errors"
	"strings"

	"github.com/sashabaranov/go-openai"
)

// IsImageBlockFallbackCandidate reports whether an image-bearing request should
// be retried without image blocks. We intentionally gate on HTTP 400 only and
// only for requests that already attempted to send image content.
func IsImageBlockFallbackCandidate(err error) bool {
	if err == nil {
		return false
	}

	var apiErr *openai.APIError
	if errors.As(err, &apiErr) {
		return apiErr.HTTPStatusCode == 400
	}

	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "status code: 400") ||
		strings.Contains(msg, "status: 400") ||
		strings.Contains(msg, "http 400") ||
		strings.Contains(msg, " 400 bad request")
}
