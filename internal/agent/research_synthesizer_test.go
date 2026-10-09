package agent

import (
	"strings"
	"testing"
)

func TestUnquoteJSONString(t *testing.T) {
	cases := []struct {
		in, key, want string
	}{
		{`{"query":"deep research agents"}`, "query", "deep research agents"},
		{`{"url":"https://arxiv.org/abs/2506.18096"}`, "url", "https://arxiv.org/abs/2506.18096"},
		{`{"other":1,"query":"x y"}`, "query", "x y"},
		{`{"nq":"v"}`, "query", ""},
		{`not json`, "query", ""},
	}
	for _, c := range cases {
		if got := unquoteJSONString(c.in, c.key); got != c.want {
			t.Errorf("unquoteJSONString(%q,%q)=%q want %q", c.in, c.key, got, c.want)
		}
	}
}

func TestRecordFinding_CollectsAndFilters(t *testing.T) {
	o := &overseerState{}
	o.recordFinding("web_search", `{"query":"agent memory"}`, "Results: 1. https://a.io/x - great post about memory")
	if len(o.researchFindings) != 1 {
		t.Fatalf("web_search must record, got %d", len(o.researchFindings))
	}
	f := o.researchFindings[0]
	if f.Query != "agent memory" || f.URL != "https://a.io/x" {
		t.Errorf("finding fields wrong: %+v", f)
	}
	o.recordFinding("read_file", `{"path":"/etc"}`, "contents")
	if len(o.researchFindings) != 1 {
		t.Error("non-retrieval tools must not record")
	}
	o.recordFinding("web_fetch", `{"url":"https://b.io/y"}`, "plain page without links")
	f2 := o.researchFindings[1]
	if f2.URL != "https://b.io/y" {
		t.Errorf("web_fetch without in-page URL must fall back to the arg URL: %+v", f2)
	}
}

func TestRecordFinding_Cap(t *testing.T) {
	o := &overseerState{}
	for i := 0; i < maxResearchFindings+5; i++ {
		o.recordFinding("web_search", `{"query":"q"}`, "x")
	}
	if len(o.researchFindings) != maxResearchFindings {
		t.Fatalf("cap must be honored, got %d", len(o.researchFindings))
	}
}

func TestFindingsSnapshot_CopySemantics(t *testing.T) {
	o := &overseerState{}
	o.recordFinding("web_search", `{"query":"q"}`, "s")
	snap := o.findingsSnapshot()
	snap[0].Query = "mutated"
	if o.researchFindings[0].Query == "mutated" {
		t.Fatal("snapshot must be a copy, mutation leaked into state")
	}
}

func TestSynthesizeResearchReport_Empty(t *testing.T) {
	if got := SynthesizeResearchReport("goal", nil, nil); got != "" {
		t.Fatalf("no findings must yield empty draft, got %q", got)
	}
}

func TestSynthesizeResearchReport_GroupsAndNumbers(t *testing.T) {
	findings := []ResearchFinding{
		{Tool: "web_search", Query: "agent memory", URL: "https://a.io/x", Snippet: "s1"},
		{Tool: "web_search", Query: "agent memory", URL: "https://b.org/y", Snippet: "s2"},
		{Tool: "web_fetch", Query: "https://c.com/z", URL: "https://c.com/z", Snippet: "s3"},
	}
	draft := SynthesizeResearchReport("goal", findings, nil)
	for _, want := range []string{
		"[Research Synthesis Draft]",
		"## Sub-query: agent memory",
		"- [src 1] https://a.io/x (web_search): s1",
		"- [src 2] https://b.org/y (web_search): s2",
		"## Sub-query: https://c.com/z",
		"2 distinct hosts", // conflict candidate note within the group
	} {
		if !strings.Contains(draft, want) {
			t.Errorf("draft missing %q:\n%s", want, draft)
		}
	}
}

func TestSynthesizeResearchReport_GapsAgainstPlan(t *testing.T) {
	findings := []ResearchFinding{
		{Tool: "web_search", Query: "agent memory architecture", URL: "https://a.io/x", Snippet: "s"},
	}
	planned := []ResearchSubQuery{
		{Perspective: "technical", Query: "agent memory architecture technical details and mechanisms", Priority: 1},
		{Perspective: "risk", Query: "known risks of vector databases", Priority: 4},
	}
	draft := SynthesizeResearchReport("goal", findings, planned)
	if strings.Contains(draft, "agent memory architecture technical details") {
		t.Errorf("covered sub-query must not be listed as a gap:\n%s", draft)
	}
	if !strings.Contains(draft, "## Uncovered sub-queries (gaps)") {
		t.Errorf("gap section missing:\n%s", draft)
	}
	if !strings.Contains(draft, "known risks of vector databases") {
		t.Errorf("uncovered planned sub-query missing from gaps:\n%s", draft)
	}
}

func TestTrimFindingSnippet(t *testing.T) {
	long := strings.Repeat("word ", 100)
	got := trimFindingSnippet(long)
	if len(got) > researchSnippetCap+1 {
		t.Errorf("snippet cap exceeded: %d", len(got))
	}
	if got := trimFindingSnippet("  a\t b\n c "); got != "a b c" {
		t.Errorf("whitespace not collapsed: %q", got)
	}
}
