package tool

// zz_r14_teamreview_test.go -- companion tests for team_review_digest /
// team_review_decide (r14: scaled oversight batch verdict surface).
import (
	"context"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/task"
)

func TestR14DigestRendersAllTeammates(t *testing.T) {
	items := []teamReviewItem{
		{TeammateID: "tm-1", TeammateName: "coder", TaskSubject: "fix login bug", HasResult: true, Summary: "fixed\nwith tests", Confident: true},
		{TeammateID: "tm-2", TeammateName: "tester", TaskSubject: "write e2e", HasResult: true, Summary: "draft only", Confident: false},
		{TeammateID: "tm-3", TeammateName: "docs", HasResult: false},
	}
	out := renderTeamReviewDigest(items)
	for _, want := range []string{"3 teammate(s)", "coder (tm-1) [VERIFIED]", "tester (tm-2) [unverified]", "docs (tm-3)", "task: fix login bug", "(no result yet)", "team_review_decide"} {
		if !strings.Contains(out, want) {
			t.Errorf("digest missing %q:\n%s", want, out)
		}
	}
	if renderTeamReviewDigest(nil) != "No teammate output pending review.\n" {
		t.Error("empty digest must say no pending output")
	}
}

func TestR14SummarizeAndConfidence(t *testing.T) {
	long := strings.Repeat("line\n", 10)
	s := summarizeResult(long, 3, 600)
	if got := strings.Count(s, "line"); got != 3 {
		t.Errorf("want 3 lines, got %d", got)
	}
	if !resultClaimsVerification("did stuff\nall tests passed (3/3)") {
		t.Error("tail 'passed' must set confidence")
	}
	if resultClaimsVerification("tests passed early\n" + strings.Repeat("then everything broke and nothing was rechecked ", 20)) {
		t.Error("only the tail should count for confidence")
	}
}

func TestR14ApprovePathCompletesBoardTask(t *testing.T) {
	tm := task.NewManager()
	mine := tm.Create("fix bug", "desc", "fixing", map[string]string{"owner": "tm-1"})
	inProgress := task.TaskStatus("in_progress")
	if _, err := tm.Update(mine.ID, task.UpdateOptions{Status: &inProgress, Owner: strPtr("tm-1")}); err != nil {
		t.Fatal(err)
	}
	other := tm.Create("other task", "", "", nil)
	if _, err := tm.Update(other.ID, task.UpdateOptions{Owner: strPtr("tm-9")}); err != nil {
		t.Fatal(err)
	}
	got, ok := latestTeammateTask(tm, "tm-1")
	if !ok || got.ID != mine.ID {
		t.Fatalf("latestTeammateTask = %+v ok=%v", got, ok)
	}
	// The exact update the approve branch performs:
	completed := task.TaskStatus("completed")
	if _, err := tm.Update(got.ID, task.UpdateOptions{Status: &completed}); err != nil {
		t.Fatal(err)
	}
	final, _ := tm.Get(got.ID)
	if final.Status != "completed" {
		t.Errorf("task not completed: %s", final.Status)
	}
	if _, ok := latestTeammateTask(tm, "tm-nope"); ok {
		t.Error("unknown teammate must not match any task")
	}
	if _, ok := latestTeammateTask(nil, "tm-1"); ok {
		t.Error("nil manager must not match")
	}
}

func TestR14DecideValidation(t *testing.T) {
	// nil manager: guarded before any team lookup
	res, err := TeamReviewDecideTool{}.Execute(context.Background(), []byte(`{"team_id":"t","teammate_id":"tm-1","verdict":"approve"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError || !strings.Contains(res.Content, "not available") {
		t.Errorf("nil manager must error: %+v", res)
	}
}

func strPtr(s string) *string { return &s }
