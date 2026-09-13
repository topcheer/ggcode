package tool

import (
	"strings"
	"testing"
)

// #1677 ask_user family batch 1: 4a (schema doc must match the #804
// default-false behavior) and 4b (the *_with_freeform kind aliases must
// actually set AllowFreeform).

func TestIssue1677SchemaDocMatchesDefaultFalse(t *testing.T) {
	// 4a: the schema used to promise "Defaults to true for single and
	// multi" while an omitted allow_freeform stayed false (#804) - models
	// following the doc locked users out of freeform notes.
	raw := string((&AskUserTool{}).Parameters())
	s := raw
	if strings.Contains(s, "Defaults to true") {
		t.Fatal("schema still claims allow_freeform defaults to true")
	}
	if !strings.Contains(s, "Defaults to false") {
		t.Fatal("schema must state the real default (false)")
	}
}

func TestIssue1677WithFreeformAliasSetsFlag(t *testing.T) {
	// 4b: single_with_freeform with allow_freeform omitted must yield
	// AllowFreeform=true, not the zero value.
	for _, alias := range []string{"single_with_freeform", "multi_with_freeform"} {
		q, err := normalizeAskUserQuestion(0, AskUserQuestion{
			Title:   "t",
			Prompt:  "p",
			Kind:    alias,
			Choices: []AskUserChoice{{Label: "a"}, {Label: "b"}},
		})
		if err != nil {
			t.Fatalf("%s: %v", alias, err)
		}
		if !q.AllowFreeform {
			t.Fatalf("%s alias did not set AllowFreeform", alias)
		}
	}
	// plain kinds stay opt-in only
	q, err := normalizeAskUserQuestion(0, AskUserQuestion{
		Title: "t", Prompt: "p", Kind: "single",
		Choices: []AskUserChoice{{Label: "a"}, {Label: "b"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if q.AllowFreeform {
		t.Fatal("plain single must default AllowFreeform=false (#804)")
	}
}
