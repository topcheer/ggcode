package agent

import (
	"testing"
)

func TestCheckTyposquatting_NPMAlert(t *testing.T) {
	oldPkg := `{
  "dependencies": {}
}`
	newPkg := `{
  "dependencies": {
    "lodahs": "^4.17.21"
  }
}`
	warnings := checkTyposquatting("package.json", oldPkg, newPkg)
	if len(warnings) == 0 {
		t.Fatal("expected typosquat warning for 'lodahs' vs 'lodash'")
	}
	found := false
	for _, w := range warnings {
		if containsAny(w, "lodahs", "lodash") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("warning should mention 'lodahs' or 'lodash': %v", warnings)
	}
}

func TestCheckTyposquatting_ExactMatchNoAlert(t *testing.T) {
	oldPkg := `{
  "dependencies": {}
}`
	newPkg := `{
  "dependencies": {
    "lodash": "^4.17.21"
  }
}`
	warnings := checkTyposquatting("package.json", oldPkg, newPkg)
	if len(warnings) != 0 {
		t.Fatalf("exact match should not trigger: %v", warnings)
	}
}

func TestCheckTyposquatting_PythonTyposquat(t *testing.T) {
	oldReq := ``
	newReq := `requets==2.31.0`
	warnings := checkTyposquatting("requirements.txt", oldReq, newReq)
	if len(warnings) == 0 {
		t.Fatal("expected typosquat warning for 'requets' vs 'requests'")
	}
}

func TestCheckTyposquatting_DeltaAware(t *testing.T) {
	oldPkg := `{
  "dependencies": {
    "lodahs": "^1.0.0"
  }
}`
	newPkg := `{
  "dependencies": {
    "lodahs": "^2.0.0"
  }
}`
	warnings := checkTyposquatting("package.json", oldPkg, newPkg)
	// 'lodahs' existed before, just changed version - no new typosquat
	if len(warnings) != 0 {
		t.Fatalf("pre-existing typosquat should not re-alert: %v", warnings)
	}
}

func TestCheckTyposquatting_NonManifestFile(t *testing.T) {
	warnings := checkTyposquatting("main.go", "", `import "lodahs"`)
	if len(warnings) != 0 {
		t.Fatalf("non-manifest file should not trigger: %v", warnings)
	}
}

func TestCheckTyposquatting_GoModule(t *testing.T) {
	oldMod := `module example.com/app
go 1.21
`
	newMod := `module example.com/app
go 1.21
require (
	github.com/spf13/cobra v1.9.1
	github.com/testfy v1.9.0
)`
	warnings := checkTyposquatting("go.mod", oldMod, newMod)
	if len(warnings) == 0 {
		t.Fatal("expected typosquat warning for 'testfy' vs 'testify'")
	}
}

func TestLevenshtein(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"", "abc", 3},
		{"abc", "", 3},
		{"abc", "abc", 0},
		{"cat", "cut", 1},
		{"kitten", "sitting", 3},
		{"flaw", "lawn", 2},
		{"lodahs", "lodash", 2}, // transposition = distance 2
	}
	for _, tc := range cases {
		got := levenshtein(tc.a, tc.b)
		if got != tc.want {
			t.Errorf("levenshtein(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestExtractPackageName_Go(t *testing.T) {
	got := extractPackageName("go", "github.com/gin-gonic/gin")
	if got != "gin" {
		t.Errorf("expected 'gin', got %q", got)
	}
}

func TestExtractPackageName_NPM(t *testing.T) {
	got := extractPackageName("npm", "lodash")
	if got != "lodash" {
		t.Errorf("expected 'lodash', got %q", got)
	}
}
