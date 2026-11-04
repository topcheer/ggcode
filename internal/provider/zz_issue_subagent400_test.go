package provider

import (
	"errors"
	"testing"
)

// zz_issue4999b_test.go: GLM (zai) reports context overflow on chat
// completions as a generic 400 "messages 参数非法" instead of an explicit
// prompt-too-long message. Reactive compaction (tryReactiveCompact ->
// IsContextOverflowError) must recognize it or long sub-agent sessions die
// instead of self-healing (observed 2026-11-04, sub-agent 400 incident).
func TestIsContextOverflowGLMParamIllegal(t *testing.T) {
	cases := []string{
		"chat error: status code: 400, message: messages 参数非法。请检查文档。",
		`400 Bad Request: {"error":{"message":"messages 参数非法"}}`,
		"openai chat: invalid messages payload",
		"stream: error, status code: 400, status: 400 Bad Request, message: messages 参数非法. Please check docs.",
	}
	for _, msg := range cases {
		if !IsContextOverflowError(errors.New(msg)) {
			t.Errorf("expected overflow match for: %s", msg)
		}
	}
	// sanity: non-overflow 400s must not match
	negatives := []string{
		"400 Bad Request: invalid model name",
		"400 Bad Request: missing api key",
	}
	for _, msg := range negatives {
		if IsContextOverflowError(errors.New(msg)) {
			t.Errorf("unexpected overflow match for: %s", msg)
		}
	}
}
