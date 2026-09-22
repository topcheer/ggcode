package tool

import (
	"strings"
	"testing"
)

func depsDiff(files ...string) string {
	return strings.Join(files, "\n")
}

func TestBuildDependencySummary_NoManifests(t *testing.T) {
	diff := depsDiff(`diff --git a/main.go b/main.go
--- a/main.go
+++ b/main.go
@@ -1,2 +1,3 @@
+x := 1
`)
	files := parseReviewDiff(diff)
	if got := buildDependencySummary(files); got != "" {
		t.Errorf("expected empty summary for non-manifest diff, got:\n%s", got)
	}
}

func TestBuildDependencySummary_GoMod(t *testing.T) {
	diff := depsDiff(`diff --git a/go.mod b/go.mod
--- a/go.mod
+++ b/go.mod
@@ -5,6 +5,9 @@
 require (
 	github.com/unchanged/pkg v1.0.0
-	github.com/old/pkg v1.2.0
+	github.com/old/pkg v2.0.0
+	github.com/newdep/thing v0.4.1
 )
`)
	files := parseReviewDiff(diff)
	got := buildDependencySummary(files)
	if got == "" {
		t.Fatal("expected non-empty summary for go.mod diff")
	}
	if !strings.Contains(got, "github.com/old/pkg v1.2.0") || !strings.Contains(got, "v2.0.0") {
		t.Errorf("expected changed package line, got:\n%s", got)
	}
	if !strings.Contains(got, "MAJOR version jump") {
		t.Errorf("expected major jump callout for v1->v2, got:\n%s", got)
	}
	if !strings.Contains(got, "github.com/newdep/thing v0.4.1 (added)") {
		t.Errorf("expected added package line, got:\n%s", got)
	}
	if strings.Contains(got, "unchanged") {
		t.Errorf("unchanged package should not be reported, got:\n%s", got)
	}
	if !strings.HasPrefix(got, "DEPENDENCIES") {
		t.Errorf("summary should start with DEPENDENCIES header, got:\n%s", got)
	}
}

func TestBuildDependencySummary_PackageJSON(t *testing.T) {
	diff := depsDiff(`diff --git a/app/package.json b/app/package.json
--- a/app/package.json
+++ b/app/package.json
@@ -3,7 +3,9 @@
   "name": "app",
-  "version": "1.0.0",
+  "version": "1.1.0",
   "dependencies": {
-    "left-pad": "^1.3.0",
+    "left-pad": "^2.0.0",
+    "zod": "^3.22.0",
+    "scripts-holder": "note the build script below stays out",
     "react": "^18.2.0"
   }
`)
	files := parseReviewDiff(diff)
	got := buildDependencySummary(files)
	if got == "" {
		t.Fatal("expected non-empty summary for package.json diff")
	}
	if !strings.Contains(got, "left-pad ^1.3.0") || !strings.Contains(got, "^2.0.0") {
		t.Errorf("expected changed dep, got:\n%s", got)
	}
	if !strings.Contains(got, "MAJOR version jump") {
		t.Errorf("expected major jump callout, got:\n%s", got)
	}
	if !strings.Contains(got, "zod ^3.22.0 (added)") {
		t.Errorf("expected added dep zod, got:\n%s", got)
	}
	// "version": "1.0.0" -> "1.1.0" is a manifest metadata key, not a dependency.
	if strings.Contains(got, "version 1.0.0") || strings.Contains(got, "version 1.1.0") {
		t.Errorf("manifest version key should be excluded, got:\n%s", got)
	}
	// build script values are not version constraints.
	if strings.Contains(got, "scripts-holder") {
		t.Errorf("non-version value should be excluded, got:\n%s", got)
	}
	if strings.Contains(got, "react") {
		t.Errorf("unchanged dep should not be reported, got:\n%s", got)
	}
}

func TestBuildDependencySummary_RequirementsTxt(t *testing.T) {
	diff := depsDiff(`diff --git a/requirements.txt b/requirements.txt
--- a/requirements.txt
+++ b/requirements.txt
@@ -1,3 +1,4 @@
-flask==2.0.1
+flask==3.0.0
+requests==2.31.0
# comment stays out
`)
	files := parseReviewDiff(diff)
	got := buildDependencySummary(files)
	if !strings.Contains(got, "flask ==2.0.1") || !strings.Contains(got, "-> ==3.0.0") {
		t.Errorf("expected flask change, got:\n%s", got)
	}
	if !strings.Contains(got, "requests ==2.31.0 (added)") {
		t.Errorf("expected requests added, got:\n%s", got)
	}
	if strings.Contains(got, "comment") {
		t.Errorf("comment should not be parsed, got:\n%s", got)
	}
}

func TestBuildDependencySummary_CargoAndGemfile(t *testing.T) {
	files := parseReviewDiff(depsDiff(
		`diff --git a/Cargo.toml b/Cargo.toml
--- a/Cargo.toml
+++ b/Cargo.toml
@@ -1,5 +1,6 @@
 [package]
 name = "demo"
+version = "0.2.0"
 [dependencies]
-serde = "1.0"
+serde = "2.1"
+rand = "0.8"
`))
	got := buildDependencySummary(files)
	if !strings.Contains(got, "serde 1.0") || !strings.Contains(got, "-> 2.1") {
		t.Errorf("expected serde change, got:\n%s", got)
	}
	if !strings.Contains(got, "rand 0.8 (added)") {
		t.Errorf("expected rand added, got:\n%s", got)
	}
	// [package] section name/version must not leak as dependencies.
	if strings.Contains(got, "demo") {
		t.Errorf("[package] name should be excluded, got:\n%s", got)
	}
	if strings.Contains(got, "version \"0.2.0\"") || strings.Contains(got, "version 0.2.0") {
		t.Errorf("[package] version should be excluded, got:\n%s", got)
	}

	gemFiles := parseReviewDiff(`diff --git a/Gemfile b/Gemfile
--- a/Gemfile
+++ b/Gemfile
@@ -1,2 +1,3 @@
 gem "rails", "7.0"
+gem "pg", "1.5"
`)
	gemGot := buildDependencySummary(gemFiles)
	if !strings.Contains(gemGot, "pg 1.5 (added)") {
		t.Errorf("expected gem pg added, got:\n%s", gemGot)
	}
	if strings.Contains(gemGot, "rails") {
		t.Errorf("unchanged gem should not be reported, got:\n%s", gemGot)
	}
}

func TestBuildDependencySummary_RemovedOnly(t *testing.T) {
	diff := depsDiff(`diff --git a/go.mod b/go.mod
--- a/go.mod
+++ b/go.mod
@@ -5,5 +5,4 @@
 require (
-	github.com/dropped/pkg v0.1.0
 	github.com/kept/pkg v1.0.0
 )
`)
	files := parseReviewDiff(diff)
	got := buildDependencySummary(files)
	if !strings.Contains(got, "github.com/dropped/pkg v0.1.0 (removed)") {
		t.Errorf("expected removed line, got:\n%s", got)
	}
}

func TestDepMajor(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"v1.2.3", 1},
		{"^2.0.0", 2},
		{"~1.4", 1},
		{">=3.0", 3},
		{"==0.9", 0},
		{"1.2.3", 1},
		{"*", -1},
		{"latest", -1},
		{"", -1},
	}
	for _, c := range cases {
		if got := depMajor(c.in); got != c.want {
			t.Errorf("depMajor(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}
