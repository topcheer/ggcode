package agent

import "testing"

func TestIssue3640_ChineseLineRefIntegrates(t *testing.T) {

	toks := extractEvidenceTokens("main.go:88: undefined: Foo\nsee line 142 of context")
	found := false
	for _, tok := range toks {
		if tok == "line 142" {
			found = true
		}
	}
	if !found {
		t.Fatalf("line-ref full-match token not extracted: %v", toks)
	}

	if !evidenceTokenInText("line 142", "第 142 行的 Foo 未定义，我来修复") {
		t.Fatal("Chinese 第 142 行 reply does not integrate 'line 142' evidence")
	}
	if !evidenceTokenInText("line 42", "看 L42 那行有拼写错误") {
		t.Fatal("L42-style reply does not integrate 'line 42' evidence")
	}

	if evidenceTokenInText("line 142", "代码 1422 行处有另一个问题") {
		t.Fatal("digit fallback matched inside a longer number")
	}
}

func TestIssue3640_PathListExemptionFiresForGrep(t *testing.T) {
	out := "internal/agent/agent.go\ninternal/agent/tool.go\ninternal/util/util.go\n\n3 file(s) matched\n"
	if !isPathListOutput(out) {
		t.Fatal("grep files_with_matches output with summary line must be exempt as path-list browsing")
	}

	if isPathListOutput("see internal/agent/agent.go for details\n\nsome other text here") {
		t.Fatal("prose output must not be classified as path-list browsing")
	}
}
