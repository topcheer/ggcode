package agent

import (
	"strings"
	"testing"
)

func TestCheckUnsafeUsage_PointerArith(t *testing.T) {
	src := `package main

import "unsafe"

func offset(p unsafe.Pointer, n int) unsafe.Pointer {
	return unsafe.Pointer(uintptr(p) + uintptr(n))
}
`
	warnings := checkUnsafeUsage("ptr.go", "", src)
	if len(warnings) == 0 {
		t.Fatal("expected pointer-arith warning")
	}
	if !strings.Contains(warnings[0], "arithmetic") {
		t.Errorf("unexpected warning: %s", warnings[0])
	}
}

func TestCheckUnsafeUsage_PointerArithSub(t *testing.T) {
	src := `package main

import "unsafe"

func sub(p unsafe.Pointer, n int) unsafe.Pointer {
	return unsafe.Pointer(uintptr(p) - uintptr(n))
}
`
	warnings := checkUnsafeUsage("sub.go", "", src)
	if len(warnings) == 0 {
		t.Fatal("expected pointer-arith (sub) warning")
	}
	if !strings.Contains(warnings[0], "arithmetic") {
		t.Errorf("unexpected warning: %s", warnings[0])
	}
}

func TestCheckUnsafeUsage_NoPointerArith(t *testing.T) {
	src := `package main

import "unsafe"

func safeOffset(p unsafe.Pointer, n int) unsafe.Pointer {
	return unsafe.Add(p, n)
}
`
	warnings := checkUnsafeUsage("safe.go", "", src)
	if len(warnings) != 0 {
		t.Errorf("expected no warnings for unsafe.Add, got: %v", warnings)
	}
}

func TestCheckUnsafeUsage_ReflectSliceHeader(t *testing.T) {
	src := `package main

import "reflect"

func toSlice(data []byte) {
	hdr := (*reflect.SliceHeader)(&data)
	_ = hdr
}
`
	warnings := checkUnsafeUsage("hdr.go", "", src)
	if len(warnings) == 0 {
		t.Fatal("expected reflect-header warning")
	}
	if !strings.Contains(warnings[0], "SliceHeader") {
		t.Errorf("unexpected warning: %s", warnings[0])
	}
}

func TestCheckUnsafeUsage_ReflectStringHeader(t *testing.T) {
	src := `package main

import "reflect"

func toStr(b []byte) string {
	return *(*string)(unsafe.Pointer(&reflect.StringHeader{}))
}
`
	warnings := checkUnsafeUsage("str.go", "", src)
	if len(warnings) == 0 {
		t.Fatal("expected reflect-header (StringHeader) warning")
	}
	if !strings.Contains(warnings[0], "StringHeader") {
		t.Errorf("unexpected warning: %s", warnings[0])
	}
}

func TestCheckUnsafeUsage_StoredUintptr(t *testing.T) {
	src := `package main

import "unsafe"

func storePtr(p unsafe.Pointer) {
	offset := uintptr(unsafe.Pointer(p))
	_ = offset
}
`
	warnings := checkUnsafeUsage("store.go", "", src)
	if len(warnings) == 0 {
		t.Fatal("expected stored-uintptr warning")
	}
	if !strings.Contains(warnings[0], "uintptr") {
		t.Errorf("unexpected warning: %s", warnings[0])
	}
}

func TestCheckUnsafeUsage_NoStoredUintptr(t *testing.T) {
	src := `package main

import "unsafe"

func passThrough(p unsafe.Pointer) unsafe.Pointer {
	return p
}
`
	warnings := checkUnsafeUsage("pass.go", "", src)
	if len(warnings) != 0 {
		t.Errorf("expected no warnings for safe pass-through, got: %v", warnings)
	}
}

func TestCheckUnsafeUsage_DeltaAware(t *testing.T) {
	oldSrc := `package main

import "unsafe"

func existing(p unsafe.Pointer) {
	offset := uintptr(unsafe.Pointer(p))
	_ = offset
}
`
	newSrc := oldSrc + `
func newFunc(p unsafe.Pointer, n int) unsafe.Pointer {
	return unsafe.Pointer(uintptr(p) + uintptr(n))
}
`
	warnings := checkUnsafeUsage("delta.go", oldSrc, newSrc)
	// Should flag only the new pointer-arith issue, not the pre-existing stored-uintptr
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "arithmetic") {
			found = true
		}
		if strings.Contains(w, "Stored uintptr") {
			t.Errorf("should not flag pre-existing stored-uintptr: %s", w)
		}
	}
	if !found {
		t.Error("expected pointer-arith warning for newly introduced code")
	}
}

func TestCheckUnsafeUsage_SkipTestFiles(t *testing.T) {
	src := `package main

import "unsafe"

func testHelper(p unsafe.Pointer, n int) unsafe.Pointer {
	return unsafe.Pointer(uintptr(p) + uintptr(n))
}
`
	warnings := checkUnsafeUsage("foo_test.go", "", src)
	if len(warnings) != 0 {
		t.Errorf("expected no warnings for test files, got: %v", warnings)
	}
}

func TestCheckUnsafeUsage_SkipNonGo(t *testing.T) {
	warnings := checkUnsafeUsage("script.py", "", `import unsafe`)
	if len(warnings) != 0 {
		t.Errorf("expected no warnings for non-Go files, got: %v", warnings)
	}
}

func TestCheckUnsafeUsage_EmptyContent(t *testing.T) {
	warnings := checkUnsafeUsage("empty.go", "", "")
	if len(warnings) != 0 {
		t.Errorf("expected no warnings for empty content, got: %v", warnings)
	}
}

func TestCheckUnsafeUsage_MaxWarnings(t *testing.T) {
	src := `package main

import (
	"reflect"
	"unsafe"
)

func f1(p unsafe.Pointer) {
	return unsafe.Pointer(uintptr(p) + 8)
}

func f2(p unsafe.Pointer) {
	return unsafe.Pointer(uintptr(p) - 8)
}

func f3(data []byte) {
	h := (*reflect.SliceHeader)(&data)
	_ = h
}

func f4(s string) {
	h := (*reflect.StringHeader)(&s)
	_ = h
}

func f5(p unsafe.Pointer) {
	o := uintptr(unsafe.Pointer(p))
	_ = o
}
`
	warnings := checkUnsafeUsage("multi.go", "", src)
	if len(warnings) > 3 {
		t.Errorf("expected max 3 warnings, got %d", len(warnings))
	}
}
