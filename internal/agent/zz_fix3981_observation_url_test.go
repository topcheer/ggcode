package agent

import (
	"strings"
	"testing"
)

// #3981: URL integrity near-match must be query-aware.
// Query-only differences (pagination page=1→2, date windows, ids) are
// intentional mutations and must NOT trigger the mistranscription hint;
// host/path-level typos must still be caught.

func TestFix3981QueryOnlyURLDiffSilent(t *testing.T) {
	base := "https://api.example.com/items?page=1&size=50"
	tr := newObservationContractTracker()
	tr.recordResult("web_fetch", "fetched "+base+" ok", 1)

	// Pagination: page=1 → page=2 (d=1) — must stay silent.
	if h := tr.checkArgs("web_fetch", []byte(`{"url":"https://api.example.com/items?page=2&size=50"}`), 2); h != "" {
		t.Fatalf("query-only URL diff must be silent, got: %s", h)
	}
	// Deeper pagination and a different date window, same base.
	if h := tr.checkArgs("web_fetch", []byte(`{"url":"https://api.example.com/items?page=10&size=50"}`), 3); h != "" {
		t.Fatalf("query-only URL diff must be silent, got: %s", h)
	}
	if h := tr.checkArgs("web_fetch", []byte(`{"url":"https://api.example.com/items?date=2026-01-02&size=50"}`), 4); h != "" {
		t.Fatalf("query-only URL diff must be silent, got: %s", h)
	}
}

func TestFix3981PathTypoStillWarns(t *testing.T) {
	// obsURLRe only captures URLs that carry a ?query, so the typo case
	// needs one to enter the artifact pool at all.
	tr := newObservationContractTracker()
	tr.recordResult("web_fetch", "fetched https://api.example.com/items/list?page=1 ok", 1)

	// Path typo: /items/list → /items/lisp (d=1), query identical — must warn.
	hint := tr.checkArgs("web_fetch", []byte(`{"url":"https://api.example.com/items/lisp?page=1"}`), 2)
	if hint == "" {
		t.Fatal("expected integrity warning for 1-char path typo, got none")
	}
	if !strings.Contains(hint, "[observation-contract]") || !strings.Contains(hint, "mistranscribed") {
		t.Fatalf("hint malformed: %s", hint)
	}
}

func TestFix3981HostTypoStillWarns(t *testing.T) {
	tr := newObservationContractTracker()
	tr.recordResult("web_fetch", "fetched https://api.example.com/v2/data?page=1 ok", 1)

	// Host typo: api.example.com → api.exemple.com (d=1) with same path and
	// query — different host means the query-only exemption does not apply.
	hint := tr.checkArgs("web_fetch", []byte(`{"url":"https://api.exemple.com/v2/data?page=1"}`), 2)
	if hint == "" {
		t.Fatal("expected integrity warning for host typo, got none")
	}
}

func TestFix3981ObsSameURLBaseSemantics(t *testing.T) {
	// http vs https with identical host/path: scheme difference must not
	// count as same base (obsSameURLBase requires identical schemes).
	if obsSameURLBase("https://api.example.com/items", "http://api.example.com/items") {
		t.Fatal("scheme difference must not count as same URL base")
	}
	if !obsSameURLBase("https://a.io/x?k=1", "https://a.io/x?k=2") {
		t.Fatal("query-only difference must count as same URL base")
	}
	if obsSameURLBase("https://a.io/x", "https://a.io/y") {
		t.Fatal("path difference must not count as same URL base")
	}
	if obsSameURLBase("https://a.io/x", "://not a url") {
		t.Fatal("unparseable input must not count as same URL base")
	}
}
