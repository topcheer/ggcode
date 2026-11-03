package agent

import (
	"strings"
	"testing"
)

func TestFix3981QueryOnlyURLDiffSilent(t *testing.T) {
	base := "https://api.example.com/items?page=1&size=50"
	tr := newObservationContractTracker()
	tr.recordResult("web_fetch", "fetched "+base+" ok", 1)

	if h := tr.checkArgs("web_fetch", []byte(`{"url":"https://api.example.com/items?page=2&size=50"}`), 2); h != "" {
		t.Fatalf("query-only URL diff must be silent, got: %s", h)
	}

	if h := tr.checkArgs("web_fetch", []byte(`{"url":"https://api.example.com/items?page=10&size=50"}`), 3); h != "" {
		t.Fatalf("query-only URL diff must be silent, got: %s", h)
	}
	if h := tr.checkArgs("web_fetch", []byte(`{"url":"https://api.example.com/items?date=2026-01-02&size=50"}`), 4); h != "" {
		t.Fatalf("query-only URL diff must be silent, got: %s", h)
	}
}

func TestFix3981PathTypoStillWarns(t *testing.T) {

	tr := newObservationContractTracker()
	tr.recordResult("web_fetch", "fetched https://api.example.com/items/list?page=1 ok", 1)

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

	hint := tr.checkArgs("web_fetch", []byte(`{"url":"https://api.exemple.com/v2/data?page=1"}`), 2)
	if hint == "" {
		t.Fatal("expected integrity warning for host typo, got none")
	}
}

func TestFix3981ObsSameURLBaseSemantics(t *testing.T) {

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
