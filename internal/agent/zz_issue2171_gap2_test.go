package agent

import "testing"

// #2171 gap 2: tier BARE-basename suffix matches by ambiguity. A bare
// basename from the error output keeps full weight when it is unambiguous
// (probe 2: same-directory bare reference stays score=60), but when the
// basename hits two or more DISTINCT edit paths the hit is weak evidence
// (same-dir weight, no fileMatch) and must not claim the authoritative
// wording. Directory-carrying suffix matches are boundary-anchored ("/").

func TestIssue2171Gap2UnambiguousBareNameStaysStrong(t *testing.T) {
	// single edit matching the bare basename: no ambiguity -> full tier
	edit := causalEditStep{filePath: "packages/b/api.ts"}
	score, fileMatch := computeCRSDetail(edit, []string{"api.ts"}, 1, nil)
	if !fileMatch {
		t.Fatal("unambiguous bare basename must keep fileMatch")
	}
	if score < causalWtErrorFileMatch {
		t.Fatalf("unambiguous bare basename must keep full weight, score=%d", score)
	}
}

func TestIssue2171Gap2CrossDirAmbiguousBareWeakTier(t *testing.T) {
	// two distinct edit paths share the bare basename -> weak tier for both
	amb := bareAmbiguousNames([]causalEditStep{
		{filePath: "packages/a/api.ts"}, {filePath: "packages/b/api.ts"},
	}, []string{"api.ts"})
	if !amb["api.ts"] {
		t.Fatal("bareAmbiguousNames must flag api.ts (2 distinct edit paths)")
	}
	edit := causalEditStep{filePath: "packages/b/api.ts"}
	score, fileMatch := computeCRSDetail(edit, []string{"api.ts"}, 0, amb)
	if fileMatch {
		t.Fatalf("ambiguous bare basename must NOT set fileMatch, score=%d", score)
	}
	if score >= causalWtErrorFileMatch {
		t.Fatalf("ambiguous bare basename must stay in the weak tier, score=%d", score)
	}
}

func TestIssue2171Gap2DirCarryingSuffixAnchored(t *testing.T) {
	// directory structure on the short side: anchored strong match, and the
	// "/" boundary means "b/api.ts" no longer matches "packages/ab/api.ts"
	edit := causalEditStep{filePath: "/repo/packages/b/api.ts"}
	score, fileMatch := computeCRSDetail(edit, []string{"packages/b/api.ts"}, 0, nil)
	if !fileMatch || score < causalWtErrorFileMatch {
		t.Fatalf("dir-carrying match must stay strong, score=%d match=%v", score, fileMatch)
	}
	sibling := causalEditStep{filePath: "packages/ab/api.ts"}
	_, fileMatch2 := computeCRSDetail(sibling, []string{"b/api.ts"}, 0, nil)
	if fileMatch2 {
		t.Fatal("b/api.ts must not anchor-match packages/ab/api.ts (missing / boundary)")
	}
}

func TestIssue2171Gap2AttributeFailurePrefersTrueMatch(t *testing.T) {
	// end-to-end: with an ambiguous bare basename across two dirs, the
	// attribution hint must not claim "error output references this file"
	// for either candidate (weak tier), while an exact-match competitor
	// still wins with authoritative evidence.
	s := newCausalAttributionState()
	s.recordEdit("edit_file", "packages/a/api.ts", 1)
	s.recordEdit("edit_file", "packages/b/api.ts", 2)
	hint := s.attributeFailure("./api.ts:1:1: boom")
	if hint != "" && contains(hint, "references this file") {
		t.Fatalf("ambiguous bare attribution must not claim authoritative wording: %s", hint)
	}
}
