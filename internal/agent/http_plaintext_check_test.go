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
