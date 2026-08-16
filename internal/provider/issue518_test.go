package provider

import (
	"fmt"
	"testing"
)

// #518: the four real-world string forms of a 422 error (probe-verified in the
// issue) must all be classified non-retryable, matching the typed path
// isRetryableHTTPStatus(422) == false. Previously the string-fallback list
// contained "422" and retried 20 times (~7m16s) per turn.
func TestIsRetryable_String422NotRetryable(t *testing.T) {
	for _, msg := range []string{
		// go-openai stream error format (same shape as vision_test.go capture)
		"error, status code: 422, message: invalid schema",
		// openai.go L421 wrapper format
		"openai stream: error, status code: 422, message: invalid tool schema",
		// JSON relay format
		`{"error":{"status":422,"message":"unprocessable entity"}}`,
		// plain wrapper
		"statusCode:422 unprocessable entity",
	} {
		if isRetryable(fmt.Errorf("%s", msg)) {
			t.Errorf("expected 422 string error to be non-retryable: %q", msg)
		}
	}
}

// #518: 429 string forms stay retryable, 408/409 keep original semantics.
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
	// 408/409 keep original retryable semantics.
	for _, msg := range []string{
		"status code: 408, message: request timeout",
		"statusCode:409 conflict",
	} {
		if !isRetryable(fmt.Errorf("%s", msg)) {
			t.Errorf("expected %q to remain retryable (408/409 semantics)", msg)
		}
	}
}

// #518 low-risk side item: digit coincidences must not count as status codes
// now that the retryable list uses containsHTTPStatus anchoring.
func TestIsRetryable_DigitCoincidenceNotRetryCode(t *testing.T) {
	// "id=42913" contains substring "429" but is not an anchored status code;
	// without any other signal the default (retry unknown) applies, which is
	// fine — the assertion here is narrower: a *permanent-looking* message
	// carrying the coincidence must not be classified retryable via the code.
	msg := "request id=42913 rejected by upstream validation: unknown field"
	// No anchored retryable status present; default is retry=true for unknown
	// errors, so we only assert containsHTTPStatus does not fire.
	for _, code := range []string{"408", "409", "429", "500", "502"} {
		if containsHTTPStatus(msg, code) {
			t.Errorf("containsHTTPStatus(%q, %q) should be false (digit coincidence)", msg, code)
		}
	}
}
