package rexcache

import (
	"regexp"
	"sync"
	"testing"
)

func TestCompileReturnsCachedPointer(t *testing.T) {
	a, err := Compile("5831" + "-alpha")
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	b, err := Compile("5831" + "-alpha")
	if err != nil {
		t.Fatalf("second Compile: %v", err)
	}
	if a != b {
		t.Fatal("Compile must hand back the identical cached *regexp.Regexp")
	}
	if !a.MatchString("5831-alpha") {
		t.Fatal("cached regex lost its semantics")
	}
}

func TestCompileInvalidPatternIsErrorAndUncached(t *testing.T) {
	before := Len()
	if _, err := Compile("(unclosed"); err == nil {
		t.Fatal("invalid pattern must return the compile error")
	}
	if Len() != before {
		t.Fatalf("invalid patterns must not occupy cache space: Len %d -> %d", before, Len())
	}
}

func TestMustReturnsCachedPointerAndPanicsOnInvalid(t *testing.T) {
	a := Must("5831" + "-beta")
	if b := Must("5831" + "-beta"); a != b {
		t.Fatal("Must must reuse the cached value")
	}
	defer func() {
		if recover() == nil {
			t.Fatal("Must must panic on an invalid pattern")
		}
	}()
	Must("(unclosed")
}

func TestCacheIsBounded(t *testing.T) {
	for i := 0; i < maxEntries+50; i++ {
		p := "5831" + "-bound-" + string(rune('a'+i%26)) + itoa(i)
		if re := Must(p); re == nil {
			t.Fatal("Must returned nil")
		}
	}
	if got := Len(); got > maxEntries {
		t.Fatalf("cache must stay bounded: Len=%d max=%d", got, maxEntries)
	}
	// The surviving cache still compiles and serves correct matches.
	if re := Must("5831" + "-bound-again"); !re.MatchString("5831-bound-again") {
		t.Fatal("post-eviction compile broken")
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}

func TestConcurrentCompileIsRaceFree(t *testing.T) {
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				p := "5831" + "-race-" + itoa(g) + "-" + itoa(i)
				re, err := Compile(p)
				if err != nil || re == nil {
					t.Errorf("Compile(%q): %v", p, err)
					return
				}
				if re2 := Must(p); re2 != re {
					t.Errorf("Must(%q) did not reuse Compile entry", p)
					return
				}
			}
		}(g)
	}
	wg.Wait()
}

var _ = regexp.MustCompile // keep regexp import shape stable for gofmt
