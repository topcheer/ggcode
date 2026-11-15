package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/cron"
)

// Regression test for #5673: after a one-shot job fires it self-deletes; a
// follow-up cron_delete for the same jobId must be idempotent (success with
// explanation), not an error that breaks management scripts.
func TestIssue5673CronDeleteIdempotentAfterOneShotFire(t *testing.T) {
	s := cron.NewScheduler(nil, "")
	defer s.Shutdown()

	createTool := CronCreateTool{Scheduler: s}
	// Compute a future-month cron expression dynamically so the test never rots
	// as wall-clock time passes (oneShotPastDateRejection rejects past dates).
	now := time.Now().UTC()
	future := now.AddDate(0, 1, 0)
	cronExpr := fmt.Sprintf("0 0 1 %d *", int(future.Month()))
	res, err := createTool.Execute(context.Background(), json.RawMessage(fmt.Sprintf(`{"cron":%q,"prompt":"expired one-shot","recurring":false}`, cronExpr)))
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("create failed: %s", res.Content)
	}
	// Parse the real job ID from the create result. The result content is the
	// job JSON object followed by human-readable suffix lines (e.g. the
	// one-shot self-delete note), so cut at the last '}' before unmarshalling.
	// IDs are dynamic "cron-<n>-<suffix>" forms, never a fixed "cron-1"; the
	// JSON field is capitalized "ID".
	var created struct {
		ID string `json:"ID"`
	}
	jsonPart := res.Content
	if idx := strings.LastIndex(jsonPart, "}"); idx >= 0 {
		jsonPart = jsonPart[:idx+1]
	}
	if err := json.Unmarshal([]byte(jsonPart), &created); err != nil {
		t.Fatalf("parse create result: %v (%s)", err, res.Content)
	}
	if created.ID == "" {
		t.Fatalf("create result missing id: %s", res.Content)
	}

	deleteTool := CronDeleteTool{Scheduler: s}

	// First delete: job exists -> deleted.
	first, err := deleteTool.Execute(context.Background(), json.RawMessage(fmt.Sprintf(`{"jobId":%q}`, created.ID)))
	if err != nil {
		t.Fatal(err)
	}
	if first.IsError {
		t.Fatalf("first delete failed: %s", first.Content)
	}
	if !strings.Contains(first.Content, "deleted") {
		t.Errorf("first delete should confirm deletion, got: %s", first.Content)
	}

	// Second delete: already gone -> idempotent tolerant success (#5673).
	second, err := deleteTool.Execute(context.Background(), json.RawMessage(fmt.Sprintf(`{"jobId":%q}`, created.ID)))
	if err != nil {
		t.Fatal(err)
	}
	if second.IsError {
		t.Fatalf("second delete must tolerate already-gone job, got error: %s", second.Content)
	}
	if !strings.Contains(second.Content, "already gone") {
		t.Errorf("second delete should explain, got: %s", second.Content)
	}
}
