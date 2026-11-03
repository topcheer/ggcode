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
	w := newBoundedOutputWriter(8192)
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

	if n := w.Len(); n > 3*4096 {
		t.Fatalf("retention exceeded bound: %d bytes", n)
	}
}

func TestBoundedOutputWriter_ExactHeadBoundary(t *testing.T) {
	w := newBoundedOutputWriter(8192)
	half := 4096

	w.Write([]byte(strings.Repeat("a", half)))

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

	if n := w.Len(); n > 3*4096 {
		t.Fatalf("retention exceeded bound under concurrency: %d", n)
	}
	if !strings.HasSuffix(strings.TrimRight(got, "\n"), "\n") == false || len(got) == 0 {
		t.Fatalf("empty retained output")
	}
}

func BenchmarkBoundedOutputWriter_HeavyStream(b *testing.B) {
	chunk := strings.Repeat("x", 64*1024)
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

func TestBoundedOutputWriterRuneSafeHeadCut(t *testing.T) {

	w := &boundedOutputWriter{headCap: 8, tail: make([]byte, 0, 8), tailCap: 8}
	cjk := []byte("中")
	w.Write([]byte("1234567"))
	w.Write(append(append([]byte{}, cjk...), []byte("tail...")...))
	out := w.String()
	if !utf8.ValidString(out) {
		t.Errorf("head cut split a rune, invalid UTF-8: %q", out)
	}
	if !strings.HasPrefix(out, "1234567") {
		t.Errorf("head bytes lost, got %q", out)
	}

	if w.head.Len() > w.headCap+3 {
		t.Errorf("head exceeded headCap+3 (rune snap bound): %d > %d", w.head.Len(), w.headCap+3)
	}

	w.mu.Lock()
	neg := w.overflow < 0
	w.mu.Unlock()
	if neg {
		t.Error("overflow went negative - head-cut accounting is wrong")
	}

	if len(w.tail) > 0 && w.tail[0]&0xC0 == 0x80 {
		t.Error("tail starts with a bare continuation byte")
	}
}

func TestBoundedOutputWriterRuneSafeTailCompaction(t *testing.T) {
	const capBytes = 4096
	w := newBoundedOutputWriter(capBytes)

	chunk := bytes.Repeat([]byte("中文输出"), 2000)
	for len(chunk) > 0 {
		n := 997
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
