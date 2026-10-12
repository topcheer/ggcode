package tool

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/cron"
)

// #4049: Pause() clears job.NextFire to the zero time (#311 invariant), but
// CronList/CronGet rendered it as the 0001-01-01 fake timestamp while
// CronUpdate already guarded it (L284 pattern). Both renderers must show
// "none (paused)" instead, and active jobs keep the RFC3339 timestamp.
func TestIssue4049PausedNextFireRendering(t *testing.T) {
	s := cron.NewScheduler(nil, "")
	defer s.Shutdown()

	// Job IDs carry a per-job hash suffix (e.g. cron-1-43a49b5c), so capture
	// the real ID from each create result instead of assuming "cron-N".
	create := CronCreateTool{Scheduler: s}
	var first, second struct{ ID string }
	res, err := create.Execute(context.Background(), json.RawMessage(`{"cron":"0 9 * * *","prompt":"morning report","recurring":true}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(res.Content), &first); err != nil {
		t.Fatalf("parse create result %q: %v", res.Content, err)
	}
	res, err = create.Execute(context.Background(), json.RawMessage(`{"cron":"30 10 * * *","prompt":"second","recurring":true}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(res.Content), &second); err != nil {
		t.Fatalf("parse create result %q: %v", res.Content, err)
	}
	if err := s.Pause(first.ID); err != nil {
		t.Fatal(err)
	}

	// CronList: paused job renders none (paused), active job keeps a real timestamp.
	list := CronListTool{Scheduler: s}
	res, err = list.Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(res.Content, "0001-01-01") {
		t.Fatalf("CronList must not render the zero-time fake timestamp, got: %s", res.Content)
	}
	if !strings.Contains(res.Content, first.ID+" [paused, recurring, skip-if-busy] 0 9 * * * next=none (paused)") {
		t.Fatalf("paused job must render next=none (paused), got: %s", res.Content)
	}
	if !strings.Contains(res.Content, second.ID) || !strings.Contains(res.Content, "next=2") {
		// active job: timestamp year starts with "2" (20xx), not "0" (0001)
		t.Fatalf("active job must keep its RFC3339 next fire, got: %s", res.Content)
	}

	// CronGet: same guard on the detailed view.
	get := CronGetTool{Scheduler: s}
	res, err = get.Execute(context.Background(), json.RawMessage(`{"jobId":"`+first.ID+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(res.Content, "0001-01-01") {
		t.Fatalf("CronGet must not render the zero-time fake timestamp, got: %s", res.Content)
	}
	if !strings.Contains(res.Content, "Next fire:   none (paused)") {
		t.Fatalf("paused job detail must render 'Next fire:   none (paused)', got: %s", res.Content)
	}

	// Active job detail keeps a real timestamp (no regression).
	res, err = get.Execute(context.Background(), json.RawMessage(`{"jobId":"`+second.ID+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Content, "Next fire:   2") {
		t.Fatalf("active job detail must keep its RFC3339 next fire, got: %s", res.Content)
	}
}
