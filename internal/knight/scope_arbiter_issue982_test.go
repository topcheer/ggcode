package knight

import (
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

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

func TestIssue982FrontmatterValueWithDashes(t *testing.T) {
	projDir := filepath.Join("/tmp", "ggcode")

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

func TestIssue982RelRootAllOccurrences(t *testing.T) {
	projDir := filepath.Join("/tmp", "zzz")

	body := "Place files under cmd/ for binaries; the launcher lives in cmd/foo/main.go."
	reason := scopeDowngradeReason(projDir, body)
	if reason == "" {
		t.Fatalf("expected project-relative path downgrade for second cmd/ occurrence")
	}
	if !strings.Contains(reason, "cmd/") {
		t.Fatalf("reason %q missing substring %q", reason, "cmd/")
	}

	if reason := scopeDowngradeReason(projDir, "Mention cmd/ only in prose here."); reason != "" {
		t.Fatalf("prose-only cmd/ mention should not downgrade, got %q", reason)
	}
}

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
