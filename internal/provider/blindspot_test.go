package provider

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/sashabaranov/go-openai"
)

func TestIsBlindSpotError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"unrecognized string error", errors.New("some gateway hiccup"), true},
		{"empty error is unrecognized too", errors.New(""), true},
		{"401 via SDK", &openai.APIError{HTTPStatusCode: http.StatusUnauthorized, Message: "bad key"}, false},
		{"429 rate limit", &openai.APIError{HTTPStatusCode: http.StatusTooManyRequests, Message: "slow down"}, false},
		{"503 via SDK", &openai.APIError{HTTPStatusCode: http.StatusServiceUnavailable}, false},
		{"context overflow cue", errors.New("prompt too long: context length exceeded"), false},
		{"connection refused", errors.New("dial tcp: connection refused"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsBlindSpotError(tc.err); got != tc.want {
				t.Errorf("IsBlindSpotError(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

func TestBlindSpotFallbackEmbedsRawError(t *testing.T) {
	raw := "weird upstream proxy failure XYZ"
	msg := UserFacingErrorLang(errors.New(raw), "zh-CN")
	if !strings.Contains(msg, raw) {
		t.Errorf("zh fallback must embed raw error, got %q", msg)
	}
	if !strings.HasPrefix(msg, fallbackErrMsgZh) {
		t.Errorf("zh fallback must keep the generic stem, got %q", msg)
	}
	msgEn := UserFacingErrorLang(errors.New(raw), "en")
	if !strings.Contains(msgEn, raw) {
		t.Errorf("en fallback must embed raw error, got %q", msgEn)
	}
}

func TestBlindSpotRawTruncated(t *testing.T) {
	long := strings.Repeat("x", 1000)
	msg := UserFacingErrorLang(errors.New(long), "zh-CN")
	if len(msg) > len(fallbackErrMsgZh)+blindSpotRawLimit+100 {
		t.Errorf("fallback embeds untruncated raw error: %d chars", len(msg))
	}
	if !strings.Contains(msg, "(truncated)") {
		t.Errorf("expected truncation marker, got %q", msg[:80])
	}
}

func TestSDKPrefixBranchNotBlindSpot(t *testing.T) {
	// Errors with a strippable SDK prefix hit the "Request failed: <msg>"
	// branch, which is a recognized shape — not a blind spot.
	err := fmt.Errorf("openai chat: weird proxy failure")
	if IsBlindSpotError(err) {
		t.Errorf("SDK-prefixed error should not be a blind spot")
	}
	msg := UserFacingErrorLang(err, "zh-CN")
	if !strings.HasPrefix(msg, "请求失败：") {
		t.Errorf("expected prefix-stripped message, got %q", msg)
	}
}
