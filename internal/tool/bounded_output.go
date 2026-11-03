package tool

import (
	"bytes"
	"fmt"
	"sync"
)

type boundedOutputWriter struct {
	mu       sync.Mutex
	head     bytes.Buffer
	headCap  int
	tail     []byte
	tailCap  int
	overflow int64
}

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

func (w *boundedOutputWriter) Write(p []byte) (int, error) {
	n := len(p)
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.head.Len() < w.headCap {
		room := w.headCap - w.head.Len()
		if n <= room {
			w.head.Write(p)
			return n, nil
		}
		cut := snapForwardToRune(p, room)
		w.head.Write(p[:cut])

		p = p[cut:]
	}

	w.tail = append(w.tail, p...)
	if excess := len(w.tail) - 2*w.tailCap; excess > 0 {
		w.overflow += int64(excess)

		start := len(w.tail) - w.tailCap
		snap := snapForwardToRune(w.tail, start)
		w.overflow += int64(snap - start)
		w.tail = append(w.tail[:0], w.tail[snap:]...)
	}
	return n, nil
}

func snapForwardToRune(b []byte, i int) int {
	for k := 0; k < 3 && i < len(b) && b[i]&0xC0 == 0x80; k++ {
		i++
	}
	return i
}

func (w *boundedOutputWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.overflow > 0 {
		tail := w.tail
		if len(tail) > w.tailCap {

			cut := snapForwardToRune(tail, len(tail)-w.tailCap)

			w.overflow += int64(cut - (len(tail) - w.tailCap))
			tail = tail[cut:]
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

	return w.head.String() + string(w.tail)
}

func (w *boundedOutputWriter) Len() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.head.Len() + len(w.tail)
}
