package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/chromedp/chromedp"
)

// resolveTarget disambiguates a CSS selector before an action is dispatched
// (docs/design/browser-desktop-borrowings.md §2; review verdict ② CONFIRM:
// on ambiguity REPORT with top-5 candidates instead of silently acting on
// the first document-order match - responsive pages routinely keep both
// desktop and mobile DOMs alive, so the first match is often a hidden
// duplicate).
//
// Return values:
//   - matched, index, nil: a unique (visible) match exists; index is its
//     position among querySelectorAll matches.
//   - false, 0, err: no match, or ambiguous - err carries the top-5
//     candidate list and already ends with the @eN disambiguation hint.
//
// The @eN ref path bypasses this probe entirely (refs are pre-resolved
// positions from snapshot).
const resolveTargetProbeJS = `(() => {
	const matches = document.querySelectorAll(__SEL__);
	if (!matches.length) return {m: 0};
	const vis = [];
	for (let i = 0; i < matches.length; i++) {
		const el = matches[i];
		const r = el.getBoundingClientRect();
		const styled = getComputedStyle(el);
		const anchored = el.offsetParent !== null || styled.position === 'fixed';
		const shown = anchored && r.width * r.height > 0 && styled.visibility !== 'hidden';
		vis.push({i, shown, tag: el.tagName.toLowerCase(),
			text: (el.textContent || '').trim().slice(0, 30)});
	}
	if (matches.length === 1) return {m: 1, idx: 0};
	const shownList = vis.filter(v => v.shown);
	if (shownList.length === 1) return {m: 1, idx: shownList[0].i};
	const cand = vis.slice(0, 5).map(v => '#' + v.i + ' ' + v.tag +
		(v.text ? ' "' + v.text + '"' : '') + ' [' + (v.shown ? 'visible' : 'hidden') + ']');
	return {m: 0, n: matches.length, c: cand.join(' | ')};
})()`

type resolveTargetProbeResult struct {
	Matched    bool   `json:"m"`
	Index      int    `json:"idx"`
	Count      int    `json:"n"`
	Candidates string `json:"c"`
}

// resolveTarget runs the disambiguation probe in the page.
func (b *Browser) resolveTarget(ctx context.Context, selector string) (matched bool, index int, err error) {
	expr := replaceSelectorPlaceholder(resolveTargetProbeJS, selector)
	raw, runErr := chromedp.Run(ctx, chromedp.Evaluate[json.RawMessage](expr))
	if runErr != nil {
		// Probe failure must not block actions that would otherwise work;
		// callers treat a probe error as "proceed on the legacy path".
		return false, 0, fmt.Errorf("resolve target probe failed: %w", runErr)
	}
	var res resolveTargetProbeResult
	if jsonErr := json.Unmarshal(raw, &res); jsonErr != nil {
		return false, 0, fmt.Errorf("resolve target probe returned malformed data: %w", jsonErr)
	}
	if res.Matched {
		return true, res.Index, nil
	}
	if res.Count == 0 {
		return false, 0, fmt.Errorf("no element matches selector %q", selector)
	}
	return false, 0, fmt.Errorf(
		"selector %q matches %d elements with no unique visible one (top candidates: %s); "+
			"use a more specific selector or snapshot @eN refs",
		selector, res.Count, res.Candidates)
}

// replaceSelectorPlaceholder injects a JSON-quoted selector literal in place
// of the __SEL__ placeholder (avoids fmt.Sprintf on a non-constant format
// string, which vet flags).
func replaceSelectorPlaceholder(js, selector string) string {
	q, _ := json.Marshal(selector)
	return strings.Replace(js, "__SEL__", string(q), 1)
}

// precheckTarget is the shared pre-action disambiguation gate (#5209 D1).
// It returns a non-nil *Result (already an error result) when the selector
// matches nothing or is ambiguous (no unique visible match). Probe transport
// failures degrade to the legacy path (nil) - the probe is advisory.
func (b *Browser) precheckTarget(ctx context.Context, selector string) *Result {
	_, _, err := b.resolveTarget(ctx, selector)
	if err == nil || isProbeTransportError(err) {
		return nil
	}
	return &Result{IsError: true, Content: err.Error()}
}

// isProbeTransportError distinguishes probe transport/malformed failures
// (which must degrade to the legacy action path) from semantic findings
// (no match / ambiguity) that gate the action.
func isProbeTransportError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "probe failed") || strings.Contains(msg, "malformed")
}

// clickAtMatchJS returns a JS expression that clicks the index-th element
// matched by the selector (used when the unique visible match is not the
// first document-order match, so chromedp.Click would hit the wrong node).
func clickAtMatchJS(selector string, index int) string {
	q, _ := json.Marshal(selector)
	return fmt.Sprintf(`(() => { const m = document.querySelectorAll(%s); if (m.length <= %d) return false; m[%d].click(); return true; })()`, string(q), index, index)
}
