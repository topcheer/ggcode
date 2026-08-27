package agent

// Targeted regression tests for GitHub issues #1156 and #1157.
// Kept in a separate file so each fix scope stays reviewable.

import (
	"strings"
	"testing"
)

// ---------- #1156: CJK-aware query tokenizer ----------

func TestIssue1156_PureCJKQueryProducesBigrams(t *testing.T) {
	tokens := qcTokenize("会话管理逻辑")
	for _, want := range []string{"会话", "话管", "管理", "理逻", "逻辑"} {
		if !tokens[want] {
			t.Fatalf("expected bigram token %q in %v", want, tokens)
		}
	}
	if len(tokens) != 5 {
		t.Fatalf("expected exactly 5 bigram tokens, got %d: %v", len(tokens), tokens)
	}
}

func TestIssue1156_LoneSingleCJKRunKeepsRune(t *testing.T) {
	tokens := qcTokenize("搜")
	if !tokens["搜"] {
		t.Fatalf("expected lone rune token for single-char CJK query (#1156 zero-visibility bug), got %v", tokens)
	}
}

func TestIssue1156_DifferentPureCJKQueriesNotIdentical(t *testing.T) {
	a := qcTokenize("会话管理逻辑")
	b := qcTokenize("数据库连接池")
	if sim := qcJaccard(a, b); sim != 0.0 {
		t.Fatalf("disjoint CJK queries must not look identical, got Jaccard %f", sim)
	}
	c := qcTokenize("会话管理实现")
	simAC := qcJaccard(a, c)
	if simAC <= 0 || simAC >= 1 {
		t.Fatalf("partially overlapping CJK queries expected between 0 and 1, got %f", simAC)
	}
	if simAC <= qcJaccard(a, b) {
		t.Fatalf("related queries should score higher than unrelated ones (%f vs %f)", simAC, qcJaccard(a, b))
	}
}

func TestIssue1156_MixedQueryIncludesBothScripts(t *testing.T) {
	tokens := qcTokenize("session会话管理")
	if !tokens["session"] {
		t.Fatalf("expected ASCII token 'session' in mixed query, got %v", tokens)
	}
	for _, want := range []string{"会话", "话管", "管理"} {
		if !tokens[want] {
			t.Fatalf("expected CJK bigram %q in mixed query, got %v", want, tokens)
		}
	}
}

func TestIssue1156_MixedQueriesSharingASCIIOnlyNoLongerIdentical(t *testing.T) {
	// Before the fix both sides tokenized to {session} giving Jaccard 1.0.
	a := qcTokenize("session管理逻辑")
	b := qcTokenize("session数据库管理")
	sim := qcJaccard(a, b)
	if sim >= 1.0 {
		t.Fatalf("mixed queries sharing only ASCII must not be identical, got %f", sim)
	}
	// Intersection {session, 管理}, union size 7 -> 2/7 (~0.29).
	if sim <= 0 || sim > 0.4 {
		t.Fatalf("expected partial similarity 2/7 for shared-bigram mixed queries, got %f", sim)
	}
}

func TestIssue1156_PureCJKSimilarQueriesTriggerWarning(t *testing.T) {
	q := newQueryConvergeState()
	q.recordToolCall("grep", `{"pattern":"用户认证逻辑处理"}`, 1)
	q.recordToolCall("grep", `{"pattern":"用户认证流程处理"}`, 2)
	q.recordToolCall("grep", `{"pattern":"用户认证校验处理"}`, 3)
	if msg := q.maybeWarn(4); msg == "" {
		t.Fatal("expected query-convergence warning for similar pure-CJK queries (#1156)")
	}
}

func TestIssue1156_CJKPunctuationSplitsRuns(t *testing.T) {
	a := qcTokenize("登录失败，重试机制")
	b := qcTokenize("登录成功后跳转页面，保存状态")
	if sim := qcJaccard(a, b); sim > 0.3 {
		t.Fatalf("distinct clauses separated by CJK punctuation must not converge, got %f", sim)
	}
}

// ---------- #1157: pointer-slice shape heuristic ----------

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
	// Bare identifiers hide the element type; without type info the detector
	// keeps its default warning behavior even when the real element is *T.
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
	// Adding ONLY a pointer-shaped range must produce zero new issues;
	// an unsuppressed heuristic would surface p.Val as a second delta hit.
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

	// Control: adding a value-slice pattern is reported once.
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
