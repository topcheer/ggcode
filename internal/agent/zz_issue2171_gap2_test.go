package agent

import "testing"

func TestIssue2171Gap2UnambiguousBareNameStaysStrong(t *testing.T) {

	edit := causalEditStep{filePath: "packages/b/api.ts"}
	score, fileMatch, _ := computeCRSDetail(edit, []string{"api.ts"}, 1, nil)
	if !fileMatch {
		t.Fatal("unambiguous bare basename must keep fileMatch")
	}
	if score < causalWtErrorFileMatch {
		t.Fatalf("unambiguous bare basename must keep full weight, score=%d", score)
	}
}

func TestIssue2171Gap2CrossDirAmbiguousBareWeakTier(t *testing.T) {

	amb := bareAmbiguousNames([]causalEditStep{
		{filePath: "packages/a/api.ts"}, {filePath: "packages/b/api.ts"},
	}, []string{"api.ts"})
	if !amb["api.ts"] {
		t.Fatal("bareAmbiguousNames must flag api.ts (2 distinct edit paths)")
	}
	edit := causalEditStep{filePath: "packages/b/api.ts"}
	score, fileMatch, _ := computeCRSDetail(edit, []string{"api.ts"}, 0, amb)
	if fileMatch {
		t.Fatalf("ambiguous bare basename must NOT set fileMatch, score=%d", score)
	}
	if score >= causalWtErrorFileMatch {
		t.Fatalf("ambiguous bare basename must stay in the weak tier, score=%d", score)
	}
}

func TestIssue2171Gap2DirCarryingSuffixAnchored(t *testing.T) {

	edit := causalEditStep{filePath: "/repo/packages/b/api.ts"}
	score, fileMatch, _ := computeCRSDetail(edit, []string{"packages/b/api.ts"}, 0, nil)
	if !fileMatch || score < causalWtErrorFileMatch {
		t.Fatalf("dir-carrying match must stay strong, score=%d match=%v", score, fileMatch)
	}
	sibling := causalEditStep{filePath: "packages/ab/api.ts"}
	_, fileMatch2, _ := computeCRSDetail(sibling, []string{"b/api.ts"}, 0, nil)
	if fileMatch2 {
		t.Fatal("b/api.ts must not anchor-match packages/ab/api.ts (missing / boundary)")
	}
}

func TestIssue2171Gap2AttributeFailurePrefersTrueMatch(t *testing.T) {

	s := newCausalAttributionState()
	s.recordEdit("edit_file", "packages/a/api.ts", 1)
	s.recordEdit("edit_file", "packages/b/api.ts", 2)
	hint := s.attributeFailure("./api.ts:1:1: boom")
	if hint != "" && contains(hint, "references this file") {
		t.Fatalf("ambiguous bare attribution must not claim authoritative wording: %s", hint)
	}
}
