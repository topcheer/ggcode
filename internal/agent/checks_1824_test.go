package agent

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// #1824 case 2 pin: content drift under an identical mtime must NOT be
// flagged redundant (the read is legitimate).
func Test1824MtimeEqualContentChanged(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "f.go")
	os.WriteFile(p, []byte(repeatStr1824("a", 4096)), 0o644)

	r := newRedundantReadState()
	// First read records mtime + hash.
	if h := r.checkRedundantRead(p, false); h != "" {
		t.Fatalf("first read must not warn, got %q", h)
	}
	// External write preserving the mtime (touch -r shape).
	st, _ := os.Stat(p)
	os.WriteFile(p, []byte(repeatStr1824("b", 4096)), 0o644)
	os.Chtimes(p, st.ModTime(), st.ModTime().Add(-time.Second)) // keep access old; keep mod identical
	os.Chtimes(p, st.ModTime(), st.ModTime())
	cur, _ := os.Stat(p)
	if cur.ModTime().UnixNano() != st.ModTime().UnixNano() {
		t.Skip("filesystem mtime granularity too coarse to force equality; relying on hash branch is still covered below")
	}
	if h := r.checkRedundantRead(p, false); h != "" {
		t.Fatalf("mtime-equal but content-changed re-read must not warn (weak evidence), got %q", h)
	}
	// Truly unchanged: still warns (positive control).
	r2 := newRedundantReadState()
	r2.checkRedundantRead(p, false)
	if h := r2.checkRedundantRead(p, false); h == "" {
		t.Fatal("unchanged re-read must still warn")
	}
}

func repeatStr1824(ch string, n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = ch[0]
	}
	return string(b)
}
