package agent

import "testing"

// #2218 case A: directory-suffix references get the same ambiguity probe
// as bare basenames - anchored is not unique. Two distinct edit paths
// ending in "/api/types.go" both match a relative "api/types.go" compile
// error; the tie must tier down to weak evidence, not let recency win
// with the max-authority "references this file" wording.

func TestIssue2218SuffixAmbiguityFlagged(t *testing.T) {
	amb := bareAmbiguousNames([]causalEditStep{
		{filePath: "pkg/api/types.go"}, {filePath: "internal/api/types.go"},
	}, []string{"api/types.go"})
	if !amb["api/types.go"] {
		t.Fatal("dir-suffix reference matching 2 distinct edit paths must be flagged ambiguous")
	}
}

func TestIssue2218AmbiguousSuffixTiersDown(t *testing.T) {
	amb := bareAmbiguousNames([]causalEditStep{
		{filePath: "pkg/api/types.go"}, {filePath: "internal/api/types.go"},
	}, []string{"api/types.go"})
	for _, editPath := range []string{"pkg/api/types.go", "internal/api/types.go"} {
		score, fileMatch := computeCRSDetail(
			causalEditStep{filePath: editPath}, []string{"api/types.go"}, 0, amb)
		if fileMatch {
			t.Fatalf("%s: ambiguous suffix match must NOT set fileMatch (authority wording)", editPath)
		}
		if score >= causalWtErrorFileMatch {
			t.Fatalf("%s: ambiguous suffix must stay weak tier, score=%d", editPath, score)
		}
	}
}

func TestIssue2218UniqueSuffixStaysStrong(t *testing.T) {
	// only one edit path ends in /api/types.go -> unambiguous, full weight
	amb := bareAmbiguousNames([]causalEditStep{
		{filePath: "pkg/api/types.go"}, {filePath: "internal/other/thing.go"},
	}, []string{"api/types.go"})
	if amb["api/types.go"] {
		t.Fatal("unique suffix must not be flagged")
	}
	score, fileMatch := computeCRSDetail(
		causalEditStep{filePath: "pkg/api/types.go"}, []string{"api/types.go"}, 0, amb)
	if !fileMatch || score < causalWtErrorFileMatch {
		t.Fatalf("unique suffix must stay strong, score=%d match=%v", score, fileMatch)
	}
}

// Regression: the bare-name probe keeps its exact #2171 gap-2 semantics.
func TestIssue2218BareProbeUnchanged(t *testing.T) {
	amb := bareAmbiguousNames([]causalEditStep{
		{filePath: "packages/a/api.ts"}, {filePath: "packages/b/api.ts"},
	}, []string{"api.ts"})
	if !amb["api.ts"] {
		t.Fatal("bare-name ambiguity regression")
	}
	if amb["packages/b/api.ts"] {
		t.Fatal("dir-carrying refs only match via the suffix anchor, base probe must not fire for them")
	}
}
