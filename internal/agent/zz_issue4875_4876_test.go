package agent

import (
	"encoding/json"
	"testing"
)

// #4875: multi_file_edit's edits live under files[].edits, not a top-level
// "edits" key - the complexity gate and the intent key both read the wrong
// shape, so the largest-blast-radius write tool scored a constant 0.
func TestIssue4875_MultiFileEditShapeAware(t *testing.T) {
	filesShape := map[string]json.RawMessage{
		"files": json.RawMessage(`[
			{"path":"a.go","edits":[{"old_text":"x","new_text":"y"},{"old_text":"z","new_text":""}]},
			{"path":"b.go","edits":[{"old_text":"p","new_text":"q"},{"old_text":"r","new_text":"s"},{"old_text":"t","new_text":"u"},{"old_text":"v","new_text":"w"},{"old_text":"m","new_text":"n"}]}
		]`),
	}
	// 7 edits (>=5) + 2 deletes -> score must be > 0 now (was constant 0).
	score, dims := scoreArgComplexity("multi_file_edit", filesShape)
	if score == 0 {
		t.Fatal("#4875: multi_file_edit files[].edits shape must score > 0")
	}
	if len(dims) == 0 {
		t.Fatal("#4875: dimension list must not be empty for a 7-edit call")
	}

	// multi_edit_file's top-level shape still works (regression guard).
	flatShape := map[string]json.RawMessage{
		"edits": json.RawMessage(`[{"old_text":"a","new_text":"b"},{"old_text":"c","new_text":""},{"old_text":"d","new_text":""}]`),
	}
	score2, _ := scoreArgComplexity("multi_edit_file", flatShape)
	if score2 == 0 {
		t.Fatal("multi_edit_file top-level edits shape must keep scoring")
	}

	// Intent keys must DIFFER between a files[] call and a flat call with
	// different edit counts (was: constant projection for files[] shape).
	key1 := argComplexityIntentKey("multi_file_edit", filesShape)
	key2 := argComplexityIntentKey("multi_edit_file", flatShape)
	if key1 == key2 {
		t.Fatalf("intent keys must differ across shapes/counts:\n%s\n%s", key1, key2)
	}
	// ...and two multi_file_edit calls with different edit counts differ too.
	fewerFiles := map[string]json.RawMessage{
		"files": json.RawMessage(`[{"path":"a.go","edits":[{"old_text":"x","new_text":"y"}]}]`),
	}
	if argComplexityIntentKey("multi_file_edit", fewerFiles) == key1 {
		t.Fatal("intent key must reflect edit count, not stay constant")
	}
}

// #4876: pushShapeRe must recognize the FORCE-update receipt
// (" + 1a2b3c4...5d6e7f8  br -> origin/br (forced update)") - the highest
// stale-review-risk form - while still matching fast-forward receipts.
func TestIssue4876_PushShapeMatchesForceUpdate(t *testing.T) {
	forced := "To github.com:topcheer/ggcode.git\n + 1a2b3c4...5d6e7f8  main -> origin/main (forced update)\n"
	if !pushShapeRe.MatchString(forced) {
		t.Fatal("#4876: force-update receipt must match pushShapeRe")
	}
	ff := "To github.com:topcheer/ggcode.git\n   1a2b3c4..5d6e7f8  main -> origin/main\n"
	if !pushShapeRe.MatchString(ff) {
		t.Fatal("fast-forward receipt must keep matching (regression)")
	}
	newBranch := " * [new branch]      main -> origin/main\n"
	if pushShapeRe.MatchString(newBranch) {
		t.Fatal("new-branch receipt must stay unmatched (benign)")
	}
}
