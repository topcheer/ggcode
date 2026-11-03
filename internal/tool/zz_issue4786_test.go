package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Issue #4786 (S-15): multi_edit_file non-unique anchors must report match
// line numbers, and a batch whose edits overlap must surface the overlap in
// the same first plan round instead of failing a later round after the
// non-unique anchor is fixed.

func TestIssue4786_MultiEditNonUniqueAnchorReportsLineNumbers(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "i18n.go")
	content := "header\nswitch x {\ncase \"save\":\n\tlabel = \"OK\"\ncase \"cancel\":\n\tlabel = \"NO\"\n}\nswitch y {\ncase \"save\":\n\tlabel = \"YES\"\ncase \"cancel\":\n\tlabel = \"NOPE\"\n}\nfooter\n"
	if err := os.WriteFile(fp, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	me := MultiEditFile{}
	input, _ := json.Marshal(map[string]interface{}{
		"file_path": fp,
		"edits": []map[string]string{
			{"old_text": "case \"save\":", "new_text": "case \"saveAs\":"},
		},
	})
	result, err := me.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatalf("expected uniqueness error, got success: %s", result.Content)
	}
	if !strings.Contains(result.Content, "found 2 times") {
		t.Errorf("expected match count in error, got: %s", result.Content)
	}
	if !strings.Contains(result.Content, "Matches start at line(s): 3, 9.") {
		t.Errorf("expected match line numbers 3, 9 in error, got: %s", result.Content)
	}
}

func TestIssue4786_MultiEditOverlapSurfacesInFirstPlanRound(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "blocks.txt")
	// Structurally identical blocks: non-unique anchor at edits[0] plus an
	// overlapping pair at edits[1]/edits[2]. Pre-fix behavior returned only
	// the edits[0] uniqueness error; the overlap fired in a second round
	// after the anchor was disambiguated.
	content := "alpha\ndup block\nbeta\ndup block\ngamma\nmiddle segment here\ndelta\n"
	if err := os.WriteFile(fp, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	me := MultiEditFile{}
	input, _ := json.Marshal(map[string]interface{}{
		"file_path": fp,
		"edits": []map[string]string{
			{"old_text": "dup block", "new_text": "unique A"},
			{"old_text": "gamma\nmiddle segment", "new_text": "GAMMA"},
			{"old_text": "middle segment here", "new_text": "SEGMENT"},
		},
	})
	result, err := me.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatalf("expected error, got success: %s", result.Content)
	}
	if !strings.Contains(result.Content, "found 2 times") {
		t.Errorf("expected edits[0] uniqueness error in first round, got: %s", result.Content)
	}
	if !strings.Contains(result.Content, "Matches start at line(s): 2, 4.") {
		t.Errorf("expected edits[0] match line numbers in first round, got: %s", result.Content)
	}
	if !strings.Contains(result.Content, "edits[1] and edits[2]: overlapping matches") {
		t.Errorf("expected overlap conflict surfaced in first round, got: %s", result.Content)
	}
}

func TestIssue4786_MultiEditAggregatesAllPerEditErrors(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "mixed.txt")
	content := "one\ntwo\nthree\nfour\nfive\n"
	if err := os.WriteFile(fp, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	me := MultiEditFile{}
	input, _ := json.Marshal(map[string]interface{}{
		"file_path": fp,
		"edits": []map[string]string{
			{"old_text": "two", "new_text": "TWO"},
			{"old_text": "missing anchor", "new_text": "X"},
			{"old_text": "four", "new_text": "FOUR"},
		},
	})
	result, err := me.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatalf("expected error, got success: %s", result.Content)
	}
	// edits[1] not-found must not mask valid edits; the whole batch is
	// rejected, and the error must name the failing edit index.
	if !strings.Contains(result.Content, "edits[1]: old_text not found in file") {
		t.Errorf("expected edits[1] not-found error, got: %s", result.Content)
	}
}

func TestIssue4786_MatchLineHintTruncationNote(t *testing.T) {
	var sb strings.Builder
	for i := 0; i < 15; i++ {
		sb.WriteString("dup\n")
	}
	hint := MatchLineHint(15, MatchLines(sb.String(), "dup"))
	if !strings.Contains(hint, "Matches start at line(s): 1, 2, 3, 4, 5, 6, 7, 8, 9, 10 (showing first 10).") {
		t.Errorf("unexpected truncation note in hint: %s", hint)
	}
	if hint := MatchLineHint(0, nil); hint != "" {
		t.Errorf("expected empty hint for no matches, got: %s", hint)
	}
}

func TestIssue4786_MultiEditLooseMatchReportsLineNumbers(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "blocks.txt")
	// Two structurally identical blocks whose indentation differs from the
	// probe old_text: whitespace-tolerant matching counts both.
	content := "alpha\n\t\tif x {\n\t\t\tdo()\n\t\t}\nbeta\n\tif x {\n\t\tdo()\n\t}\ngamma\n"
	if err := os.WriteFile(fp, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	me := MultiEditFile{}
	input, _ := json.Marshal(map[string]interface{}{
		"file_path": fp,
		"edits": []map[string]string{
			{"old_text": "\t if x {\n\t\t do()\n\t }", "new_text": "X"},
		},
	})
	result, err := me.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatalf("expected loose-match uniqueness error, got success: %s", result.Content)
	}
	if !strings.Contains(result.Content, "whitespace-tolerant matching") {
		t.Errorf("expected whitespace-tolerant error, got: %s", result.Content)
	}
	if !strings.Contains(result.Content, "Loose matches start at line(s): 2, 6.") {
		t.Errorf("expected loose match line numbers, got: %s", result.Content)
	}
}
