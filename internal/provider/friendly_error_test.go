package provider

import (
	"context"
	"errors"
	"io"
	"net"
	"testing"

	"github.com/sashabaranov/go-openai"
)

func TestFriendlyError_Nil(t *testing.T) {
	if msg := FriendlyError(nil); msg != "" {
		t.Errorf("expected empty string for nil error, got %q", msg)
	}
}

func TestFriendlyError_401(t *testing.T) {
	err := &openai.APIError{HTTPStatusCode: 401, Message: "Unauthorized"}
	msg := FriendlyError(err)
	if !contains(msg, "401") {
		t.Errorf("expected 401 in message, got %q", msg)
	}
	if !contains(msg, "API key") {
		t.Errorf("expected 'API key' advice, got %q", msg)
	}
}

func TestFriendlyError_402(t *testing.T) {
	err := &openai.APIError{HTTPStatusCode: 402, Message: "Payment Required"}
	msg := FriendlyError(err)
	if !contains(msg, "Payment") {
		t.Errorf("expected 'Payment' in message, got %q", msg)
	}
	if !contains(msg, "credits") {
		t.Errorf("expected 'credits' advice, got %q", msg)
	}
}

func TestFriendlyError_403(t *testing.T) {
	err := &openai.APIError{HTTPStatusCode: 403, Message: "Forbidden"}
	msg := FriendlyError(err)
	if !contains(msg, "403") {
		t.Errorf("expected 403 in message, got %q", msg)
	}
	if !contains(msg, "permission") {
		t.Errorf("expected 'permission' advice, got %q", msg)
	}
}

func TestFriendlyError_403_RateLimit(t *testing.T) {
	err := &openai.APIError{HTTPStatusCode: 403, Message: "rate limit exceeded"}
	msg := FriendlyError(err)
	if !contains(msg, "Rate limit") {
		t.Errorf("expected 'Rate limit' for 403 rate limit, got %q", msg)
	}
}

func TestFriendlyError_403_AccessTerminated(t *testing.T) {
	// Simulates Kimi coding API quota exhaustion
	err := &openai.APIError{
		HTTPStatusCode: 403,
		Message:        "You've reached your usage limit for this billing cycle. Your quota will be refreshed in the next cycle. To continue now, purchase extra usage or upgrade your plan: https://www.kimi.com/code/#pricing",
	}
	msg := FriendlyError(err)
	if !contains(msg, "quota exhausted") {
		t.Errorf("expected 'quota exhausted' for access_terminated_error, got %q", msg)
	}
	if contains(msg, "forbidden") {
		t.Errorf("should not say 'forbidden' for quota exhaustion, got %q", msg)
	}
}

func TestFriendlyError_404(t *testing.T) {
	err := &openai.APIError{HTTPStatusCode: 404, Message: "model not found"}
	msg := FriendlyError(err)
	if !contains(msg, "not found") {
		t.Errorf("expected 'not found' for 404, got %q", msg)
	}
	if !contains(msg, "/model") {
		t.Errorf("expected /model advice, got %q", msg)
	}
}

func TestFriendlyError_429(t *testing.T) {
	err := &openai.APIError{HTTPStatusCode: 429, Message: "Too Many Requests"}
	msg := FriendlyError(err)
	if !contains(msg, "429") {
		t.Errorf("expected 429 in message, got %q", msg)
	}
	if !contains(msg, "Rate") {
		t.Errorf("expected 'Rate' in message, got %q", msg)
	}
}

func TestFriendlyError_500(t *testing.T) {
	err := &openai.APIError{HTTPStatusCode: 503, Message: "Service Unavailable"}
	msg := FriendlyError(err)
	if !contains(msg, "503") {
		t.Errorf("expected 503 in message, got %q", msg)
	}
	if !contains(msg, "temporary") {
		t.Errorf("expected 'temporary' advice, got %q", msg)
	}
}

func TestFriendlyError_ContextOverflow(t *testing.T) {
	err := errors.New("error, status code: 400, message: context_length_exceeded")
	msg := FriendlyError(err)
	if !contains(msg, "/compact") {
		t.Errorf("expected /compact advice for context overflow, got %q", msg)
	}
}

func TestFriendlyError_NetworkTimeout(t *testing.T) {
	err := &timeoutError{msg: "i/o timeout"}
	msg := FriendlyError(err)
	if !contains(msg, "timed out") {
		t.Errorf("expected 'timed out' for network timeout, got %q", msg)
	}
}

func TestFriendlyError_ConnectionClosed(t *testing.T) {
	err := io.EOF
	msg := FriendlyError(err)
	if !contains(msg, "Connection closed") {
		t.Errorf("expected 'Connection closed' for EOF, got %q", msg)
	}
}

func TestFriendlyError_Cancelled(t *testing.T) {
	err := context.Canceled
	msg := FriendlyError(err)
	if msg != "Request cancelled." {
		t.Errorf("expected 'Request cancelled.', got %q", msg)
	}
}

func TestFriendlyError_Unknown(t *testing.T) {
	origErr := errors.New("some weird error")
	msg := FriendlyError(origErr)
	if msg != "some weird error" {
		t.Errorf("expected original error for unknown type, got %q", msg)
	}
}

func TestFriendlyError_422(t *testing.T) {
	err := &openai.APIError{HTTPStatusCode: 422, Message: "Unprocessable Entity"}
	msg := FriendlyError(err)
	if !contains(msg, "422") {
		t.Errorf("expected 422 in message, got %q", msg)
	}
	if !contains(msg, "format") {
		t.Errorf("expected 'format' advice, got %q", msg)
	}
}

// Helper
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsStr(s, substr))
}

func containsStr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// timeoutError implements net.Error
type timeoutError struct {
	msg string
}

func (e *timeoutError) Error() string   { return e.msg }
func (e *timeoutError) Timeout() bool   { return true }
func (e *timeoutError) Temporary() bool { return true }

// Ensure net.Error is satisfied
var _ net.Error = (*timeoutError)(nil)
