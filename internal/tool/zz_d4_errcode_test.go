package tool

import (
	"strings"
	"testing"
)

// #5209 D4: structured errcode prefixes for browser + desktop_control
// (narrow starting point per verdict ④). Acceptance: every category has a
// pin (prefix present + semantic routing correct), annotation is idempotent
// (browserActionFailure output keeps exactly one code), and non-error
// results - including D1-D3 degradation paths - pass through untouched.

func TestAnnotateErrcodeCategoryRouting(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    browserErrCode
	}{
		{"not-found", `no element matches selector ".x"`, browserErrElementNotFound},
		{"not-found-desktop", `No UI element found matching "Login"`, browserErrElementNotFound},
		{"not-visible", "click blocked: \".x\" is hidden (display:none...)", browserErrElementNotVisible},
		{"not-visible-covered", "click blocked: the click point on \".x\" is covered by <div#banner>", browserErrElementNotVisible},
		{"not-visible-hijack", "focus hijacked between focus and typing; active element is <input#q>", browserErrElementNotVisible},
		{"ambiguous", `selector ".x" matches 3 elements with no unique visible one (top candidates: ...)`, browserErrAmbiguous},
		{"ambiguous-d1", `type: unique visible match for ".x" is document-order match #2`, browserErrAmbiguous},
		{"timeout", "wait timed out after 10s: context deadline exceeded", browserErrTimeout},
		{"timeout-d2", "[timeout: budget exhausted] some iframes were skipped", browserErrTimeout},
		{"invalid-arg", `unknown action: snapshot2`, browserErrInvalidArg},
		{"invalid-arg-desktop", `unknown button "top" (left, right, middle)`, browserErrInvalidArg},
		{"chrome-unavailable", "chrome executable not found", browserErrChromeUnavailable},
		{"nav-refused", "connection refused", browserErrNavRefused},
		{"unknown-fallback", "some novel failure mode", browserErrUnknown},
	}
	for _, tc := range cases {
		r := annotateErrcode(Result{IsError: true, Content: tc.content})
		if !strings.HasPrefix(r.Content, "[error_code="+string(tc.want)+"]") {
			t.Errorf("%s: prefix = %.40s..., want [error_code=%s]", tc.name, r.Content, tc.want)
		}
		if !strings.Contains(r.Content, tc.content) {
			t.Errorf("%s: original message body not preserved", tc.name)
		}
	}
}

func TestAnnotateErrcodeIdempotent(t *testing.T) {
	// browserActionFailure output already carries a code: exactly one
	// prefix must remain, message body byte-identical.
	already := "click failed [error_code=TIMEOUT]: context deadline exceeded\noperation timed out - raise wait_timeout or verify the page finished loading"
	out := annotateErrcode(Result{IsError: true, Content: already})
	if out.Content != already {
		t.Errorf("idempotency broken:\nin:  %s\nout: %s", already, out.Content)
	}
	if strings.Count(out.Content, "[error_code=") != 1 {
		t.Errorf("expected exactly one code marker, got %d", strings.Count(out.Content, "[error_code="))
	}
}

// Degradation chains must not regress: D1-D3 probe-transport failures
// degrade to legacy SUCCESS paths - those are non-error Results and must
// pass through verbatim (no prefix, no hint).
func TestAnnotateErrcodeSkipsNonErrors(t *testing.T) {
	ok := Result{Content: "Clicked: .btn\nCurrent URL: https://example.com"}
	got := annotateErrcode(ok)
	if got.IsError != ok.IsError || got.Content != ok.Content {
		t.Errorf("non-error result mutated: %+v", got)
	}
	if strings.Contains(got.Content, "[error_code=") {
		t.Error("non-error result must not carry an errcode prefix")
	}
}

func TestAnnotateErrcodeAppendsHint(t *testing.T) {
	r := annotateErrcode(Result{IsError: true, Content: `selector ".a" matches 2 elements with no unique visible one`})
	if !strings.Contains(r.Content, "@eN") {
		t.Errorf("AMBIGUOUS hint missing @eN guidance:\n%s", r.Content)
	}
}
