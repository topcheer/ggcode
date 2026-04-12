package provider

import (
	"errors"
	"testing"

	"github.com/sashabaranov/go-openai"
)

func TestIsImageBlockFallbackCandidate(t *testing.T) {
	t.Run("openai api 400", func(t *testing.T) {
		err := &openai.APIError{HTTPStatusCode: 400, Message: "bad request"}
		if !IsImageBlockFallbackCandidate(err) {
			t.Fatal("expected HTTP 400 API error to trigger fallback")
		}
	})

	t.Run("generic 400 text", func(t *testing.T) {
		err := errors.New("openai stream: error, status code: 400, status: 400 Bad Request")
		if !IsImageBlockFallbackCandidate(err) {
			t.Fatal("expected textual 400 error to trigger fallback")
		}
	})

	t.Run("non 400", func(t *testing.T) {
		err := &openai.APIError{HTTPStatusCode: 401, Message: "unauthorized"}
		if IsImageBlockFallbackCandidate(err) {
			t.Fatal("did not expect non-400 API error to trigger fallback")
		}
	})
}
