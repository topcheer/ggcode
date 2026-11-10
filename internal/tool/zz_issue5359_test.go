package tool

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/cron"
)

// #5359: cron_create's far_horizon_ack parameter was a documented no-op —
// declared in the Parameters schema and the struct, promised by
// docs/guide/configuration.md ("job is created and a WARNING is logged"),
// but the one-shot horizon guard only ever read args.Far, so
// far_horizon_ack=true callers were rejected with guidance pointing at the
// other parameter name. Resolution (option 2 from the issue): far_horizon_ack
// is removed entirely and far stays the single authoritative acknowledgement
// knob, matching cron_update, CreateAcknowledgedFar routing, the rejection
// message, and the WARNING suffix wording.
func newIssue5359TestScheduler() *cron.Scheduler {
	s := cron.NewScheduler(func(string, bool) {}, "")
	return s
}

func TestCronCreateTool_SingleFarKnobNoDeadTwin(t *testing.T) {
	s := newIssue5359TestScheduler()
	defer s.Shutdown()
	tool := CronCreateTool{Scheduler: s}

	schema := string(tool.Parameters())
	if strings.Contains(schema, "far_horizon_ack") {
		t.Fatalf("cron_create schema must not advertise the dead far_horizon_ack twin:\n%s", schema)
	}
	if !strings.Contains(schema, "\"far\"") {
		t.Fatal("cron_create schema must keep the wired far parameter")
	}

	// Sending the removed name must not acknowledge anything: the caller gets
	// the same fail-closed educational rejection as any unacknowledged
	// far-future one-shot, whose guidance points at the working knob.
	farExpr := oneShotExprFor(200)
	in := `{"cron":"` + farExpr + `","prompt":"far reminder","one_shot":true,"far_horizon_ack":true,"description":"t"}`
	res, err := tool.Execute(context.Background(), json.RawMessage(in))
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatalf("unknown far_horizon_ack must not acknowledge a far-future one-shot; expected rejection, got: %s", res.Content)
	}
	if !strings.Contains(res.Content, "one-shot cron rejected") {
		t.Fatalf("rejection must keep the educational message, got: %s", res.Content)
	}
	if !strings.Contains(res.Content, `"far": true`) {
		t.Fatalf("rejection guidance must point at the working far knob, got: %s", res.Content)
	}
}

func TestCronCreateTool_FarKnobAcceptsFarFuture(t *testing.T) {
	s := newIssue5359TestScheduler()
	defer s.Shutdown()
	tool := CronCreateTool{Scheduler: s}

	// The authoritative path stays green: far=true creates the job and
	// surfaces the loud far-future WARNING suffix.
	farExpr := oneShotExprFor(200)
	in := `{"cron":"` + farExpr + `","prompt":"far reminder","one_shot":true,"far":true,"description":"t"}`
	res, err := tool.Execute(context.Background(), json.RawMessage(in))
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("far=true must still accept a far-future one-shot, got: %s", res.Content)
	}
	if !strings.Contains(res.Content, "WARNING: far-future one-shot accepted") {
		t.Fatalf("accepted creation must surface the WARNING suffix, got: %s", res.Content)
	}
}
