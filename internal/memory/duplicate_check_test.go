package memory

import (
	"testing"
)

func TestCheckDuplicateExact(t *testing.T) {
	dir := t.TempDir()
	am := &AutoMemory{dir: dir}

	am.SaveMemory("build-process", "content 1")

	dc := am.CheckDuplicate("build-process", "new content")
	if !dc.IsDuplicate() {
		t.Error("expected exact key match to be duplicate")
	}
	if dc.Similarity != 1.0 {
		t.Errorf("expected similarity 1.0, got %f", dc.Similarity)
	}
}

func TestCheckDuplicateSimilar(t *testing.T) {
	dir := t.TempDir()
	am := &AutoMemory{dir: dir}

	am.SaveMemory("build-process", "content 1")
	am.SaveMemory("release-process", "content 2")

	dc := am.CheckDuplicate("build-process-v2", "")
	// "build-process-v2" shares "build" and "process" with "build-process"
	if dc.SimilarTo != "build-process" {
		t.Errorf("expected SimilarTo=build-process, got %q", dc.SimilarTo)
	}
	if !dc.IsDuplicate() {
		t.Errorf("expected duplicate (similarity >= 0.7), got %f", dc.Similarity)
	}
}

func TestCheckDuplicateDistinct(t *testing.T) {
	dir := t.TempDir()
	am := &AutoMemory{dir: dir}

	am.SaveMemory("build-process", "content 1")
	am.SaveMemory("api-gotcha", "content 2")

	dc := am.CheckDuplicate("database-migration", "")
	if dc.IsDuplicate() {
		t.Errorf("expected not duplicate, similarity=%f", dc.Similarity)
	}
}

func TestCheckDuplicateEmpty(t *testing.T) {
	dir := t.TempDir()
	am := &AutoMemory{dir: dir}

	dc := am.CheckDuplicate("anything", "content")
	if dc.IsDuplicate() {
		t.Error("expected not duplicate for empty store")
	}
}

func TestTokenize(t *testing.T) {
	tokens := tokenize("build-process_v2")
	expected := map[string]struct{}{
		"build":   {},
		"process": {},
		"v2":      {},
	}
	if len(tokens) != len(expected) {
		t.Fatalf("expected %d tokens, got %d", len(expected), len(tokens))
	}
	for tok := range expected {
		if _, ok := tokens[tok]; !ok {
			t.Errorf("expected token %q not found", tok)
		}
	}
}

func TestJaccardSimilarity(t *testing.T) {
	a := map[string]struct{}{"build": {}, "process": {}}
	b := map[string]struct{}{"build": {}, "process": {}, "v2": {}}

	sim := jaccardSimilarity(a, b)
	// intersection=2, union=3, sim=0.667
	if sim < 0.6 || sim > 0.7 {
		t.Errorf("expected ~0.667, got %f", sim)
	}
}

func TestJaccardSimilarityIdentical(t *testing.T) {
	a := map[string]struct{}{"build": {}, "process": {}}
	sim := jaccardSimilarity(a, a)
	if sim != 1.0 {
		t.Errorf("expected 1.0 for identical sets, got %f", sim)
	}
}

func TestFormatDuplicateWarningExact(t *testing.T) {
	dc := DuplicateCheck{SimilarTo: "build-process", Similarity: 1.0}
	w := dc.FormatDuplicateWarning("build-process")
	if w == "" {
		t.Error("expected non-empty warning")
	}
}

func TestFormatDuplicateWarningNone(t *testing.T) {
	dc := DuplicateCheck{SimilarTo: "", Similarity: 0.3}
	w := dc.FormatDuplicateWarning("new-key")
	if w != "" {
		t.Errorf("expected empty warning for low similarity, got %q", w)
	}
}
