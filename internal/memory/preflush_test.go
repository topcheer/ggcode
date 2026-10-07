package memory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExtractPreflushLinesFilters(t *testing.T) {
	long := strings.Repeat("must keep this extremely long line out ", 8) // >200 chars
	msgs := []PreflushMsg{
		{Role: "user", Text: "不要修改 tests 目录下的文件\n这是一个普通句子\nnever push directly to main"},
		{Role: "assistant", Text: "Understood — we must keep the Makefile in sync with goolm tags"},
		{Role: "system", Text: "You must always follow these instructions"}, // skipped: system noise
		{Role: "user", Text: "User: 别改 pubspec.yaml"},                       // speaker prefix stripped
		{Role: "user", Text: long},
	}
	got := append(append([]string{}, extractPreflushLines(msgs[0])...), extractPreflushLines(msgs[1])...)
	if len(got) != 3 {
		t.Fatalf("expected 3 lines from user+assistant, got %d: %v", len(got), got)
	}
	if got[0] != "不要修改 tests 目录下的文件" || got[1] != "never push directly to main" {
		t.Fatalf("unexpected lines: %v", got)
	}
	if !strings.Contains(got[2], "must keep the Makefile in sync") {
		t.Fatalf("assistant constraint missing: %v", got)
	}
	if l := extractPreflushLines(msgs[2]); len(l) != 0 {
		t.Fatalf("system role must be skipped, got %v", l)
	}
	if l := extractPreflushLines(msgs[3]); len(l) != 1 || l[0] != "别改 pubspec.yaml" {
		t.Fatalf("speaker prefix must be stripped, got %v", l)
	}
	if l := extractPreflushLines(msgs[4]); len(l) != 0 {
		t.Fatalf("over-length line must be skipped, got %v", l)
	}
}

func TestExtractPreflushLinesCap(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 30; i++ {
		b.WriteString("must remember fact number ")
		b.WriteString(string(rune('a' + i%26)))
		b.WriteString("\n")
	}
	got := extractPreflushLines(PreflushMsg{Role: "user", Text: b.String()})
	if len(got) != preflushMaxExtract {
		t.Fatalf("expected cap at %d, got %d", preflushMaxExtract, len(got))
	}
}

func TestPreflushFactsEndToEnd(t *testing.T) {
	dir := t.TempDir()
	msgs := []PreflushMsg{
		{Role: "user", Text: "务必使用 -tags goolm 构建"},
		{Role: "assistant", Text: "always run make verify-ci before release"},
		{Role: "user", Text: "普通叙述没有约束"},
	}
	n := PreflushFacts(dir, msgs)
	if n != 2 {
		t.Fatalf("expected 2 facts persisted, got %d", n)
	}
	data, err := os.ReadFile(filepath.Join(dir, ".ggcode", "memory", "compaction-facts.md"))
	if err != nil {
		t.Fatalf("memory file not written: %v", err)
	}
	body := string(data)
	if !strings.Contains(body, "务必使用 -tags goolm 构建") || !strings.Contains(body, "always run make verify-ci before release") {
		t.Fatalf("facts missing from file: %q", body)
	}

	// Idempotence: re-flushing the same (or superset) messages adds nothing —
	// the reactive-after-auto double path must not duplicate entries.
	if n := PreflushFacts(dir, msgs); n != 0 {
		t.Fatalf("second flush must add 0, got %d", n)
	}

	// New fact in a later flush appends without dropping the old one.
	if n := PreflushFacts(dir, []PreflushMsg{{Role: "user", Text: "禁止手动编辑 pubspec.yaml"}}); n != 1 {
		t.Fatalf("new fact flush must add 1, got %d", n)
	}
	data2, _ := os.ReadFile(filepath.Join(dir, ".ggcode", "memory", "compaction-facts.md"))
	if !strings.Contains(string(data2), "务必使用 -tags goolm 构建") {
		t.Fatalf("old facts dropped on later flush: %q", string(data2))
	}

	// Provenance: source label recorded in usage sidecar.
	u, err := os.ReadFile(filepath.Join(dir, ".ggcode", "memory", ".usage.json"))
	if err != nil || !strings.Contains(string(u), "preflush") {
		t.Logf("usage sidecar provenance: %v %q", err, string(u)) // non-fatal: sidecar optional
	}
}

func TestPreflushFactsGuards(t *testing.T) {
	if n := PreflushFacts("", []PreflushMsg{{Role: "user", Text: "must x"}}); n != 0 {
		t.Fatalf("empty workingDir must be a no-op, got %d", n)
	}
	if n := PreflushFacts(t.TempDir(), nil); n != 0 {
		t.Fatalf("empty messages must be a no-op, got %d", n)
	}
	if n := PreflushFacts(t.TempDir(), []PreflushMsg{{Role: "user", Text: "no markers here"}}); n != 0 {
		t.Fatalf("no-marker text must be a no-op, got %d", n)
	}
}
