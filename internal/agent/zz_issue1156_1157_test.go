package agent

import (
	"strings"
	"testing"
)

func TestIssue1157_PointerCompositeLiteralsSuppressed(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{"slice_literal_field_assign", `package main

type User struct{ Name string }

func renameAll() {
	for _, u := range []*User{{Name: "a"}, {Name: "b"}} {
		u.Name = "renamed"
	}
}
`},
		{"map_literal_pointer_value", `package main

type User struct{ Name string }

func renameAll() {
	for _, u := range map[int]*User{1: {Name: "a"}} {
		u.Name = "renamed"
	}
}
`},
		{"slice_literal_address_of", `package main

type Item struct{ Val int }

func proc() {
	for _, item := range []*Item{{1}} {
		modifyItem(&item)
	}
}
func modifyItem(i *Item) {}
`},
	}
	for _, tc := range cases {
		warnings := checkRangeCopyMod("test.go", "", tc.src)
		if len(warnings) != 0 {
			t.Errorf("%s: expected 0 warnings for inferred pointer elements (#1157), got %d: %v",
				tc.name, len(warnings), warnings)
		}
	}
}

func TestIssue1157_MakeAndAppendPointerFormsSuppressed(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{"make_call", `package main

type User struct{ Name string }

func initDefaults(n int) {
	for _, u := range make([]*User, n) {
		u.Name = "default"
	}
}
`},
		{"append_with_literal", `package main

type User struct{ Name string }

func merged(extra []*User) {
	for _, u := range append([]*User{}, extra...) {
		u.Name = "x"
	}
}
`},
		{"append_nested_make", `package main

type User struct{ Name string }

func grown(extra []*User) {
	for _, u := range append(make([]*User, 0, 4), extra...) {
		u.Name = "x"
	}
}
`},
	}
	for _, tc := range cases {
		warnings := checkRangeCopyMod("test.go", "", tc.src)
		if len(warnings) != 0 {
			t.Errorf("%s: expected 0 warnings for make/append pointer forms (#1157), got %d: %v",
				tc.name, len(warnings), warnings)
		}
	}
}

func TestIssue1157_ValueCompositeLiteralStillFlagged(t *testing.T) {
	src := `package main

type Item struct{ Val int }

func bump() {
	for _, item := range []Item{{Val: 1}, {Val: 2}} {
		item.Val = 9
	}
}
`
	warnings := checkRangeCopyMod("test.go", "", src)
	if len(warnings) != 1 {
		t.Fatalf("value-slice literal must still warn (heuristic must not oversuppress), got %d: %v",
			len(warnings), warnings)
	}
	if !strings.Contains(warnings[0], "index-based") {
		t.Errorf("unexpected warning text: %s", warnings[0])
	}
}

func TestIssue1157_BareIdentifierKeepsOriginalBehavior(t *testing.T) {

	src := `package main

type User struct{ Name string }

func renameAll(ptrs []*User) {
	for _, u := range ptrs {
		u.Name = "x"
	}
}
`
	warnings := checkRangeCopyMod("test.go", "", src)
	if len(warnings) != 1 {
		t.Fatalf("bare identifier ranges keep prior behavior, expected 1 warning, got %d: %v",
			len(warnings), warnings)
	}
}

func TestIssue1157_DeltaAwareExcludesPointerShapes(t *testing.T) {
	oldSrc := `package main

type User struct{ Name string }

func renameAll(ptrs []*User) {
	for _, u := range ptrs {
		u.Name = "x"
	}
}
`

	withPointer := oldSrc + `
type Item struct{ Val int }

func bumpPtr() {
	for _, p := range []*Item{{Val: 1}} {
		p.Val = 99
	}
}
`
	if warnings := checkRangeCopyMod("test.go", oldSrc, withPointer); len(warnings) != 0 {
		t.Fatalf("delta must exclude suppressed pointer shapes (#1157), got %d: %v",
			len(warnings), warnings)
	}

	withValue := oldSrc + `
type Item struct{ Val int }

func bump(items []Item) {
	for _, it := range items {
		it.Val = 99
	}
}
`
	warnings := checkRangeCopyMod("test.go", oldSrc, withValue)
	if len(warnings) != 1 {
		t.Fatalf("expected exactly 1 new delta warning for value pattern, got %d: %v",
			len(warnings), warnings)
	}
}
