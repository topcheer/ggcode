package agent

import (
	"strings"
	"testing"
)

// zz_issue3742_test.go pins the two #3742 fixes:
//  1. value-semantics builders (method returning the receiver type) are NOT
//     flagged -- the mutation-on-copy is returned to the caller by design.
//  2. nested field mutations (c.inner.f = v) ARE flagged -- the mutation is
//     lost with the copy just like a top-level field.
// It also pins the deliberate index-assignment blind spot declared in the
// file comment.

func TestIssue3742ValueBuilderNotFlagged(t *testing.T) {
	src := `package p

type Config struct{ name string }

func (c Config) WithName(n string) Config { c.name = n; return c }

func (c Config) Clone() (Config, error) { c.name = "clone"; return c, nil }
`
	issues := checkValueRecvMutation("cfg.go", "", src)
	for _, msg := range issues {
		if strings.Contains(msg, "WithName") || strings.Contains(msg, "Clone") {
			t.Fatalf("value-semantics builder falsely flagged: %s", msg)
		}
	}
	if len(issues) != 0 {
		t.Fatalf("expected zero issues for builder-only source, got: %v", issues)
	}
}

func TestIssue3742NestedFieldMutationFlagged(t *testing.T) {
	// #3799 B supersedes the FLAG expectation: this true-positive shape
	// (value inner field) is AST-indistinguishable from the pointer-field
	// form (c.state.ready with `state *State`), which is a LEGAL visible
	// mutation the old flag mislabeled "lost with the copy". The untyped
	// checker cannot tell them apart, so the whole nested-selector class
	// is conservatively exempt - this true positive is an accepted miss
	// (issue #3799's explicit tradeoff: miss over false accusation).
	src := `package p

type Inner struct{ f int }
type Holder struct{ inner Inner }

func (h Holder) SetInner(v int) { h.inner.f = v }
`
	issues := checkValueRecvMutation("holder.go", "", src)
	for _, msg := range issues {
		if strings.Contains(msg, "SetInner") && strings.Contains(msg, "VALUE receiver") {
			t.Fatalf("nested selector chains must be exempt (pointer-field ambiguity, #3799); got: %s", msg)
		}
	}
}

func TestIssue3742PointerBuilderUnaffected(t *testing.T) {
	// A value receiver that returns a DIFFERENT type is not a builder and
	// must still be flagged: mutation is genuinely lost.
	src := `package p

type Config struct{ name string }
type View struct{ Name string }

func (c Config) ToView() View { c.name = "x"; return View{Name: c.name} }
`
	issues := checkValueRecvMutation("cfg.go", "", src)
	found := false
	for _, msg := range issues {
		if strings.Contains(msg, "ToView") {
			found = true
		}
	}
	if !found {
		t.Fatalf("non-builder value-receiver mutation should still be flagged, issues: %v", issues)
	}
}

func TestIssue3742IndexAssignmentBlindSpotPinned(t *testing.T) {
	// Documented blind spot (#3742 comment): index assignments are not
	// checked (map/slice fields share backing store through the copy).
	src := `package p

type Buf struct{ arr [4]int }

func (b Buf) SetFirst(v int) { b.arr[0] = v }
`
	issues := checkValueRecvMutation("buf.go", "", src)
	for _, msg := range issues {
		if strings.Contains(msg, "SetFirst") {
			t.Fatalf("index assignment is a declared blind spot but was flagged: %s", msg)
		}
	}
}

func TestIssue3742BuilderDeltaSuppressionConsistent(t *testing.T) {
	// Old content with a builder plus a plain mutation: editing the plain
	// mutation in (identical) new content must still be delta-suppressed,
	// and the builder must never appear in oldMutations-driven output.
	oldSrc := `package p

type Config struct{ name, tag string }

func (c Config) WithTag(t string) Config { c.tag = t; return c }

func (c Config) Bad() { c.name = "x" }
`
	newSrc := `package p

type Config struct{ name, tag string }

func (c Config) WithTag(t string) Config { c.tag = t; return c }

func (c Config) Bad() { c.name = "y" }
`
	issues := checkValueRecvMutation("cfg.go", oldSrc, newSrc)
	for _, msg := range issues {
		if strings.Contains(msg, "WithTag") {
			t.Fatalf("builder flagged in delta pass: %s", msg)
		}
		if strings.Contains(msg, "Bad") {
			t.Fatalf("pre-existing Bad mutation should be delta-suppressed: %s", msg)
		}
	}
}
