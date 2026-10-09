package agent

// #3640 probes: (1) line-reference evidence must integrate in Chinese
// replies (第 42 行 / L42) via the digit fallback - the English "line 42"
// literal never appears there; (2) the isPathListOutput exemption must
// actually fire for grep files_with_matches output, whose trailing
// "N file(s) matched" summary line used to fail the every-line-is-a-path
// check.

import "testing"

func TestIssue3640_ChineseLineRefIntegrates(t *testing.T) {
	// English form still extracts as the full-match token.
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
	// Chinese replies integrate the evidence through the digit fallback.
	if !evidenceTokenInText("line 142", "第 142 行的 Foo 未定义，我来修复") {
		t.Fatal("Chinese 第 142 行 reply does not integrate 'line 142' evidence")
	}
	if !evidenceTokenInText("line 42", "看 L42 那行有拼写错误") {
		t.Fatal("L42-style reply does not integrate 'line 42' evidence")
	}
	// Digits embedded in longer numbers must NOT count (1422 != 142).
	if evidenceTokenInText("line 142", "代码 1422 行处有另一个问题") {
		t.Fatal("digit fallback matched inside a longer number")
	}
}

func TestIssue3640_PathListExemptionFiresForGrep(t *testing.T) {
	out := "internal/agent/agent.go\ninternal/agent/tool.go\ninternal/util/util.go\n\n3 file(s) matched\n"
	if !isPathListOutput(out) {
		t.Fatal("grep files_with_matches output with summary line must be exempt as path-list browsing")
	}
	// Genuine mixed content (real prose lines) is still not a path list.
	if isPathListOutput("see internal/agent/agent.go for details\n\nsome other text here") {
		t.Fatal("prose output must not be classified as path-list browsing")
	}
}
