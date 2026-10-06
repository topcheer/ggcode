package agent

import "testing"

// zz_issue2910_test.go pins the #2910 fix: map preallocation warnings must not
// fire (nor suggest len()) for range sources whose iteration count is
// unknowable — channels, integer values, and func iterators — while normal
// slice/map sources keep the len() hint.

func TestIssue2910_RangeChanParamNoWarning(t *testing.T) {
	code := `package main
type Item struct{ K string }
func drain(ch chan Item) map[string]bool {
	m := make(map[string]bool)
	for v := range ch {
		m[v.K] = true
	}
	return m
}`
	warnings := checkMapPrealloc("test.go", "", code)
	if len(warnings) != 0 {
		t.Fatalf("channel range has no knowable size; warning must be suppressed, got: %v", warnings)
	}
}

func TestIssue2910_RangeLocalChanMakeNoWarning(t *testing.T) {
	code := `package main
type Item struct{ K string }
func producer() map[string]bool {
	ch := make(chan Item)
	m := make(map[string]bool)
	go func() { ch <- Item{K: "x"}; close(ch) }()
	for v := range ch {
		m[v.K] = true
	}
	return m
}`
	warnings := checkMapPrealloc("test.go", "", code)
	for _, w := range warnings {
		if strContains(w, "len(ch)") {
			t.Errorf("channel range must not suggest len(ch), got: %s", w)
		}
	}
}

func TestIssue2910_RangeOverIntNoWarning(t *testing.T) {
	code := `package main
func spread(n int) map[int]bool {
	m := make(map[int]bool)
	for i := range n {
		m[i] = true
	}
	return m
}`
	warnings := checkMapPrealloc("test.go", "", code)
	if len(warnings) != 0 {
		t.Fatalf("range-over-int has no len() size; warning must be suppressed, got: %v", warnings)
	}
}

func TestIssue2910_RangeOverLocalIntLiteralNoWarning(t *testing.T) {
	code := `package main
func spread() map[int]bool {
	n := 10
	m := make(map[int]bool)
	for i := range n {
		m[i] = true
	}
	return m
}`
	warnings := checkMapPrealloc("test.go", "", code)
	if len(warnings) != 0 {
		t.Fatalf("range over locally-declared int literal must be suppressed, got: %v", warnings)
	}
}

func TestIssue2910_RangeOverFuncIteratorNoWarning(t *testing.T) {
	code := `package main
func collect() map[int]bool {
	m := make(map[int]bool)
	seq := func(yield func(int) bool) {
		for i := 0; i < 8; i++ {
			if !yield(i) {
				return
			}
		}
	}
	for v := range seq {
		m[v] = true
	}
	return m
}`
	warnings := checkMapPrealloc("test.go", "", code)
	if len(warnings) != 0 {
		t.Fatalf("range-over-func has no knowable size; warning must be suppressed, got: %v", warnings)
	}
}

func TestIssue2910_SliceSourceStillWarnsWithLenHint(t *testing.T) {
	code := `package main
type Item struct{ K string }
func build() map[string]bool {
	items := make([]Item, 0, 8)
	m := make(map[string]bool)
	for _, v := range items {
		m[v.K] = true
	}
	return m
}`
	warnings := checkMapPrealloc("test.go", "", code)
	if len(warnings) == 0 {
		t.Fatal("slice range must still produce the prealloc warning")
	}
	if !strContains(warnings[0], "len(items)") {
		t.Errorf("slice range should keep len(items) hint, got: %s", warnings[0])
	}
}

func TestIssue2910_MapSourceStillWarnsWithLenHint(t *testing.T) {
	code := `package main
func merge(src map[string]int) map[string]int {
	m := make(map[string]int)
	for k, v := range src {
		m[k] = v
	}
	return m
}`
	warnings := checkMapPrealloc("test.go", "", code)
	if len(warnings) == 0 {
		t.Fatal("map range (len(src) is exact) must still warn")
	}
	if !strContains(warnings[0], "len(src)") {
		t.Errorf("map range should keep len(src) hint, got: %s", warnings[0])
	}
}
