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

type browserRefEntry struct {
	Ref      string `json:"ref"`
	Role     string `json:"role"`
	Name     string `json:"name"`
	Selector string `json:"selector"`
}

type browserRefSnapshot struct {
	Generation int
	Entries    []browserRefEntry
	At         time.Time
}

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

func (r *browserRefRegistry) store(key string, entries []browserRefEntry) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.gen++
	r.snaps[key] = &browserRefSnapshot{Generation: r.gen, Entries: entries, At: time.Now()}
}

func (r *browserRefRegistry) invalidate(key string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.snaps, key)
}

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

func (r *browserRefRegistry) hasRefs(key string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.snaps[key]
	return ok
}

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

// D2 (#5209 design §1, verdict ① CONFIRM): layered snapshot budgets.
// Top-level traversal: min(wait_timeout default 3s, 8s hard cap) - enforced
// by the Go-side context. Iframe collection: 1s shared budget across all
// same-origin iframes, 500ms per child frame - enforced inside the probe JS
// (a single Evaluate cannot be interrupted mid-flight by ctx, so the JS
// self-limits with Date.now()). Timed-out frames yield ZERO entries (no
// @eN refs for partially collected subtrees) and the rendered snapshot
// carries an explicit "[timeout: budget exhausted]" marker: partial results
// beat total failure, but they must never masquerade as complete.
const (
	snapshotTopLevelDefault = 3 * time.Second
	snapshotTopLevelHardCap = 8 * time.Second
)

// snapshotTopLevelBudget derives the top-level snapshot deadline from the
// user-supplied wait_timeout (seconds; 0 or negative -> default). The hard
// cap clamps any user request.
func snapshotTopLevelBudget(waitTimeoutSec int) time.Duration {
	d := snapshotTopLevelDefault
	if waitTimeoutSec > 0 {
		d = time.Duration(waitTimeoutSec) * time.Second
	}
	if d > snapshotTopLevelHardCap {
		d = snapshotTopLevelHardCap
	}
	return d
}

const browserSnapshotJS = `(() => {
	const out = [];
	const cap = 200;
	const interactive = 'a,button,input,textarea,select,[role],[onclick],[tabindex],label,h1,h2,h3,h4,h5,h6,img,summary,details,video,audio,canvas,svg[role]';
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
	const cssPath = (el, rootTag) => {
		const parts = [];
		let cur = el, depth = 0;
		while (cur && cur.nodeType === 1 && depth < 8) {
			const tag = cur.tagName.toLowerCase();
			if (tag === 'html' || tag === 'body') { parts.unshift(rootTag || tag); break; }
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
	const collectInto = (doc, label) => {
		let n = 0;
		if (!doc) return n;
		const nodes = doc.querySelectorAll(interactive);
		for (const el of nodes) {
			if (out.length >= cap) break;
			const cs = (doc.defaultView && doc.defaultView.getComputedStyle) ? doc.defaultView.getComputedStyle(el) : getComputedStyle(el);
			if (cs.display === 'none' || cs.visibility === 'hidden') continue;
			const rect = el.getBoundingClientRect();
			if (rect.width === 0 && rect.height === 0) continue;
			const href = el.tagName.toLowerCase() === 'a' ? (el.getAttribute('href') || '') : '';
			const p = cssPath(el, label);
			out.push({ role: roleName(el), name: nameOf(el), sel: (label ? label + ' ' : '') + p, href: href.slice(0, 200) });
			n++;
		}
		return n;
	};
	// Top-level document: always collected (fast, ctx-bounded by the caller).
	collectInto(document, '');
	// Iframe collection under a shared 1s budget, 500ms per child frame.
	// Timed-out frames yield zero entries and are reported, not raised.
	const IFRAME_TOTAL_MS = 1000, IFRAME_CHILD_MS = 500;
	const iframeStart = Date.now();
	const frames = [];
	let timedOut = false;
	const ifr = document.querySelectorAll('iframe');
	for (let i = 0; i < ifr.length; i++) {
		const remainingTotal = iframeStart + IFRAME_TOTAL_MS - Date.now();
		if (remainingTotal <= 50) { frames.push({idx: i, ok: false, timeout: true}); timedOut = true; continue; }
		const childDeadline = Date.now() + Math.min(remainingTotal, IFRAME_CHILD_MS);
		let fdoc = null;
		try { fdoc = ifr[i].contentDocument; } catch (e) { fdoc = null; }
		if (!fdoc || !fdoc.body) { frames.push({idx: i, ok: false, timeout: false, reason: 'cross-origin-or-empty'}); continue; }
		// Approximate per-child budget guard: a frame whose body cannot even be
		// enumerated within its slice is treated as timed out. DOM enumeration is
		// synchronous; the deadline check bounds pathological trees by bailing
		// before entering heavy subtrees once the slice is spent.
		if (Date.now() > childDeadline) { frames.push({idx: i, ok: false, timeout: true}); timedOut = true; continue; }
		const label = 'iframe:nth-of-type(' + (i + 1) + ')';
		const before = out.length;
		collectInto(fdoc, label);
		// If the cap was hit mid-frame the frame is partial: report it, and
		// treat only genuinely budget-driven skips as timeouts.
		if (out.length >= cap && before < cap) {
			frames.push({idx: i, ok: true, partial: true});
		} else {
			frames.push({idx: i, ok: true});
		}
		if (Date.now() > childDeadline) { /* collected within slice */ }
	}
	return JSON.stringify({ entries: out, frames: frames, timedOut: timedOut });
})()`

// collectBrowserRefsForPayload decodes a snapshot probe payload into ref
// entries. Exposed for unit tests (acceptance ②: refs are minted strictly
// from decoded entries - a timed-out frame contributes no entry, hence no
// ref, by construction).
func (b *Browser) collectBrowserRefsForPayload(raw string) ([]browserRefEntry, bool, error) {
	var payload struct {
		Entries []struct {
			Role string `json:"role"`
			Name string `json:"name"`
			Sel  string `json:"sel"`
			Href string `json:"href"`
		} `json:"entries"`
		Frames []struct {
			Idx     int  `json:"idx"`
			OK      bool `json:"ok"`
			Timeout bool `json:"timeout"`
		} `json:"frames"`
		TimedOut bool `json:"timedOut"`
	}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return nil, false, fmt.Errorf("snapshot decode failed: %w", err)
	}
	entries := make([]browserRefEntry, 0, len(payload.Entries))
	for i, f := range payload.Entries {
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
	return entries, payload.TimedOut, nil
}

func (b *Browser) collectBrowserRefs(ctx context.Context, profile, session string) ([]browserRefEntry, bool, error) {
	raw, err := chromedp.Run(ctx, chromedp.Evaluate[string](browserSnapshotJS))
	if err != nil {
		return nil, false, err
	}
	entries, timedOut, err := b.collectBrowserRefsForPayload(raw)
	if err != nil {
		return nil, false, err
	}
	b.refs.store(browserRefKey(profile, session), entries)
	return entries, timedOut, nil
}

// snapshotTimeoutMarker is the explicit truncation marker required by the
// D2 verdict acceptance assertion ①: when any iframe budget is exhausted
// the rendered snapshot must carry this exact line.
const snapshotTimeoutMarker = "[timeout: budget exhausted]"

// renderSnapshotOutput assembles the full snapshot tool output, including
// the truncation marker when frame budgets were exhausted (acceptance ①),
// and the standing ref-usage footer.
func renderSnapshotOutput(entries []browserRefEntry, timedOut bool) string {
	out := renderRefTree(entries)
	if timedOut {
		out += "\n" + snapshotTimeoutMarker + " some iframes were skipped; elements inside them have NO refs"
	}
	return out + "\n\nUse a ref (e.g. '@e3') in the selector parameter of click/type/extract/select/hover/upload/scroll. Refs stay valid until this tab navigates."
}

func renderRefTree(entries []browserRefEntry) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Snapshot: %d elements (refs valid until navigation)\n", len(entries)))
	for _, e := range entries {
		line := fmt.Sprintf("%s %s %q", e.Ref, e.Role, e.Name)
		if len(line) > 160 {

			trunc := []rune(line)
			for len(string(trunc))+len("…") > 161 {
				trunc = trunc[:len(trunc)-1]
			}
			line = string(trunc) + "…"
		}
		sb.WriteString(line + "\n")
	}
	return strings.TrimRight(sb.String(), "\n")
}

type browserClickPrecheckResult struct {
	OK     bool   `json:"ok"`
	Reason string `json:"reason"`
	By     string `json:"by"`
}

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
