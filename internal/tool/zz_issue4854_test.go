package tool

import (
	"encoding/json"
	"strings"
	"testing"
)

// #4854: ref-based snapshot interaction + click validity pre-checks
// (agent-browser parity). These unit tests pin the pure surfaces: registry
// lifecycle (store/resolve/invalidate = ref stability until navigation),
// CSS passthrough back-compat, render format, and the blocked-click
// message shapes (covering element named).

func newTestBrowserRefs() *Browser {
	return &Browser{refs: newBrowserRefRegistry()}
}

func TestIssue4854_RefRegistryStoreResolveInvalidate(t *testing.T) {
	b := newTestBrowserRefs()
	key := browserRefKey("default", "default")
	if _, ok := b.refs.resolve(key, "@e1"); ok {
		t.Fatal("empty registry must not resolve refs")
	}
	b.refs.store(key, []browserRefEntry{
		{Ref: "@e1", Role: "link", Name: "Sign in", Selector: "body > a:nth-of-type(1)"},
		{Ref: "@e2", Role: "button", Name: "Submit", Selector: "body > button:nth-of-type(1)"},
	})
	got, ok := b.refs.resolve(key, "@e2")
	if !ok || got.Selector != "body > button:nth-of-type(1)" || got.Role != "button" {
		t.Fatalf("resolve @e2 = %+v ok=%v", got, ok)
	}
	// Ref stability across an in-page mutation (a later store bumps the
	// generation but refs keep working until NAVIGATION invalidates them).
	b.refs.store(key, []browserRefEntry{
		{Ref: "@e1", Role: "link", Name: "Sign in", Selector: "body > a:nth-of-type(1)"},
	})
	if _, ok := b.refs.resolve(key, "@e1"); !ok {
		t.Fatal("@e1 must resolve from the latest snapshot")
	}
	if _, ok := b.refs.resolve(key, "@e2"); ok {
		t.Fatal("@e2 belonged to the superseded snapshot - must not resolve")
	}
	// Navigation kills all refs for the tab.
	b.refs.invalidate(key)
	if b.refs.hasRefs(key) {
		t.Fatal("invalidate must drop the tab snapshot")
	}
}

func TestIssue4854_ResolveSelectorArgCSSPassthroughAndRefResolution(t *testing.T) {
	b := newTestBrowserRefs()
	// CSS selectors pass through untouched (back-compat contract).
	for _, css := range []string{"button.submit", "#login-form", "a[href*=\"github\"]", ""} {
		got, ok := b.resolveSelectorArg("default", "default", css)
		if !ok || got != css {
			t.Fatalf("CSS passthrough %q -> (%q,%v)", css, got, ok)
		}
	}
	// A ref with no snapshot -> actionable failure.
	if _, ok := b.resolveSelectorArg("default", "default", "@e3"); ok {
		t.Fatal("ref without snapshot must fail resolution")
	}
	b.refs.store(browserRefKey("default", "default"), []browserRefEntry{
		{Ref: "@e3", Role: "textbox", Name: "Email", Selector: "body > form > input:nth-of-type(2)"},
	})
	got, ok := b.resolveSelectorArg("default", "default", "@e3")
	if !ok || got != "body > form > input:nth-of-type(2)" {
		t.Fatalf("@e3 resolution = (%q,%v)", got, ok)
	}
	// Another tab's snapshot must not leak into this tab.
	if _, ok := b.resolveSelectorArg("default", "other-tab", "@e3"); ok {
		t.Fatal("refs are per-tab; cross-tab resolution must fail")
	}
}

func TestIssue4854_RenderRefTree(t *testing.T) {
	out := renderRefTree([]browserRefEntry{
		{Ref: "@e1", Role: "link", Name: "Docs", Selector: "body > a:nth-of-type(1)"},
		{Ref: "@e2", Role: "button", Name: "Get started", Selector: "body > button"},
	})
	if !strings.Contains(out, "Snapshot: 2 elements (refs valid until navigation)") {
		t.Fatalf("header missing: %q", out)
	}
	if !strings.Contains(out, `@e1 link "Docs"`) || !strings.Contains(out, `@e2 button "Get started"`) {
		t.Fatalf("ref lines malformed:\n%s", out)
	}
}

func TestIssue4854_FormatClickBlockedNamesCoveringElement(t *testing.T) {
	cases := []struct {
		res  browserClickPrecheckResult
		want string
	}{
		{browserClickPrecheckResult{OK: false, Reason: "covered", By: `div#consent.banner "Accept all cookies?"`}, "covered by <div#consent.banner"},
		{browserClickPrecheckResult{OK: false, Reason: "hidden"}, "hidden (display:none"},
		{browserClickPrecheckResult{OK: false, Reason: "disabled"}, "disabled"},
		{browserClickPrecheckResult{OK: false, Reason: "out-of-viewport"}, "could not be scrolled into the viewport"},
		{browserClickPrecheckResult{OK: false, Reason: "not-found"}, "matches no element"},
	}
	for i, c := range cases {
		msg := formatClickBlocked("button.go", &c.res)
		if !strings.Contains(msg, c.want) {
			t.Fatalf("case %d: %q missing %q", i, msg, c.want)
		}
	}
	if formatClickBlocked("x", nil) != "" {
		t.Fatal("nil verdict must format empty (no block on probe error)")
	}
}

func TestIssue4854_PrecheckJSVerdictDecode(t *testing.T) {
	// The pre-check JS returns a JSON string; decode mirrors the runtime path.
	verdict := `{"ok":false,"reason":"covered","by":"div#overlay \"Accept\""}`
	var res browserClickPrecheckResult
	if err := json.Unmarshal([]byte(verdict), &res); err != nil {
		t.Fatal(err)
	}
	if res.OK || res.Reason != "covered" || !strings.Contains(res.By, "div#overlay") {
		t.Fatalf("decode = %+v", res)
	}
	if !strings.Contains(precheckClickJS("button.go"), `"button.go"`) {
		t.Fatal("precheck JS must embed the selector")
	}
	if !strings.Contains(browserSnapshotJS, "nth-of-type") {
		t.Fatal("snapshot JS must build structural CSS paths")
	}
}
