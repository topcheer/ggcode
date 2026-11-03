package agent

import (
	"strings"
	"testing"
)

func Test1520GotoLabelNotUnreachable(t *testing.T) {
	newContent := `package p
func f(err error) {
	if err != nil {
		goto done
	}
	println("ok")
	return
	done:
	println("cleanup")
}
`
	warnings := checkUnreachableCode("t.go", "", newContent)
	for _, w := range warnings {
		if contains := len(w) > 0; contains {
			t.Fatalf("goto-target code must not be flagged unreachable: %q", w)
		}
	}
}

func Test1520TwoLineUintptrRoundTrip(t *testing.T) {
	src := `package p
import "unsafe"
func g(p *byte, q *byte) {
	off := uintptr(unsafe.Pointer(p)) + 16
	*q = *(*byte)(unsafe.Pointer(off))
}
`
	warnings := checkUnsafeUsage("t.go", "", src)
	if len(warnings) == 0 {
		t.Fatal("two-line uintptr(unsafe.Pointer(p))+16 -> unsafe.Pointer(off) round trip must be detected (vet-unsafeptr parity)")
	}
}

func Test1520UnusedParamHintNonDestructive(t *testing.T) {
	src := `package p
type handler struct{}
func (h *handler) Shutdown(ctx string) error { return nil }
`
	warnings := checkUnusedParam("t.go", "", src)
	for _, w := range warnings {
		if len(w) > 0 && w[0] == 'X' {
			t.Fatal("unreachable")
		}
		if strings.Contains(w, "consider removing it or renaming") {
			t.Fatalf("hint must not offer deletion as an option (interface contracts break): %q", w)
		}
	}
}
