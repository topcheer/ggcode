package tool

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/chromedp/chromedp"
)

// #5209 D3: focus-token guard for the type action (design §3, verdict ③
// CONFIRM). Between focusing the target element and the keystroke
// injection, a re-render or a stolen-focus popup can move focus to a
// different element - typing then lands in the wrong box (password fields,
// search bars). The guard closes that race:
//
//   1. resolveTarget (D1) pins the unique visible match
//   2. a random one-shot dataset token (e.dataset.ggTk) is planted on it
//   3. the element is focused
//   4. document.activeElement is read back; its ggTk must equal the planted
//      token, otherwise focus was hijacked: REFUSE with the actual active
//      element's tag#id (verdict ③: report, never retry)
//   5. finally: the token is cleared on BOTH the success and the error path
//      (D3 hardening - zero DOM residue)
//
// press (global keys) has no target element and bypasses the guard. The
// virtual clipboard of the ZCode original is deliberately omitted - only
// the anti-drift core is adopted.

const focusTokenDatasetKey = "ggTk"

// focusTokenGuardJS plants the token on the selector's unique visible
// match, focuses it, and reads back the active element's identity + token.
// Executed as ONE Evaluate so plant/focus/read-back are atomic from the
// page's perspective (no await points for a re-render to slip in between).
const focusTokenGuardJS = `(() => {
	const matches = document.querySelectorAll(__SEL__);
	if (!matches.length) return {stage: 'no-match'};
	const styled = (el) => { const v = el.ownerDocument.defaultView; return v.getComputedStyle(el); };
	let target = null;
	if (matches.length === 1) target = matches[0];
	else {
		const shown = [];
		for (const el of matches) {
			const s = styled(el);
			const r = el.getBoundingClientRect();
			if (el.offsetParent !== null || s.position === 'fixed') {
				if (r.width * r.height > 0 && s.visibility !== 'hidden') shown.push(el);
			}
		}
		if (shown.length === 1) target = shown[0];
		else return {stage: 'ambiguous', n: matches.length};
	}
	target.dataset.ggTk = __TOKEN__;
	target.focus();
	const active = document.activeElement;
	const readBack = active && active.dataset ? (active.dataset.ggTk || '') : '';
	const id = active && active.id ? '#' + active.id : '';
	const describe = active ? active.tagName.toLowerCase() + id : 'none';
	if (readBack !== __TOKEN__ || active !== target) {
		// Hijacked: clear the token on the error path too (D3 hardening).
		delete target.dataset.ggTk;
		return {stage: 'hijacked', active: describe};
	}
	return {stage: 'ok'};
})()`

const focusTokenClearJS = `(() => {
	const matches = document.querySelectorAll(__SEL__);
	let cleared = 0;
	for (const el of matches) {
		if (el.dataset && el.dataset.ggTk !== undefined) { delete el.dataset.ggTk; cleared++; }
	}
	// Also sweep the (possibly different) active element: a hijack path may
	// have moved focus before clearing the planted target's token.
	const active = document.activeElement;
	if (active && active.dataset && active.dataset.ggTk !== undefined) { delete active.dataset.ggTk; cleared++; }
	return cleared;
})()`

type focusTokenGuardResult struct {
	Stage   string `json:"stage"`
	Active  string `json:"active"`
	Matches int    `json:"n"`
}

// mintFocusToken returns a random 8-byte one-shot token, hex-encoded.
func mintFocusToken() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand failure is practically unreachable; a fixed fallback
		// still guards the read-back comparison within this call.
		return "fallback-token"
	}
	return hex.EncodeToString(b)
}

// focusHijackedError renders the verdict ③ message shape: the word
// "hijacked" plus the actual active element's tag#id.
func focusHijackedError(selector, active string) error {
	return fmt.Errorf(
		"focus hijacked between focus and typing; active element is <%s>, refusing to type into wrong target (selector %q)",
		active, selector)
}

// runFocusTokenGuard executes plant/focus/read-back atomically and, on
// success, returns the clear function callers MUST invoke via defer (the
// finally token-clear on both paths - D3 hardening).
func (b *Browser) runFocusTokenGuard(ctx context.Context, selector string) (clear func(), err error) {
	token := mintFocusToken()
	q, _ := json.Marshal(selector)
	tq, _ := json.Marshal(token)
	expr := strings.ReplaceAll(focusTokenGuardJS, "__SEL__", string(q))
	expr = strings.ReplaceAll(expr, "__TOKEN__", string(tq))

	clear = func() {
		clearExpr := strings.ReplaceAll(focusTokenClearJS, "__SEL__", string(q))
		// Best-effort: the clear runs in its own Evaluate on the same tab
		// context; a failure to clear is logged, never propagated (the type
		// action outcome must not be masked by cleanup noise).
		if _, clearErr := chromedp.Run(ctx, chromedp.Evaluate[any](clearExpr)); clearErr != nil {
			// Intentionally swallowed: token is one-shot and dies with the
			// dataset on navigation; cleanup failure is non-fatal.
			_ = clearErr
		}
	}

	raw, runErr := chromedp.Run(ctx, chromedp.Evaluate[json.RawMessage](expr))
	if runErr != nil {
		clear()
		return nil, fmt.Errorf("focus token guard failed: %w", runErr)
	}
	var res focusTokenGuardResult
	if jsonErr := json.Unmarshal(raw, &res); jsonErr != nil {
		clear()
		return nil, fmt.Errorf("focus token guard returned malformed data: %w", jsonErr)
	}
	switch res.Stage {
	case "ok":
		return clear, nil
	case "hijacked":
		clear()
		return nil, focusHijackedError(selector, res.Active)
	case "no-match":
		clear()
		return nil, fmt.Errorf("no element matches selector %q", selector)
	case "ambiguous":
		clear()
		return nil, fmt.Errorf("selector %q matches %d elements with no unique visible one; use snapshot @eN refs", selector, res.Matches)
	}
	clear()
	return nil, fmt.Errorf("focus token guard unknown stage %q", res.Stage)
}
