package tool

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/chromedp/chromedp"
)

// Structured browser-action error channel (sa-136, ICML2026 "Web Agents
// Should Use Typed Actions Instead of Click-Based Browsing" cross-check).
//
// Verdict from that cross-check: ggcode's browser tool already has typed
// INPUT (21-action enum schema + per-action arg validation) and localized
// failure attribution for select/drag, but click/type/screenshot failures
// surfaced raw chromedp errors ("click failed: context deadline exceeded")
// that conflate element-missing, element-hidden and timeout - the agent
// could not pick the right recovery move. classifyBrowserError plus a
// cheap DOM existence probe turns that channel into a typed, actionable
// failure without changing the Result schema.

// browserErrCode categorizes a browser action failure for agent-side
// recovery routing. Values are part of the tool's output contract - do
// not rename them casually.
type browserErrCode string

const (
	// The selector matches nothing in the DOM (probe-confirmed).
	browserErrElementNotFound browserErrCode = "ELEMENT_NOT_FOUND"
	// The element exists but never became visible/ready (probe-confirmed).
	browserErrElementNotVisible browserErrCode = "ELEMENT_NOT_VISIBLE"
	// Generic timeout; probe could not refine further.
	browserErrTimeout browserErrCode = "TIMEOUT"
	// Navigation-level failures: connection refused, DNS, bad URL.
	browserErrNavRefused browserErrCode = "NAV_REFUSED"
	// Chrome/Chromium binary missing or failed to launch.
	browserErrChromeUnavailable browserErrCode = "CHROME_UNAVAILABLE"
	// Anything else.
	browserErrUnknown browserErrCode = "UNKNOWN"
)

// classifyBrowserError maps a chromedp error onto a coarse category using
// error-text matching only (deterministic, no side effects - unit-testable
// without Chrome). The caller refines TIMEOUT into ELEMENT_NOT_FOUND /
// ELEMENT_NOT_VISIBLE with a DOM probe when a selector is in play.
func classifyBrowserError(err error) browserErrCode {
	if err == nil {
		return browserErrUnknown
	}
	s := strings.ToLower(err.Error())
	switch {
	case strings.Contains(s, "chrome executable"),
		strings.Contains(s, "executable file not found"),
		strings.Contains(s, "cannot find"),
		strings.Contains(s, "failed to launch"),
		strings.Contains(s, "no chrome"):
		return browserErrChromeUnavailable
	case strings.Contains(s, "refused"),
		strings.Contains(s, "no such host"),
		strings.Contains(s, "dns"),
		strings.Contains(s, "invalid url"),
		strings.Contains(s, "lookup "):
		return browserErrNavRefused
	case strings.Contains(s, "deadline"),
		strings.Contains(s, "timed out"),
		strings.Contains(s, "timeout"):
		return browserErrTimeout
	default:
		return browserErrUnknown
	}
}

// retryHintFor gives the agent the recovery move for a category. Typed
// failures are only useful if they change what the agent does next.
func retryHintFor(code browserErrCode) string {
	switch code {
	case browserErrElementNotFound:
		return "selector matches nothing in the DOM - re-run action 'extract' or 'links' to get the current selector (the page may have re-rendered)"
	case browserErrElementNotVisible:
		return "element exists but is hidden or covered - try a different selector variant, check for an iframe (frame param), or raise wait_timeout"
	case browserErrTimeout:
		return "operation timed out - raise wait_timeout or verify the page finished loading"
	case browserErrNavRefused:
		return "navigation-level failure - check the URL, DNS, and that the site is reachable"
	case browserErrChromeUnavailable:
		return "Chrome/Chromium is not available - install it or ensure it is on PATH"
	default:
		return ""
	}
}

// probeSelectorExists runs a minimal document.querySelector against the
// live tab to distinguish "selector matches nothing" from "matches but
// never became visible". ok=false means the probe itself failed (context
// cancelled, tab gone) - callers must treat that as no-information, never
// as element-missing.
func probeSelectorExists(ctx context.Context, selector string) (exists, ok bool) {
	var found bool
	expr := fmt.Sprintf(`(function(){return !!document.querySelector(%q);})()`, selector)
	if err := chromedp.Run(ctx, chromedp.Evaluate(expr, &found)); err != nil {
		return false, false
	}
	return found, true
}

// browserActionFailure renders a structured failure Result:
//
//	click failed [error_code=ELEMENT_NOT_FOUND]: context deadline exceeded
//	selector matches nothing in the DOM - ...
//
// The [error_code=...] prefix is machine-greppable; the original chromedp
// error is preserved verbatim for debugging. tabCtx must be the TAB's
// long-lived context (the action's timeout context is already dead by the
// time we get here) - the probe gets its own short deadline.
func (b *Browser) browserActionFailure(action string, tabCtx context.Context, selector string, err error) Result {
	code := classifyBrowserError(err)
	if code == browserErrTimeout && selector != "" && tabCtx != nil {
		probeCtx, cancel := context.WithTimeout(tabCtx, 3*time.Second)
		defer cancel()
		if exists, ok := probeSelectorExists(probeCtx, selector); ok {
			if !exists {
				code = browserErrElementNotFound
			} else {
				code = browserErrElementNotVisible
			}
		}
	}
	content := fmt.Sprintf("%s failed [error_code=%s]: %v", action, code, err)
	if hint := retryHintFor(code); hint != "" {
		content += "\n" + hint
	}
	return Result{IsError: true, Content: content}
}
