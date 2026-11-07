package tool

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/chromedp/chromedp"
)

type browserErrCode string

const (
	browserErrElementNotFound   browserErrCode = "ELEMENT_NOT_FOUND"
	browserErrElementNotVisible browserErrCode = "ELEMENT_NOT_VISIBLE"
	browserErrTimeout           browserErrCode = "TIMEOUT"
	browserErrNavRefused        browserErrCode = "NAV_REFUSED"
	browserErrChromeUnavailable browserErrCode = "CHROME_UNAVAILABLE"
	// #5209 D4 additions (narrow starting point: browser + desktop_control
	// only; repo-wide errcode adoption is a separate initiative).
	browserErrAmbiguous  browserErrCode = "AMBIGUOUS"
	browserErrInvalidArg browserErrCode = "INVALID_INPUT"
	browserErrUnknown    browserErrCode = "UNKNOWN"
)

// classifyBrowserErrContent maps a rendered error message (not an error
// value) to an errcode. Used by the Execute-level annotation choke point so
// the ~74 raw Sprintf error sites inherit codes without per-site edits
// (D4 constraint: keep existing consumers intact - the message body is
// preserved verbatim, only the [error_code=X] prefix is added).
//
// Reserved, currently unused categories (no producing error path today,
// declared for the review's exhaustiveness audit): NOT_AUTHORIZED,
// DEGRADED. They are intentionally NOT emitted until a real producer
// exists.
func classifyBrowserErrContent(content string) browserErrCode {
	s := strings.ToLower(content)
	switch {
	case strings.Contains(s, "no element matches"),
		strings.Contains(s, "matches no element"),
		strings.Contains(s, "element not found"),
		strings.Contains(s, "no ui element found"),
		strings.Contains(s, "reason: 'not-found'"),
		strings.Contains(s, "iframe not found"),
		strings.Contains(s, "cannot access iframe"):
		return browserErrElementNotFound
	case strings.Contains(s, "no unique visible"),
		strings.Contains(s, "document-order match #"),
		strings.Contains(s, "ambiguous"):
		return browserErrAmbiguous
	case strings.Contains(s, "hijacked"),
		strings.Contains(s, "hidden"),
		strings.Contains(s, "covered"),
		strings.Contains(s, "disabled"),
		strings.Contains(s, "out-of-viewport"),
		strings.Contains(s, "none had clickable coordinates"):
		return browserErrElementNotVisible
	case strings.Contains(s, "timed out"),
		strings.Contains(s, "timeout"),
		strings.Contains(s, "[timeout:"):
		return browserErrTimeout
	case strings.Contains(s, "unknown action"),
		strings.Contains(s, "unknown button"),
		strings.Contains(s, "unknown key"),
		strings.Contains(s, "invalid input"),
		strings.Contains(s, "cannot set value"),
		strings.Contains(s, "option not found"):
		return browserErrInvalidArg
	case strings.Contains(s, "chrome executable"),
		strings.Contains(s, "executable file not found"),
		strings.Contains(s, "failed to launch"),
		strings.Contains(s, "no chrome"):
		return browserErrChromeUnavailable
	case strings.Contains(s, "refused"),
		strings.Contains(s, "no such host"),
		strings.Contains(s, "dns"),
		strings.Contains(s, "invalid url"),
		strings.Contains(s, "lookup "):
		return browserErrNavRefused
	default:
		return browserErrUnknown
	}
}

// errcodePrefixMarker is the stable annotation format, identical to the
// one browserActionFailure already emits (format stability is a review
// checkpoint: no second format is introduced).
const errcodePrefixMarker = "[error_code="

// annotateErrcode is the Execute-level choke point: every IsError Result
// from the browser and desktop_control tools leaves with exactly one
// [error_code=X] prefix. Idempotent - messages that already carry a code
// (browserActionFailure output) pass through untouched. Non-error results
// are returned verbatim (the D1-D3 degradation paths stay prefix-free).
func annotateErrcode(r Result) Result {
	if !r.IsError {
		return r
	}
	if strings.Contains(r.Content, errcodePrefixMarker) {
		return r
	}
	code := classifyBrowserErrContent(r.Content)
	content := fmt.Sprintf("%s%s] %s", errcodePrefixMarker, code, r.Content)
	if hint := retryHintFor(code); hint != "" {
		content += "\n" + hint
	}
	return Result{IsError: true, Content: content, Images: r.Images, SuggestedWorkingDir: r.SuggestedWorkingDir, FollowUpMessages: r.FollowUpMessages}
}

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

func retryHintFor(code browserErrCode) string {
	switch code {
	case browserErrElementNotFound:
		return "selector matches nothing in the DOM - re-run action 'extract' or 'links' to get the current selector (the page may have re-rendered)"
	case browserErrElementNotVisible:
		return "element exists but is hidden or covered - try a different selector variant, check for an iframe (frame param), or raise wait_timeout"
	case browserErrAmbiguous:
		return "selector matches multiple elements with no unique visible one - use a more specific selector or snapshot @eN refs"
	case browserErrInvalidArg:
		return "the action arguments are invalid - fix the parameter values listed in the message"
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

func probeSelectorExists(ctx context.Context, selector string) (exists, ok bool) {
	expr := fmt.Sprintf(`(function(){return !!document.querySelector(%q);})()`, selector)
	found, err := chromedp.Run(ctx, chromedp.Evaluate[bool](expr))
	if err != nil {
		return false, false
	}
	return found, true
}

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
