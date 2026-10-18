package tool

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// sa-136: typed browser failure channel. Pure-function tests (no Chrome).
// The probe-refined paths (ELEMENT_NOT_FOUND vs ELEMENT_NOT_VISIBLE) need
// a live tab and are exercised via the classifier + format contract here.

func TestClassifyBrowserError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want browserErrCode
	}{
		{"nil", nil, browserErrUnknown},
		{"deadline", errors.New("context deadline exceeded"), browserErrTimeout},
		{"timed out", errors.New("wait timed out"), browserErrTimeout},
		{"timeout", errors.New("browser timeout"), browserErrTimeout},
		{"refused", errors.New("dial tcp 127.0.0.1:9222: connect: connection refused"), browserErrNavRefused},
		{"dns", errors.New("dial tcp: lookup nope.invalid: no such host"), browserErrNavRefused},
		{"chrome missing", errors.New("exec: \"google-chrome\": executable file not found in $PATH"), browserErrChromeUnavailable},
		{"chrome launch", errors.New("failed to launch chrome: chrome executable not found"), browserErrChromeUnavailable},
		{"other", errors.New("something odd"), browserErrUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := classifyBrowserError(tc.err); got != tc.want {
				t.Fatalf("classifyBrowserError(%v) = %q, want %q", tc.err, got, tc.want)
			}
		})
	}
}

func TestRetryHintFor(t *testing.T) {
	// Every category except UNKNOWN must carry an actionable hint - a typed
	// error code without a recovery move does not change agent behavior.
	for _, code := range []browserErrCode{
		browserErrElementNotFound,
		browserErrElementNotVisible,
		browserErrTimeout,
		browserErrNavRefused,
		browserErrChromeUnavailable,
	} {
		if retryHintFor(code) == "" {
			t.Errorf("retryHintFor(%q) is empty; every typed code needs a hint", code)
		}
	}
	if retryHintFor(browserErrUnknown) != "" {
		t.Error("retryHintFor(UNKNOWN) should be empty")
	}
}

func TestBrowserActionFailure_Format(t *testing.T) {
	b := &Browser{}
	// Non-timeout category with a selector: no probe attempted, code kept.
	res := b.browserActionFailure("click", context.Background(), "#x", errors.New("dial tcp: connection refused"))
	if !res.IsError {
		t.Fatal("result must be an error result")
	}
	if !strings.Contains(res.Content, "[error_code=NAV_REFUSED]") {
		t.Fatalf("content missing typed code: %q", res.Content)
	}
	if !strings.Contains(res.Content, "click failed") || !strings.Contains(res.Content, "connection refused") {
		t.Fatalf("content must keep action name and original error: %q", res.Content)
	}
	if !strings.Contains(res.Content, "navigation-level failure") {
		t.Fatalf("content missing retry hint: %q", res.Content)
	}
}

func TestBrowserActionFailure_TimeoutWithoutProbeCtx(t *testing.T) {
	b := &Browser{}
	// Timeout error but no tab ctx (nil): must degrade to plain TIMEOUT,
	// never panic, never guess element-missing.
	res := b.browserActionFailure("click", nil, "#x", errors.New("context deadline exceeded"))
	if !strings.Contains(res.Content, "[error_code=TIMEOUT]") {
		t.Fatalf("nil probe ctx should keep TIMEOUT: %q", res.Content)
	}
}

func TestBrowserActionFailure_UnknownNoHint(t *testing.T) {
	b := &Browser{}
	res := b.browserActionFailure("type", context.Background(), "#x", errors.New("weird failure"))
	if !strings.Contains(res.Content, "[error_code=UNKNOWN]") {
		t.Fatalf("want UNKNOWN code: %q", res.Content)
	}
	if strings.Count(res.Content, "\n") != 0 {
		t.Fatalf("UNKNOWN should append no hint line: %q", res.Content)
	}
}
