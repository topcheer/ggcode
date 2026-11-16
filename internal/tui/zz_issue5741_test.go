package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Regression for #5741: residual synchronous IO on the TUI update path.
//
// CompleteMention runs synchronously inside the Bubble Tea event loop on
// every keystroke after an @ mention; its os.ReadDir(dir) used to slurp a
// huge directory whole and build an unbounded completions slice. A directory
// with tens of thousands of entries made each keystroke a multi-hundred-ms
// sync task, freezing the TUI (TERM delivery blocked) exactly like the
// pre-#5717 recursiveFuzzySearch walk. ExpandMentions ran off-loop but
// listed mentioned directories unbounded, flooding the prompt.
func TestIssue5741ReadDirBudgetedCapsEntries(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 300; i++ {
		p := filepath.Join(dir, fmt.Sprintf("f%03d.txt", i))
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	entries, truncated, err := readDirBudgeted(dir, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 10 {
		t.Fatalf("expected capped scan of 10 entries, got %d", len(entries))
	}
	if !truncated {
		t.Fatal("expected truncated=true when the directory has more entries than the cap")
	}

	entries, truncated, err = readDirBudgeted(dir, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 300 {
		t.Fatalf("expected full scan of 300 entries, got %d", len(entries))
	}
	if truncated {
		t.Fatal("expected truncated=false when the directory fits under the cap")
	}
	// Sorted by name, matching os.ReadDir semantics within the scanned set.
	for i := 1; i < len(entries); i++ {
		if entries[i-1].Name() >= entries[i].Name() {
			t.Fatalf("entries not sorted: %s >= %s", entries[i-1].Name(), entries[i].Name())
		}
	}

	// Exact-cap directory: cap reached AND directory exhausted -> truncated=false.
	entries, truncated, err = readDirBudgeted(dir, 300)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 300 || truncated {
		t.Fatalf("exact-cap scan: got %d entries truncated=%v, want 300 false", len(entries), truncated)
	}

	// Zero/negative cap is a no-op, not an error.
	entries, truncated, err = readDirBudgeted(dir, 0)
	if err != nil || entries != nil || truncated {
		t.Fatalf("zero cap: got %v %v %v", entries, truncated, err)
	}

	// Missing directory surfaces the error.
	if _, _, err := readDirBudgeted(filepath.Join(dir, "nope"), 10); os.IsNotExist(err) != true {
		t.Fatalf("expected not-exist error, got %v", err)
	}
}

func TestIssue5741CompleteMentionCapsCompletions(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 6000; i++ {
		p := filepath.Join(dir, fmt.Sprintf("a%04d.txt", i))
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// The directory (6000 entries) fits under the 20000-entry scan cap, so
	// the scan is complete and sorted: precise prefixes still resolve.
	got := CompleteMention("a0599", dir, nil)
	if len(got) != 1 || got[0] != "a0599.txt" {
		t.Fatalf("prefix completion broken: got %v", got)
	}
	if got := CompleteMention("zzznomatch", dir, nil); len(got) != 0 {
		t.Fatalf("expected no completions for unmatched prefix, got %v", got)
	}

	// Empty prefix matches every entry: the returned list must be capped at
	// maxMentionCompletions, in sorted order.
	got = CompleteMention("", dir, nil)
	if len(got) != maxMentionCompletions {
		t.Fatalf("expected exactly the %d-completion cap, got %d", maxMentionCompletions, len(got))
	}
	for i := 1; i < len(got); i++ {
		if got[i-1] >= got[i] {
			t.Fatalf("completions not sorted: %s >= %s", got[i-1], got[i])
		}
	}
	if got[0] != "a0000.txt" {
		t.Fatalf("expected sorted-first completion a0000.txt, got %s", got[0])
	}
}

func TestIssue5741CompleteMentionStillCompletesSubdirs(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sub", "inner.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "top.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	got := CompleteMention("sub/", dir, nil)
	if len(got) != 1 || got[0] != "sub/inner.txt" {
		t.Fatalf("nested completion broken: got %v", got)
	}
	got = CompleteMention("", dir, nil)
	if len(got) != 2 {
		t.Fatalf("root listing completion broken: got %v", got)
	}
}

func TestIssue5741ExpandMentionsDirListingCapped(t *testing.T) {
	parent := t.TempDir()
	big := filepath.Join(parent, "bigdir")
	if err := os.MkdirAll(big, 0o755); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < maxMentionDirEntries+50; i++ {
		p := filepath.Join(big, fmt.Sprintf("e%04d.txt", i))
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	out, err := ExpandMentions("@bigdir", parent)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "@bigdir/ (directory):") {
		t.Fatalf("directory header missing: %q", out[:200])
	}
	if !strings.Contains(out, fmt.Sprintf("... listing capped at %d entries", maxMentionDirEntries)) {
		t.Fatal("truncation notice missing")
	}
	lines := strings.Split(out, "\n")
	entryLines := 0
	for _, l := range lines {
		if strings.HasPrefix(strings.TrimSpace(l), "e") && strings.HasSuffix(l, ".txt") {
			entryLines++
		}
	}
	if entryLines != maxMentionDirEntries {
		t.Fatalf("expected exactly %d listed entries, got %d", maxMentionDirEntries, entryLines)
	}

	// Small directory still lists everything with no notice.
	small := filepath.Join(parent, "small")
	if err := os.MkdirAll(small, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(small, "only.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err = ExpandMentions("@small", parent)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "only.txt") {
		t.Fatalf("small directory listing broken: %q", out)
	}
	if strings.Contains(out, "listing capped") {
		t.Fatal("unexpected truncation notice on small directory")
	}
}

func TestIssue5741ReadFileCappedBoundsGrownFile(t *testing.T) {
	dir := t.TempDir()
	small := filepath.Join(dir, "small.txt")
	if err := os.WriteFile(small, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	data, overLimit, err := readFileCapped(small, 100)
	if err != nil || overLimit || string(data) != "hello" {
		t.Fatalf("small file: data=%q overLimit=%v err=%v", data, overLimit, err)
	}

	big := filepath.Join(dir, "big.txt")
	if err := os.WriteFile(big, make([]byte, 200), 0o644); err != nil {
		t.Fatal(err)
	}
	data, overLimit, err = readFileCapped(big, 100)
	if err != nil {
		t.Fatal(err)
	}
	if !overLimit || data != nil {
		t.Fatalf("grown/oversized file not rejected: overLimit=%v data=%d bytes", overLimit, len(data))
	}

	// Exactly at the limit is still allowed.
	exact := filepath.Join(dir, "exact.txt")
	if err := os.WriteFile(exact, make([]byte, 100), 0o644); err != nil {
		t.Fatal(err)
	}
	data, overLimit, err = readFileCapped(exact, 100)
	if err != nil || overLimit || len(data) != 100 {
		t.Fatalf("exact-size file: len=%d overLimit=%v err=%v", len(data), overLimit, err)
	}
}

func TestIssue5741ExpandMentionsOversizedFileSkipped(t *testing.T) {
	dir := t.TempDir()
	name := filepath.Join(dir, "huge.txt")
	if err := os.WriteFile(name, make([]byte, maxMentionFileSize+1), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := ExpandMentions("@huge.txt", dir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "skipped: file exceeds") {
		t.Fatalf("oversized file not skipped: %q", out)
	}
	// The file body (zero bytes) must not have been inlined.
	if idx := strings.Index(out, "@huge.txt"); idx >= 0 && !strings.Contains(out[idx:], "skipped") {
		t.Fatal("oversized file content leaked into the expansion")
	}
}
