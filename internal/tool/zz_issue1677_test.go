package tool

import (
	"strings"
	"testing"
)

func TestIssue1677SchemaDocMatchesDefaultFalse(t *testing.T) {

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
