package agent

import (
	"strings"
	"testing"
)

func TestCheckHTTPPlaintext_NewURL(t *testing.T) {
	oldContent := `package main
func main() {}
`
	newContent := `package main
func fetch() {
	resp, _ := http.Get("http://api.example.com/data")
	_ = resp
}`
	warnings := checkHTTPPlaintext("main.go", oldContent, newContent)
	if len(warnings) == 0 {
		t.Fatal("expected HTTP plaintext warning")
	}
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "api.example.com") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("warning should mention the host: %v", warnings)
	}
}

func TestCheckHTTPPlaintext_LocalhostExempt(t *testing.T) {
	oldContent := ``
	newContent := `const url = "http://localhost:8080/api"`
	warnings := checkHTTPPlaintext("config.js", oldContent, newContent)
	if len(warnings) != 0 {
		t.Fatalf("localhost should be exempt: %v", warnings)
	}
}

// TestCheckHTTPPlaintext_Issue3536 covers the loopback whitelist widening:
// *.localhost subdomains (RFC 6761 §6.3) and the full 127.0.0.0/8 range
// (RFC 1122 §3.2.1.3) are local dev addresses and must not trigger the
// SeverityCritical plaintext-HTTP guidance.
func TestCheckHTTPPlaintext_Issue3536(t *testing.T) {
	exempt := []string{
		`const url = "http://app.localhost:3000/api"`,    // *.localhost (Vite style)
		`const url = "http://api.localhost:3000"`,        // another subdomain
		`const url = "http://sub.deep.localhost:8080/x"`, // nested depth
		`url = "http://127.0.0.2:8080/health"`,           // 127.0.0.0/8
		`url = "http://127.1.2.3/x"`,                     // far into the /8
		`url = "http://LOCALHOST:8080/x"`,                // case-insensitive exact
		`url = "http://APP.LOCALHOST:3000/x"`,            // case-insensitive suffix
	}
	for _, nc := range exempt {
		if warnings := checkHTTPPlaintext("config.js", "", nc); len(warnings) != 0 {
			t.Errorf("local dev address should be exempt (%q): %v", nc, warnings)
		}
	}

	flagged := []string{
		`const url = "http://evil.localhost.example.com/x"`, // .localhost must be a suffix, not infix
		`const url = "http://localhost.evil.com/x"`,         // subdomain OF someone else
		`url = "http://127.0.0.256/x"`,                      // invalid IP literal
		`url = "http://api.example.com/x"`,                  // plain public host
	}
	for _, nc := range flagged {
		if warnings := checkHTTPPlaintext("config.js", "", nc); len(warnings) == 0 {
			t.Errorf("public/invalid host should still be flagged: %q", nc)
		}
	}
}

func TestIsLocalhost_Issue3536(t *testing.T) {
	for _, host := range []string{"app.localhost", "127.0.0.2", "127.255.255.254", "::1", "0.0.0.0"} {
		if !isLocalhost(host) {
			t.Errorf("isLocalhost(%q) = false, want true", host)
		}
	}
	for _, host := range []string{"evil.localhost.example.com", "localhost.evil.com", "127.0.0.256", "example.com"} {
		if isLocalhost(host) {
			t.Errorf("isLocalhost(%q) = true, want false", host)
		}
	}
}

func TestCheckHTTPPlaintext_127Exempt(t *testing.T) {
	oldContent := ``
	newContent := `url = "http://127.0.0.1:3000"`
	warnings := checkHTTPPlaintext("app.py", oldContent, newContent)
	if len(warnings) != 0 {
		t.Fatalf("127.0.0.1 should be exempt: %v", warnings)
	}
}

func TestCheckHTTPPlaintext_HTTPSNotFlagged(t *testing.T) {
	oldContent := ``
	newContent := `const url = "https://api.example.com/data"`
	warnings := checkHTTPPlaintext("config.js", oldContent, newContent)
	if len(warnings) != 0 {
		t.Fatalf("https should not be flagged: %v", warnings)
	}
}

func TestCheckHTTPPlaintext_DeltaAware(t *testing.T) {
	oldContent := `const url = "http://api.example.com/old"`
	newContent := `const url = "http://api.example.com/new"`
	warnings := checkHTTPPlaintext("config.js", oldContent, newContent)
	// Host 'api.example.com' existed before, so no new warning
	if len(warnings) != 0 {
		t.Fatalf("pre-existing host should not re-alert: %v", warnings)
	}
}

func TestCheckHTTPPlaintext_EmptyContent(t *testing.T) {
	warnings := checkHTTPPlaintext("main.go", "", "")
	if len(warnings) != 0 {
		t.Fatalf("empty content should not trigger: %v", warnings)
	}
}

func TestCheckHTTPPlaintext_MaxWarnings(t *testing.T) {
	oldContent := ``
	newContent := `urls := []string{
		"http://a.example.com",
		"http://b.example.com",
		"http://c.example.com",
		"http://d.example.com",
	}`
	warnings := checkHTTPPlaintext("main.go", oldContent, newContent)
	if len(warnings) > maxPlaintextWarnings {
		t.Fatalf("should cap at %d warnings, got %d", maxPlaintextWarnings, len(warnings))
	}
}
