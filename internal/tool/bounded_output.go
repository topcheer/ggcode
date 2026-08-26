package tool

import (
	"bytes"
	"fmt"
	"sync"
)

// boundedOutputWriter is a bytes.Buffer replacement for capturing command
// output whose size is untrusted (global builds, codegen, install trees).
//
// Problem it fixes: run_command used raw bytes.Buffer for stdout/stderr.
// Commands emitting gigabytes (`go build ./...` verbose, npm ci) grew the
// buffer unboundedly — peak RSS mirrored the raw output size, repeated
// growth-copies burned CPU, and the GC scanned hundreds of MB per cycle.
// All that to ultimately keep only maxOutputSize (100KB) after
// truncateMiddle. The whole-process slowdown users felt during "heavy
// commands" was this memory pressure, not the render path.
//
// Design: keep the first cap/2 bytes and the last cap/2 bytes (same
// head+tail philosophy as truncateMiddle). Overflow drops the middle and
// records the elided byte count. Retained memory is O(cap) regardless of
// total output volume. Concurrency-safe: stdout and stderr pumps may run
// on separate goroutines (bytes.Buffer is not safe).
type boundedOutputWriter struct {
	mu       sync.Mutex
	head     bytes.Buffer
	headCap  int
	tail     []byte
	tailCap  int
	overflow int64 // bytes dropped between head and tail
}

// newBoundedOutputWriter returns a writer retaining at most cap bytes
// (split head/tail). cap is clamped to a 4KB minimum.
func newBoundedOutputWriter(cap int) *boundedOutputWriter {
	if cap < 4096 {
		cap = 4096
	}
	half := cap / 2
	return &boundedOutputWriter{
		headCap: half,
		tail:    make([]byte, 0, half),
		tailCap: half,
	}
}

// Write implements io.Writer with bounded retention.
func (w *boundedOutputWriter) Write(p []byte) (int, error) {
	n := len(p)
	w.mu.Lock()
	defer w.mu.Unlock()

	// Head phase: fill until headCap.
	if w.head.Len() < w.headCap {
		room := w.headCap - w.head.Len()
		if n <= room {
			w.head.Write(p)
			return n, nil
		}
		w.head.Write(p[:room])
		p = p[room:]
	}

	// Tail phase: append, compacting whenever the staging slice exceeds
	// 2x tailCap so total memory stays O(tailCap) with amortized copies.
	w.tail = append(w.tail, p...)
	if excess := len(w.tail) - 2*w.tailCap; excess > 0 {
		w.overflow += int64(excess)
		// Keep the newest tailCap bytes.
		w.tail = append(w.tail[:0], w.tail[len(w.tail)-w.tailCap:]...)
	}
	return n, nil
}

// String reconstructs the retained output: head + marker + tail.
// The marker mirrors truncateMiddle's "[N bytes omitted]" contract so the
// final truncateMiddle pass treats it as ordinary content.
func (w *boundedOutputWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.overflow > 0 {
		tail := w.tail
		if len(tail) > w.tailCap {
			tail = tail[len(tail)-w.tailCap:]
		}
		if w.head.Len() == 0 {
			return string(tail)
		}
		return w.head.String() +
			fmt.Sprintf("\n... [%d bytes of intermediate output dropped] ...\n", w.overflow) +
			string(tail)
	}
	if len(w.tail) == 0 {
		return w.head.String()
	}
	if w.head.Len() == 0 {
		return string(w.tail)
	}
	// Head filled exactly, tail under cap: no elision, concatenate.
	return w.head.String() + string(w.tail)
}

// Len reports the retained size (head + tail).
func (w *boundedOutputWriter) Len() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.head.Len() + len(w.tail)
}
