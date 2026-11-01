package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/chromedp"
)

// #4854: agent-browser parity — ref-based snapshot interaction model.
//
// snapshot returns an accessibility-shaped tree of the page's interactive
// elements, each carrying a stable short ref (@e1, @e2, ...) in DOM order.
// Subsequent click/type/extract/select/hover/upload/scroll calls accept an
// @eN ref in the selector parameter (CSS selectors keep working unchanged).
// Refs stay valid within the tab until navigation (navigate/back invalidate
// the snapshot generation); heavy in-page DOM surgery may shift structural
// paths — the click pre-check then fails with a precise reason instead of
// clicking the wrong element.

// browserRefEntry is one interactive element captured by a snapshot.
type browserRefEntry struct {
	Ref      string `json:"ref"` // "@eN"
	Role     string `json:"role"`
	Name     string `json:"name"`
	Selector string `json:"selector"` // stable CSS path (nth-of-type chain)
}

// browserRefSnapshot is one tab's captured ref set.
type browserRefSnapshot struct {
	Generation int
	Entries    []browserRefEntry
	At         time.Time
}

// browserRefRegistry holds the latest snapshot per profile|session tab.
// Generation bumps on navigation; resolving a ref from an older generation
// is refused so a stale ref can never silently target the wrong element.
type browserRefRegistry struct {
	mu    sync.Mutex
	snaps map[string]*browserRefSnapshot
	gen   int
}

func newBrowserRefRegistry() *browserRefRegistry {
	return &browserRefRegistry{snaps: make(map[string]*browserRefSnapshot)}
}

func browserRefKey(profile, session string) string {
	return profile + "\x00" + session
}

// store records a fresh snapshot for the tab (new generation).
func (r *browserRefRegistry) store(key string, entries []browserRefEntry) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.gen++
	r.snaps[key] = &browserRefSnapshot{Generation: r.gen, Entries: entries, At: time.Now()}
}

// invalidate drops the tab's snapshot (navigation destroyed the page).
func (r *browserRefRegistry) invalidate(key string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.snaps, key)
}

// resolve maps an @eN ref to its CSS selector.
func (r *browserRefRegistry) resolve(key, ref string) (browserRefEntry, bool) {
	if !strings.HasPrefix(ref, "@e") {
		return browserRefEntry{}, false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	snap, ok := r.snaps[key]
	if !ok {
		return browserRefEntry{}, false
	}
	for _, e := range snap.Entries {
		if e.Ref == ref {
			return e, true
		}
	}
	return browserRefEntry{}, false
}

// hasRefs reports whether the tab holds a live snapshot.
func (r *browserRefRegistry) hasRefs(key string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.snaps[key]
	return ok
}

// resolveSelectorArg accepts either a plain CSS selector (returned verbatim)
// or an @eN ref (resolved against the tab's latest snapshot). A ref that
// cannot be resolved returns an actionable error.
func (b *Browser) resolveSelectorArg(profile, session, selector string) (string, bool) {
	if selector == "" || !strings.HasPrefix(selector, "@e") {
		return selector, true
	}
	key := browserRefKey(profile, session)
	if !b.refs.hasRefs(key) {
		return "", false
	}
	if entry, ok := b.refs.resolve(key, selector); ok {
		return entry.Selector, true
	}
	return "", false
}

// snapshotCollectionJS walks the DOM and returns a JSON array of
// {role,name,sel} for interactive/orientation elements, @eN-assigned in DOM
// order, capped at 200 entries. Rendered as a self-contained IIFE returning
// a JSON string so the CDP roundtrip is a single string decode.
const browserSnapshotJS = `(() => {
	const out = [];
	const cap = 200;
	const interactive = 'a,button,input,textarea,select,[role],[onclick],[tabindex],label,h1,h2,h3,h4,h5,h6,img,summary,details,video,audio,canvas,svg[role]';
	const nodes = document.querySelectorAll(interactive);
	const roleName = (el) => {
		const r = el.getAttribute('role');
		if (r) return r;
		const t = el.tagName.toLowerCase();
		if (t === 'a') return el.getAttribute('href') ? 'link' : 'link(no-href)';
		if (t === 'button') return 'button';
		if (t === 'input') return 'input:' + (el.getAttribute('type') || 'text');
		if (t === 'textarea') return 'textbox';
		if (t === 'select') return 'combobox';
		if (t === 'img') return 'image';
		if (/^h[1-6]$/.test(t)) return 'heading:' + t;
		if (t === 'label') return 'label';
		if (t === 'summary') return 'summary';
		return 'clickable';
	};
	const nameOf = (el) => {
		const n = el.getAttribute('aria-label') || el.getAttribute('alt') ||
			el.getAttribute('placeholder') || el.getAttribute('title') ||
			el.getAttribute('value') || (el.innerText || el.textContent || '');
		return n.replace(/\s+/g, ' ').trim().slice(0, 80);
	};
	const cssPath = (el) => {
		// nth-of-type chain up to <body>; stops at any element with an id
		// (ids are stable anchors). Depth-capped at 8.
		const parts = [];
		let cur = el, depth = 0;
		while (cur && cur.nodeType === 1 && depth < 8) {
			const tag = cur.tagName.toLowerCase();
			if (tag === 'html' || tag === 'body') { parts.unshift(tag); break; }
			if (cur.id) { parts.unshift('#' + CSS.escape(cur.id)); break; }
			const parent = cur.parentNode;
			if (parent) {
				let nth = 1, sib = cur.previousElementSibling;
				while (sib) { if (sib.tagName === cur.tagName) nth++; sib = sib.previousElementSibling; }
				parts.unshift(tag + ':nth-of-type(' + nth + ')');
			} else {
				parts.unshift(tag);
			}
			cur = parent; depth++;
		}
		return parts.join(' > ');
	};
	for (const el of nodes) {
		if (out.length >= cap) break;
		const cs = getComputedStyle(el);
		if (cs.display === 'none' || cs.visibility === 'hidden') continue;
		const rect = el.getBoundingClientRect();
		if (rect.width === 0 && rect.height === 0) continue;
		const href = el.tagName.toLowerCase() === 'a' ? (el.getAttribute('href') || '') : '';
		out.push({ role: roleName(el), name: nameOf(el), sel: cssPath(el), href: href.slice(0, 200) });
	}
	return JSON.stringify(out);
})()`

// collectBrowserRefs runs the snapshot walk in the tab and registers the
// entries under a fresh generation.
func (b *Browser) collectBrowserRefs(ctx context.Context, profile, session string) ([]browserRefEntry, error) {
	key := browserRefKey(profile, session)
	raw, err := chromedp.Run(ctx, chromedp.Evaluate[string](browserSnapshotJS))
	if err != nil {
		return nil, err
	}
	var flat []struct {
		Role string `json:"role"`
		Name string `json:"name"`
		Sel  string `json:"sel"`
		Href string `json:"href"`
	}
	if err := json.Unmarshal([]byte(raw), &flat); err != nil {
		return nil, fmt.Errorf("snapshot decode failed: %w", err)
	}
	entries := make([]browserRefEntry, 0, len(flat))
	for i, f := range flat {
		name := f.Name
		if f.Href != "" && name == "" {
			name = f.Href
		}
		entries = append(entries, browserRefEntry{
			Ref:      fmt.Sprintf("@e%d", i+1),
			Role:     f.Role,
			Name:     name,
			Selector: f.Sel,
		})
	}
	b.refs.store(key, entries)
	return entries, nil
}

// renderRefTree formats the snapshot for the model: one line per element,
// ref + role + quoted name (+ selector for precise reuse).
func renderRefTree(entries []browserRefEntry) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Snapshot: %d elements (refs valid until navigation)\n", len(entries)))
	for _, e := range entries {
		line := fmt.Sprintf("%s %s %q", e.Ref, e.Role, e.Name)
		if len(line) > 160 {
			line = line[:160] + "…"
		}
		sb.WriteString(line + "\n")
	}
	return strings.TrimRight(sb.String(), "\n")
}

// browserClickPrecheckResult is the decoded verdict of precheckClickJS.
type browserClickPrecheckResult struct {
	OK     bool   `json:"ok"`
	Reason string `json:"reason"` // "covered" | "hidden" | "disabled" | "out-of-viewport" | "not-found"
	By     string `json:"by"`     // human description of the covering element (covered only)
}

// precheckClickJS scrolls the target into view, then fails EARLY when the
// click point is covered by another element (consent banner/modal), the
// target is hidden or disabled, or it lies outside the viewport — naming
// the covering element so the agent can dismiss it instead of looping on
// "clicked, nothing happened".
func precheckClickJS(selector string) string {
	return fmt.Sprintf(`(() => {
	const sel = %q;
	const el = document.querySelector(sel);
	if (!el) return JSON.stringify({ok:false, reason:'not-found'});
	el.scrollIntoView({block:'center'});
	const cs = getComputedStyle(el);
	const rect = el.getBoundingClientRect();
	if (cs.display === 'none' || cs.visibility === 'hidden' || (rect.width === 0 && rect.height === 0) ||
	    (el.offsetParent === null && cs.position !== 'fixed'))
		return JSON.stringify({ok:false, reason:'hidden'});
	if (el.disabled || el.getAttribute('aria-disabled') === 'true')
		return JSON.stringify({ok:false, reason:'disabled'});
	const vw = window.innerWidth, vh = window.innerHeight;
	const cx = rect.x + rect.width / 2, cy = rect.y + rect.height / 2;
	if (cx < 0 || cy < 0 || cx > vw || cy > vh)
		return JSON.stringify({ok:false, reason:'out-of-viewport'});
	const top = document.elementFromPoint(cx, cy);
	if (top && top !== el && !el.contains(top) && !top.contains(el)) {
		const describe = (n) => {
			const tag = n.tagName ? n.tagName.toLowerCase() : 'unknown';
			const id = n.id ? '#' + n.id : '';
			const cls = (typeof n.className === 'string' && n.className) ? '.' + n.className.trim().split(/\s+/).slice(0, 2).join('.') : '';
			const txt = (n.innerText || '').replace(/\s+/g, ' ').trim().slice(0, 60);
			return tag + id + cls + (txt ? ' ' + JSON.stringify(txt) : '');
		};
		return JSON.stringify({ok:false, reason:'covered', by: describe(top)});
	}
	return JSON.stringify({ok:true});
})()`, selector)
}

// runClickPrecheck executes the pre-check in the tab. A CDP-level error is
// NOT a block (the subsequent chromedp flow reports real failures); only a
// decoded ok:false verdict blocks.
func (b *Browser) runClickPrecheck(ctx context.Context, selector string) *browserClickPrecheckResult {
	raw, err := chromedp.Run(ctx, chromedp.Evaluate[string](precheckClickJS(selector)))
	if err != nil {
		return nil
	}
	var res browserClickPrecheckResult
	if err := json.Unmarshal([]byte(raw), &res); err != nil {
		return nil
	}
	return &res
}

// formatClickBlocked renders the actionable early-fail message.
func formatClickBlocked(selector string, res *browserClickPrecheckResult) string {
	if res == nil {
		return ""
	}
	switch res.Reason {
	case "covered":
		return fmt.Sprintf("click blocked: the click point on %q is covered by <%s> (consent banner/modal?). Dismiss or target the covering element first.", selector, res.By)
	case "hidden":
		return fmt.Sprintf("click blocked: %q is hidden (display:none / visibility:hidden / zero size). Re-snapshot or pick a visible element.", selector)
	case "disabled":
		return fmt.Sprintf("click blocked: %q is disabled. Wait for it to become enabled or pick another control.", selector)
	case "out-of-viewport":
		return fmt.Sprintf("click blocked: %q could not be scrolled into the viewport. It may live inside a closed container.", selector)
	case "not-found":
		return fmt.Sprintf("click blocked: %q matches no element. Re-snapshot (refs die on navigation).", selector)
	}
	return fmt.Sprintf("click blocked on %q (%s)", selector, res.Reason)
}
