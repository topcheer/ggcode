package agent

import (
	"strings"
	"testing"
)

func TestCheckHTTPTimeout_NonGoFile(t *testing.T) {
	warnings := checkHTTPTimeout("main.py", "", "import requests\nrequests.get('http://example.com')\n")
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings for non-Go file, got %d", len(warnings))
	}
}

func TestCheckHTTPTimeout_EmptyContent(t *testing.T) {
	warnings := checkHTTPTimeout("main.go", "", "")
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings for empty content, got %d", len(warnings))
	}
}

func TestCheckHTTPTimeout_SyntaxError(t *testing.T) {
	warnings := checkHTTPTimeout("main.go", "", "package main\n\nfunc broken(\n")
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings for unparseable file, got %d", len(warnings))
	}
}

func TestCheckHTTPTimeout_DefaultClientFuncs(t *testing.T) {
	src := `package main

import "net/http"

func fetch() {
	resp, err := http.Get("http://example.com")
	_ = err
	defer resp.Body.Close()
	_ = resp
}

func post() {
	resp, err := http.Post("http://example.com", "text/plain", nil)
	defer resp.Body.Close()
	_ = resp
	_ = err
}
`
	warnings := checkHTTPTimeout("main.go", "", src)
	if len(warnings) != 2 {
		t.Fatalf("expected 2 warnings (http.Get + http.Post), got %d: %v", len(warnings), warnings)
	}
	joined := strings.Join(warnings, " ")
	if !strings.Contains(joined, "http.Get") {
		t.Errorf("expected warning about http.Get, got: %s", joined)
	}
	if !strings.Contains(joined, "http.Post") {
		t.Errorf("expected warning about http.Post, got: %s", joined)
	}
}

func TestCheckHTTPTimeout_DefaultClientExplicit(t *testing.T) {
	src := `package main

import "net/http"

func fetch() {
	resp, err := http.DefaultClient.Get("http://example.com")
	defer resp.Body.Close()
	_ = resp
	_ = err
}
`
	warnings := checkHTTPTimeout("main.go", "", src)
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning for http.DefaultClient.Get, got %d: %v", len(warnings), warnings)
	}
	if !strings.Contains(warnings[0], "DefaultClient") {
		t.Errorf("expected warning about DefaultClient, got: %s", warnings[0])
	}
}

func TestCheckHTTPTimeout_ClientNoTimeout(t *testing.T) {
	src := `package main

import "net/http"

func fetch() {
	client := &http.Client{}
	resp, err := client.Get("http://example.com")
	defer resp.Body.Close()
	_ = resp
	_ = err
}
`
	warnings := checkHTTPTimeout("main.go", "", src)
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning for http.Client without Timeout, got %d: %v", len(warnings), warnings)
	}
	if !strings.Contains(warnings[0], "Timeout") {
		t.Errorf("expected warning mentioning Timeout field, got: %s", warnings[0])
	}
}

func TestCheckHTTPTimeout_ClientWithTimeout(t *testing.T) {
	src := `package main

import (
	"net/http"
	"time"
)

func fetch() {
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get("http://example.com")
	defer resp.Body.Close()
	_ = resp
	_ = err
}
`
	warnings := checkHTTPTimeout("main.go", "", src)
	// http.Client has a Timeout field, so no warning for the client.
	// The client.Get call itself is fine since the client has a timeout.
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings for http.Client with Timeout, got %d: %v", len(warnings), warnings)
	}
}

func TestCheckHTTPTimeout_ClientWithTimeoutAndZeroValue(t *testing.T) {
	src := `package main

import (
	"net/http"
	"time"
)

func fetch() {
	client := &http.Client{Timeout: 0 * time.Second}
	_ = client
}
`
	warnings := checkHTTPTimeout("main.go", "", src)
	// Has Timeout field = 0, but we only check field presence, not value.
	// This is acceptable: 0 means "no timeout" but presence means the developer
	// made a conscious choice. We don't flag it to avoid false positives.
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings when Timeout field present (even if 0), got %d: %v", len(warnings), warnings)
	}
}

func TestCheckHTTPTimeout_AllThreePatterns(t *testing.T) {
	src := `package main

import "net/http"

func fetch1() {
	http.Get("http://example.com")
}

func fetch2() {
	http.DefaultClient.Get("http://example.com")
}

func fetch3() {
	c := &http.Client{}
	c.Get("http://example.com")
}
`
	warnings := checkHTTPTimeout("main.go", "", src)
	if len(warnings) != 3 {
		t.Fatalf("expected 3 warnings for all three patterns, got %d: %v", len(warnings), warnings)
	}
	joined := strings.Join(warnings, " ")
	if !strings.Contains(joined, "http.Get") {
		t.Errorf("missing http.Get warning")
	}
	if !strings.Contains(joined, "DefaultClient") {
		t.Errorf("missing DefaultClient warning")
	}
	if !strings.Contains(joined, "http.Client{}") {
		t.Errorf("missing http.Client{} warning")
	}
}

func TestCheckHTTPTimeout_DeltaAware(t *testing.T) {
	// Old content already has http.Get (pre-existing issue).
	oldSrc := `package main

import "net/http"

func existing() {
	http.Get("http://example.com")
}
`
	// New content adds http.Post (new issue) on top of existing.
	newSrc := `package main

import "net/http"

func existing() {
	http.Get("http://example.com")
}

func added() {
	http.Post("http://example.com", "text/plain", nil)
}
`
	warnings := checkHTTPTimeout("main.go", oldSrc, newSrc)
	// Should only flag the NEW http.Post, not the pre-existing http.Get.
	if len(warnings) != 1 {
		t.Fatalf("expected 1 delta-filtered warning (http.Post only), got %d: %v", len(warnings), warnings)
	}
	if !strings.Contains(warnings[0], "http.Post") {
		t.Errorf("expected warning about http.Post (new), got: %s", warnings[0])
	}
}

func TestCheckHTTPTimeout_NewFile(t *testing.T) {
	src := `package main

import "net/http"

func main() {
	http.Get("http://example.com")
}
`
	// oldContent = "" means new file, all patterns are new.
	warnings := checkHTTPTimeout("main.go", "", src)
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning for new file, got %d: %v", len(warnings), warnings)
	}
}

func TestCheckHTTPTimeout_NoHTTPUsage(t *testing.T) {
	src := `package main

import "fmt"

func main() {
	fmt.Println("hello")
}
`
	warnings := checkHTTPTimeout("main.go", "", src)
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings for non-HTTP code, got %d: %v", len(warnings), warnings)
	}
}

func TestCheckHTTPTimeout_NotConfusedBySimilarNames(t *testing.T) {
	// myhttp.Get should NOT be flagged (only net/http package).
	src := `package main

type myhttp struct{}

func (myhttp) Get(url string) {}

func main() {
	var m myhttp
	m.Get("http://example.com")
}
`
	warnings := checkHTTPTimeout("main.go", "", src)
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings for non-http package, got %d: %v", len(warnings), warnings)
	}
}
