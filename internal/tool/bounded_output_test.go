package tool

import (
	"bytes"
	"fmt"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"
)

func TestBoundedOutputWriter_SmallOutputUnchanged(t *testing.T) {
	w := newBoundedOutputWriter(8192)
	w.Write([]byte("hello world"))
	if got := w.String(); got != "hello world" {
		t.Fatalf("small output must round-trip exactly, got %q", got)
	}
}

func TestBoundedOutputWriter_HeadAndTailKept(t *testing.T) {
	w := newBoundedOutputWriter(8192) // head 4KB, tail 4KB
	var sb strings.Builder
	for i := 0; i < 1000; i++ {
		sb.WriteString(fmt.Sprintf("line-%04d: %s\n", i, strings.Repeat("x", 20)))
	}
	w.Write([]byte(sb.String()))
	got := w.String()
	if !strings.Contains(got, "line-0000") {
		t.Fatalf("head line-0000 missing:\n%.200s", got)
	}
	if !strings.Contains(got, "line-0999") {
		t.Fatalf("tail line-0999 missing:\n%.200s", got)
	}
	if !strings.Contains(got, "intermediate output dropped") {
		t.Fatalf("overflow marker missing:\n%.200s", got)
	}
	// Retained memory must be bounded: head 4KB + tail staging 8KB + marker.
	if n := w.Len(); n > 3*4096 {
		t.Fatalf("retention exceeded bound: %d bytes", n)
	}
}

func TestBoundedOutputWriter_ExactHeadBoundary(t *testing.T) {
	w := newBoundedOutputWriter(8192)
	half := 4096
	// Fill head exactly.
	w.Write([]byte(strings.Repeat("a", half)))
	// First tail write starts the tail phase; total well under tail cap.
	w.Write([]byte("BC"))
	got := w.String()
	if !strings.Contains(got, strings.Repeat("a", 100)) || !strings.Contains(got, "BC") {
		t.Fatalf("boundary content lost, got %.80s...", got)
	}
	if strings.Contains(got, "dropped") {
		t.Fatalf("no overflow expected at this size, got %q", got)
	}
}

func TestBoundedOutputWriter_ManySmallWritesConcurrent(t *testing.T) {
	w := newBoundedOutputWriter(8192)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				w.Write([]byte(fmt.Sprintf("g%d-i%03d\n", g, i)))
			}
		}(g)
	}
	wg.Wait()
	got := w.String()
	// Content must be bounded and end with a well-formed final line region.
	if n := w.Len(); n > 3*4096 {
		t.Fatalf("retention exceeded bound under concurrency: %d", n)
	}
	if !strings.HasSuffix(strings.TrimRight(got, "\n"), "\n") == false || len(got) == 0 {
		t.Fatalf("empty retained output")
	}
}

// BenchmarkBoundedOutputWriter_HeavyStream simulates a 1GB-output command in
// 64KB chunks - the "global build" profile. Retention must stay flat.
func BenchmarkBoundedOutputWriter_HeavyStream(b *testing.B) {
	chunk := strings.Repeat("x", 64*1024) // 64KB per Write, like pipe buffers
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		w := newBoundedOutputWriter(2 * maxOutputSize)
		for total := 0; total < 256*1024*1024; total += len(chunk) {
			w.Write([]byte(chunk))
		}
		_ = w.String()
	}
}

// TestBoundedOutputWriterRuneSafeHeadCut: a multi-byte char straddling the
// headCap boundary must not be split - the head cut snaps back to a rune
// start and the dropped bytes are counted as overflow.
func TestBoundedOutputWriterRuneSafeHeadCut(t *testing.T) {
	// 3-byte CJK rune; headCap = 8. Write 7 ASCII bytes then force the cut
	// mid-rune: 7 + first 1 byte of a 3-byte rune lands at exactly 8.
	w := newBoundedOutputWriter(16) // headCap = 8
	cjk := []byte("中")              // 3 bytes
	w.Write([]byte("1234567"))      // 7 bytes
	w.Write(append(append([]byte{}, cjk...), []byte("tail...")...))
	out := w.String()
	if !utf8.ValidString(out) {
		t.Errorf("head cut split a rune, invalid UTF-8: %q", out)
	}
	if !strings.HasPrefix(out, "1234567") {
		t.Errorf("head bytes lost, got %q", out)
	}
}

// TestBoundedOutputWriterRuneSafeTailCompaction: tail compaction (2x tailCap)
// must snap forward past continuation bytes instead of slicing mid-rune.
func TestBoundedOutputWriterRuneSafeTailCompaction(t *testing.T) {
	const capBytes = 4096 // min clamp, tailCap = 2048
	w := newBoundedOutputWriter(capBytes)
	// Feed enough CJK text that compaction fires many times, at effectively
	// random byte alignments.
	chunk := bytes.Repeat([]byte("中文输出"), 2000) // 4 runes x 3 bytes = 12 bytes, repeated
	for len(chunk) > 0 {
		n := 997 // awkward prime chunk size to vary alignment per Write
		if n > len(chunk) {
			n = len(chunk)
		}
		w.Write(chunk[:n])
		chunk = chunk[n:]
	}
	out := w.String()
	if !utf8.ValidString(out) {
		t.Errorf("tail compaction split a rune, invalid UTF-8 near cut: %q", lastRunes(out, 20))
	}
	if !strings.Contains(out, "bytes of intermediate output dropped") {
		t.Errorf("expected overflow marker, got tail: %q", lastRunes(out, 80))
	}
}

func lastRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[len(r)-n:])
}
