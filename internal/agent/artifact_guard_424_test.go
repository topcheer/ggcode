package agent

import "testing"

func TestConflictMarkersDocExampleNoGuiltByAssociation(t *testing.T) {
	content := "Intro text\n" +
		"<<<<<<< HEAD\n" +
		"ours\n" +
		"=======\n" +
		"theirs\n" +
		">>>>>>> branch\n" +
		"Setext Heading\n" +
		"=======\n" +
		"Another Heading\n" +
		"=======\n"
	w := checkMergeConflictMarkers("doc.md", content)
	if w == "" {
		t.Fatal("expected warning for real conflict block")
	}

	if !containsSubstr(w, "3 git merge conflict markers") {
		t.Errorf("expected count of 3 markers, got: %s", w)
	}
}

func TestContentGrowthIgnoresBlankLines(t *testing.T) {

	oldC := ""
	for i := 0; i < 10; i++ {
		oldC += "real code line\n"
		for j := 0; j < 9; j++ {
			oldC += "\n"
		}
	}
	newC := ""
	for i := 0; i < 10; i++ {
		newC += "real code line\n"
	}
	if w := checkContentGrowth("f.go", oldC, newC); w != "" {
		t.Errorf("no growth expected (same substance), got: %s", w)
	}

	if w := checkContentGrowth("f.go", oldC, oldC+oldC+oldC+oldC+oldC); w == "" {
		t.Error("expected growth warning for 5x substantive duplication")
	}
}
