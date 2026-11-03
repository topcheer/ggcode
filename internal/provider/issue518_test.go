package provider

import (
	"fmt"
	"testing"
)

func TestIsRetryable_String422NotRetryable(t *testing.T) {
	for _, msg := range []string{

		"error, status code: 422, message: invalid schema",

		"openai stream: error, status code: 422, message: invalid tool schema",

		`{"error":{"status":422,"message":"unprocessable entity"}}`,

		"statusCode:422 unprocessable entity",
	} {
		if isRetryable(fmt.Errorf("%s", msg)) {
			t.Errorf("expected 422 string error to be non-retryable: %q", msg)
		}
	}
}

func TestIsRetryable_String429StillRetryable(t *testing.T) {
	for _, msg := range []string{
		"error, status code: 429, message: rate limit exceeded",
		`{"error":{"status":429}}`,
		"statusCode:429 too many requests",
		"request failed with 429 ",
	} {
		if !isRetryable(fmt.Errorf("%s", msg)) {
			t.Errorf("expected 429 string error to remain retryable: %q", msg)
		}
	}

	for _, msg := range []string{
		"status code: 408, message: request timeout",
		"statusCode:409 conflict",
	} {
		if !isRetryable(fmt.Errorf("%s", msg)) {
			t.Errorf("expected %q to remain retryable (408/409 semantics)", msg)
		}
	}
}

func TestIsRetryable_DigitCoincidenceNotRetryCode(t *testing.T) {

	msg := "request id=42913 rejected by upstream validation: unknown field"

	for _, code := range []string{"408", "409", "429", "500", "502"} {
		if containsHTTPStatus(msg, code) {
			t.Errorf("containsHTTPStatus(%q, %q) should be false (digit coincidence)", msg, code)
		}
	}
}
