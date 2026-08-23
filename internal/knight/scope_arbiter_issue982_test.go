package knight

import (
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestIssue982MakeProseNotFlagged verifies that natural-language uses of
// "make ..." in running prose do not trigger a global->project scope
// downgrade. Corpus lines are taken from real skill bodies in this repo
// (agent/skills/hyperframes/SKILL.md, agent/skills/media-use/SKILL.md).
func TestIssue982MakeProseNotFlagged(t *testing.T) {
	projDir := filepath.Join("/tmp", "ggcode")

	bodies := []struct {
		name string
		body string
	}{
		{
			name: "hyperframes-corpus-make-something",
			body: "I just have a URL - make something",
		},
		{
			name: "media-use-corpus-make-the-result",
			body: "merely to make the result look more",
		},
		{
			name: "make-sure-imperative",
			body: "Make sure to validate inputs before calling the tool.",
		},
		{
			name: "make-sense-embedded",
			body: "This does not make sense in a generic workflow.",
		},
		{
			name: "make-changes-midsentence",
			body: "When the user asks to make changes to the style, apply them consistently.",
		},
	}

	for _, b := range bodies {
		t.Run(b.name, func(t *testing.T) {
			if reason := scopeDowngradeReason(projDir, b.body); reason != "" {
				t.Fatalf("prose 'make ...' should not downgrade a global skill, got %q", reason)
			}
		})
	}
}

// TestIssue982MakeCommandFlagged verifies that line-initial make commands
// (including inside fenced code blocks) still trigger the downgrade.
func TestIssue982MakeCommandFlagged(t *testing.T) {
	projDir := filepath.Join("/tmp", "ggcode")

	bodies := []struct {
		name string
		body string
	}{
		{
			name: "fenced-make-build",
			body: "Build with:\n\n```sh\nmake build\n```",
		},
		{
			name: "fenced-make-test-foo",
			body: "```makefile\nmake test-foo\n```",
		},
		{
			name: "indented-command-line",
			body: "Steps:\n\n    make build\n    make test-foo",
		},
		{
			name: "top-level-command-line",
			body: "make build",
		},
	}

	for _, b := range bodies {
		t.Run(b.name, func(t *testing.T) {
			reason := scopeDowngradeReason(projDir, b.body)
			if reason == "" {
				t.Fatalf("expected make-target downgrade reason, got empty")
			}
			if !strings.Contains(reason, "make") {
				t.Fatalf("reason %q missing substring %q", reason, "make")
			}
		})
	}
}

// TestIssue982FrontmatterValueWithDashes verifies the frontmatter terminator
// is a standalone "---" line. A value containing "---" as a prefix must not
// truncate the frontmatter early and leak frontmatter content into the body
// analysis (or vice versa).
func TestIssue982FrontmatterValueWithDashes(t *testing.T) {
	projDir := filepath.Join("/tmp", "ggcode")

	// Case 1: frontmatter value contains "---text"; body is clean prose.
	// The real terminator is the standalone line; body must not be analyzed
	// as if it started at the inline dashes.
	content := `---
name: dash-value
description: rule about ---markers in text
scope: global
---

# Generic rule

Always prefer composition over inheritance.`
	if reason := scopeDowngradeReason(projDir, content); reason != "" {
		t.Fatalf("clean body with dash-prefixed frontmatter value should not downgrade, got %q", reason)
	}

	// Case 2: same frontmatter shape, but the BODY references the project
	// basename - the strip must not stop at "---markers" and must still
	// reach the body so the real signal is detected.
	content2 := `---
name: dash-value-2
description: rule about ---markers in text
scope: global
---

# Project rule

Edit the ggcode scheduler to apply this.`
	reason := scopeDowngradeReason(projDir, content2)
	if reason == "" {
		t.Fatalf("expected basename downgrade to be detected after correct frontmatter strip")
	}
	if !strings.Contains(reason, "basename") {
		t.Fatalf("reason %q missing substring %q", reason, "basename")
	}
}

// TestIssue982RelRootAllOccurrences verifies the rel-root scan checks every
// occurrence of cmd/ (etc.), not just the first: a first hit with no path
// character following must not shadow a later real path reference.
func TestIssue982RelRootAllOccurrences(t *testing.T) {
	projDir := filepath.Join("/tmp", "zzz") // basename too short to fire (<3)

	// First occurrence "cmd/" is followed by a space (prose); the later
	// "cmd/foo" is a real path and must be flagged.
	body := "Place files under cmd/ for binaries; the launcher lives in cmd/foo/main.go."
	reason := scopeDowngradeReason(projDir, body)
	if reason == "" {
		t.Fatalf("expected project-relative path downgrade for second cmd/ occurrence")
	}
	if !strings.Contains(reason, "cmd/") {
		t.Fatalf("reason %q missing substring %q", reason, "cmd/")
	}

	// Control: prose-only mention with no path segment stays global.
	if reason := scopeDowngradeReason(projDir, "Mention cmd/ only in prose here."); reason != "" {
		t.Fatalf("prose-only cmd/ mention should not downgrade, got %q", reason)
	}
}

// TestIssue982SemanticMemoryLockOrder exercises concurrent Append/Recent on
// two store instances sharing one path. Before #982 the inverted lock order
// (Append: s.mu->pathMu vs Recent: pathMu->s.mu) could deadlock; the fix
// removed the per-instance mutex so pathMu is the only lock. This test fails
// (times out) if any AB-BA ordering is reintroduced.
func TestIssue982SemanticMemoryLockOrder(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "knight-memory.jsonl")

	storeA := newSemanticMemoryStore(path)
	storeB := newSemanticMemoryStore(path)

	const workers = 4
	const iterations = 20

	done := make(chan struct{})
	go func() {
		defer close(done)
		var wg sync.WaitGroup
		for w := 0; w < workers; w++ {
			wg.Add(2)
			go func() {
				defer wg.Done()
				for i := 0; i < iterations; i++ {
					if err := storeA.Append(SemanticMemoryEntry{Summary: "lock-order probe"}); err != nil {
						t.Errorf("append: %v", err)
						return
					}
				}
			}()
			go func() {
				defer wg.Done()
				for i := 0; i < iterations; i++ {
					if _, err := storeB.Recent(5); err != nil {
						t.Errorf("recent: %v", err)
						return
					}
				}
			}()
		}
		wg.Wait()
	}()

	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("semantic memory store deadlocked (AB-BA lock order regression)")
	}
}
