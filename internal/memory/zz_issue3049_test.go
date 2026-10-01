package memory

// Regression probes for #3049:
//   - C1: complementary values (one a token-subset of the other) must NOT be
//     judged contradictory - "go" vs "go+typescript" overlaps 2/3, the old
//     band's middle, and deleted/marked the earlier memory as contradicted.
//   - C2: an include-only Makefile must produce ZERO drift findings.
//   - C3: an exact-key duplicate hit must carry ITS OWN entry content, not
//     the similarity-loop leftover from a different key.
//   - C4: truncation must be rune-safe (no split CJK runes).

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIssue3049_C1_SubsetValuesAreNotContradictions(t *testing.T) {
	if claimsConflict("language: go", "language: go+typescript") {
		t.Fatal("#3049-C1: complementary subset value judged as contradiction")
	}
	if claimsConflict("make build", "make build+test+lint") {
		t.Fatal("#3049-C1: '+'-list enrichment judged as contradiction")
	}
	// Token-subset WITHOUT '+' grouping is a rephrased command - must keep
	// conflicting (pinned by existing BuildCommandConflict tests).
	if !claimsConflict("go build ./...", "go build -tags goolm ./...") {
		t.Fatal("#3049-C1: non-list token-subset must still conflict (regression guard)")
	}
	// Genuine divergent values must still conflict.
	if !claimsConflict("build command: go build ./cmd/app", "build command: go build ./internal/...") {
		t.Fatal("divergent same-domain values must still conflict")
	}
	// Opposite values keep conflicting.
	if !claimsConflict("enabled", "disabled") {
		t.Fatal("opposite values must still conflict")
	}
}

func TestIssue3049_C2_IncludeOnlyMakefileNoFalseDrift(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Makefile"), []byte("include mobile/flutter/Makefile.mk\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	content := "Build with:\n\n```make\nmake build\n```\n\nand test:\n\n```make\nmake test\n```\n"
	findings := ScanConventionDrift(content, dir)
	for _, f := range findings {
		if strings.Contains(f, "is referenced but not defined in Makefile") {
			t.Fatalf("#3049-C2: include-only Makefile produced a false drift finding: %s", f)
		}
	}
	// A Makefile WITH targets still scans (guard against over-disabling).
	if err := os.WriteFile(filepath.Join(dir, "Makefile"), []byte("build:\n\tgo build ./...\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	findings = ScanConventionDrift(content, dir)
	found := false
	for _, f := range findings {
		if strings.Contains(f, `"test" is referenced`) {
			found = true
		}
	}
	if !found {
		t.Fatal("a real missing target must still be reported when the Makefile defines targets")
	}
}

func TestIssue3049_C3_ExactMatchCarriesOwnContent(t *testing.T) {
	dir := t.TempDir()
	// Two entries with identical token sets but different keys; the
	// similarity loop hits 1.0 on the FIRST one, and the exact match is the
	// second - the old code returned the first one's content.
	if err := os.WriteFile(filepath.Join(dir, "cmd-build.md"), []byte("WRONG ENTRY"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "build-cmd.md"), []byte("RIGHT ENTRY"), 0o644); err != nil {
		t.Fatal(err)
	}
	am := &AutoMemory{dir: dir}
	dup := am.CheckDuplicate("build-cmd", "value")
	if dup.Similarity != 1.0 || dup.SimilarTo != "build-cmd" {
		t.Fatalf("exact key must be detected, got %+v", dup)
	}
	if !strings.Contains(dup.ExistingContent, "RIGHT ENTRY") {
		t.Fatalf("#3049-C3: exact match carried the wrong entry's content: %q", dup.ExistingContent)
	}
}

func TestIssue3049_C4_RuneSafeTruncation(t *testing.T) {
	cjk := strings.Repeat("构建命令", 30) // 4 bytes per pair, 120 bytes total
	got := truncate(cjk, 40)
	if !strings.HasSuffix(got, "...") {
		t.Fatal("truncate must keep the ellipsis suffix")
	}
	for _, r := range got {
		if r == 0xFFFD {
			t.Fatal("#3049-C4: truncate split a rune mid-sequence (mojibake)")
		}
	}
}
