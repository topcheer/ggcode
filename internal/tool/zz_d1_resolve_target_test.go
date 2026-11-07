package tool

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// #5209 D1: resolveTarget disambiguation probe - unit tests for the
// non-browser surfaces (JS construction, placeholder injection, error
// classification). Browser-level behavior is covered by the integration
// suite; these pin the contract pieces reviewers asked for (verdict §6.1 ②:
// ambiguity reports with top-5 candidates).

func TestResolveTargetProbeJSPlaceholderInjection(t *testing.T) {
	out := replaceSelectorPlaceholder(resolveTargetProbeJS, `.btn[class="a"]`)
	if strings.Contains(out, "__SEL__") {
		t.Fatal("placeholder not replaced")
	}
	// The selector must appear JSON-quoted exactly once in the querySelectorAll call.
	if strings.Count(out, `document.querySelectorAll(".btn[class=\"a\"]")`) != 1 {
		t.Errorf("selector not injected as a single JSON-quoted literal:\n%.200s", out)
	}
	// Injected selector must not be able to break out of the string literal.
	if strings.Contains(out, `"__proto__`) && strings.Contains(out, `");`) {
		t.Error("possible literal breakout via crafted selector")
	}
}

func TestIsProbeTransportError(t *testing.T) {
	cases := []struct {
		err  error
		want bool
	}{
		{nil, false},
		{errors.New("resolve target probe failed: context deadline exceeded"), true},
		{errors.New("resolve target probe returned malformed data: unexpected end of JSON"), true},
		{errors.New(`no element matches selector ".x"`), false},
		{errors.New(`selector ".x" matches 3 elements with no unique visible one (top candidates: ...)`), false},
	}
	for _, tc := range cases {
		if got := isProbeTransportError(tc.err); got != tc.want {
			t.Errorf("isProbeTransportError(%v) = %v, want %v", tc.err, got, tc.want)
		}
	}
}

// precheckTarget must degrade to nil (legacy path) on transport errors and
// gate with an error result on semantic findings. The transport side is
// exercised with a cancelled context, which makes the chromedp probe fail
// without a browser.
func TestPrecheckTargetDegradesOnTransportFailure(t *testing.T) {
	b := &Browser{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // probe will fail immediately with a context error
	if res := b.precheckTarget(ctx, ".anything"); res != nil {
		t.Errorf("precheckTarget on cancelled ctx must degrade to nil, got %+v", res)
	}
}

func TestClickAtMatchJS(t *testing.T) {
	js := clickAtMatchJS(`.buy`, 2)
	for _, want := range []string{`document.querySelectorAll(".buy")`, `m[2].click()`, `m.length <= 2`} {
		if !strings.Contains(js, want) {
			t.Errorf("clickAtMatchJS missing %q in:\n%s", want, js)
		}
	}
}
