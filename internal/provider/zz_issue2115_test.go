package provider

// #2115 regression: isThinkingError was a bare substring match over the
// whole error message - unrelated errors mentioning "thinking" (an
// upstream 5xx about streaming thinking blocks, a gateway error whose
// MODEL NAME contains "thinking", a quota error mentioning
// "budget_tokens plan") were misjudged, silently stripped thinking for
// the rest of the session. Now: anchored phrases + a 4xx parameter-class
// status gate when a status code is extractable.

import (
	"errors"
	"fmt"
	"testing"
)

type statusErr struct {
	code int
	msg  string
}

func (e statusErr) Error() string   { return e.msg }
func (e statusErr) StatusCode() int { return e.code }

func TestIsThinkingErrorTrueCases(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"direct not-supported", errors.New("[400] this model does not support thinking")},
		{"not enabled", errors.New("[400] thinking is not enabled for this model")},
		{"budget too small", errors.New("[400] budget_tokens must be at least 1024")},
		{"budget field error", errors.New("budget_tokens: field required")},
		{"gateway stringified", errors.New("claude-3-5-haiku does not support extended thinking")},
		{"no status, anchored", errors.New("thinking is not supported on this model")},
		{"typed 400", statusErr{400, "unexpected: thinking parameter"}},
	} {
		if !isThinkingError(tc.err) {
			t.Errorf("%s: expected true, got false", tc.name)
		}
	}
}

func TestIsThinkingErrorFalseCases(t *testing.T) {
	// The three probe-reproduced false positives from the issue.
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"upstream 5xx streaming thinking blocks", errors.New("upstream error while streaming thinking blocks (500)")},
		{"model name contains thinking", errors.New("[500] claude-3-7-thinking internal server error")},
		{"quota mentions budget_tokens plan", errors.New("429 quota exceeded for budget_tokens plan")},
		{"no anchor at all", errors.New("connection reset by peer")},
		{"typed 500 with anchor", statusErr{500, "does not support thinking"}},
		{"typed 429 with anchor word only", statusErr{429, "thinking quota exceeded"}},
		{"nil", nil},
	} {
		if isThinkingError(tc.err) {
			t.Errorf("%s: expected false, got true (thinking would be silently stripped)", tc.name)
		}
	}
}

var _ = fmt.Sprintf // keep fmt if future assertions use it
