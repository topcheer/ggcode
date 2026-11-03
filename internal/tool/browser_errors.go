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
	browserErrUnknown           browserErrCode = "UNKNOWN"
)

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
