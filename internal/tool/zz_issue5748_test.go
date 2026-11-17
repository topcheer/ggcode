package tool

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/cron"
)

// Regression guard for #5748: tool Result.Content that leads with a JSON
// value must stay parseable as that single value - downstream tests and
// tools Unmarshal either the whole Content (after TrimSpace) or its first
// line. The raw-text suffix append shape (json + "\nNote: ...") broke 4
// whole-content callers during the #5745 cycle before the #5746 rework;
// these tests pin the contract detector, the note-field envelope, and the
// two remediated producers (cron_create one-shot, swarm_task_create).

func TestIssue5748LeadingJSONViolationDetector(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    bool // want a violation reported
	}{
		{"pure object", "{\"ID\":\"j1\"}", false},
		{"pure array", "[1,2,3]", false},
		{"object trailing newline", "{\"ID\":\"j1\"}\n", false},
		{"object trailing mixed whitespace", "{\"ID\":\"j1\"} \n\t ", false},
		{"leading whitespace then object", "\n\t {\"ID\":\"j1\"}", false},
		{"plain text content", "Job j1 deleted\n", false},
		{"plain text starting with parens", "(one-shot: fires once)\n", false},
		{"empty content", "", false},
		{"whitespace only", "\n \t", false},
		// The #5745 regression shape: JSON payload with a raw text suffix.
		{"raw text suffix on new line", "{\"ID\":\"j1\"}\n(one-shot: fires once and is removed automatically)", true},
		{"raw text suffix same line", "{\"ID\":\"j1\"} created ok", true},
		{"raw text suffix after array", "[1,2]\nNote: blocked by uncompleted dependencies", true},
		{"leading whitespace object plus suffix", "\n {\"ID\":\"j1\"}\nnext fire: soon", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := LeadingJSONViolation(tc.content)
			if tc.want && got == "" {
				t.Errorf("LeadingJSONViolation(%q) = \"\", want a violation", tc.content)
			}
			if !tc.want && got != "" {
				t.Errorf("LeadingJSONViolation(%q) = %q, want none", tc.content, got)
			}
		})
	}
	// Malformed JSON-leading content reports a (different) violation so the
	// guard does not silently wave through unparseable payloads.
	if v := LeadingJSONViolation("{\"ID\":"); v == "" {
		t.Error("LeadingJSONViolation on malformed leading object should report a parse failure")
	}
}

func TestIssue5748JSONContentWithNoteEnvelope(t *testing.T) {
	payload := struct {
		ID        string
		Recurring bool
	}{"j1", false}

	out, err := jsonContentWithNote(payload, "(one-shot: fires once)\nnext fire: soon")
	if err != nil {
		t.Fatalf("jsonContentWithNote: %v", err)
	}
	if v := LeadingJSONViolation(out); v != "" {
		t.Fatalf("envelope violates its own contract: %s (content: %s)", v, out)
	}
	var got struct {
		ID        string
		Recurring bool
		Note      string
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("whole-content Unmarshal must work: %v (content: %s)", err, out)
	}
	if got.ID != "j1" || got.Recurring {
		t.Errorf("payload fields lost in envelope: %+v (content: %s)", got, out)
	}
	if got.Note != "(one-shot: fires once)\nnext fire: soon" {
		t.Errorf("note field mismatch: %q (content: %s)", got.Note, out)
	}

	// Empty note adds no field (recurring payloads keep their bare shape).
	bare, err := jsonContentWithNote(payload, "")
	if err != nil {
		t.Fatalf("jsonContentWithNote empty note: %v", err)
	}
	if strings.Contains(bare, "note") {
		t.Errorf("empty note must not add a note field: %s", bare)
	}
	if v := LeadingJSONViolation(bare); v != "" {
		t.Errorf("bare envelope violates contract: %s", v)
	}

	// Non-object payloads cannot carry the envelope field: refuse loudly.
	if _, err := jsonContentWithNote([]int{1, 2}, "note"); err == nil {
		t.Error("array payload must be rejected, got success")
	}

	// A payload that already carries a note field must not be overwritten.
	withNote := map[string]string{"note": "existing"}
	if _, err := jsonContentWithNote(withNote, "replacement"); err == nil {
		t.Error("existing note field must be rejected, got success")
	}
}

// Behavioral pin: the cron_create one-shot result (the exact #5745/#5746
// surface, note + next-fire echo + acknowledged-rollover warning) must
// Unmarshal as a whole, not just by first line. This fails at the pre-#5748
// tree, where the notes were raw-concatenated after the job JSON.
func TestIssue5748CronCreateOneShotWholeContentParses(t *testing.T) {
	s := cron.NewScheduler(func(string, bool) {}, "")
	defer s.Shutdown()
	tool := CronCreateTool{Scheduler: s}

	result, err := tool.Execute(context.Background(), json.RawMessage(
		"{\"cron\": \"0 0 * * *\", \"prompt\": \"tonight follow-up\", \"one_shot\": true, \"tomorrow\": true}"))
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("acknowledged one-shot create must succeed, got: %s", result.Content)
	}
	if v := LeadingJSONViolation(result.Content); v != "" {
		t.Fatalf("cron_create violates the leading-JSON contract: %s (content: %s)", v, result.Content)
	}
	var job struct {
		ID   string
		Note string
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(result.Content)), &job); err != nil {
		t.Fatalf("whole-content Unmarshal must succeed (raw suffix appends break it): %v\ncontent: %s", err, result.Content)
	}
	if job.ID == "" {
		t.Fatalf("created job JSON missing ID: %s", result.Content)
	}
	for _, want := range []string{
		"(one-shot: fires once and is removed automatically",
		"next fire: ",
		"WARNING: same-day overshoot accepted via tomorrow=true",
	} {
		if !strings.Contains(result.Content, want) {
			t.Errorf("create result should still carry %q, got: %s", want, result.Content)
		}
	}
}

// Behavioral pin: the swarm_task_create blocked-dependency result must
// Unmarshal as a whole. This fails at the pre-#5748 tree, where the blocked
// note was raw-concatenated after the task JSON.
func TestIssue5748SwarmCreateBlockedWholeContentParses(t *testing.T) {
	mgr := swarmTestManager(t)
	team := mgr.CreateTeam("5748", "leader")
	tm, err := mgr.EnsureTaskManager(team.ID)
	if err != nil {
		t.Fatal(err)
	}
	dep := tm.Create("prerequisite", "", "", nil)

	createTool := SwarmTaskCreateTool{Manager: mgr}
	ci, _ := json.Marshal(map[string]interface{}{
		"team_id":     team.ID,
		"subject":     "downstream",
		"description": "needs the prerequisite done first",
		"depends_on":  []string{dep.ID},
	})
	cr, err := createTool.Execute(context.Background(), ci)
	if err != nil {
		t.Fatal(err)
	}
	if cr.IsError {
		t.Fatalf("create with depends_on failed: %s", cr.Content)
	}
	if v := LeadingJSONViolation(cr.Content); v != "" {
		t.Fatalf("swarm_task_create violates the leading-JSON contract: %s (content: %s)", v, cr.Content)
	}
	var created struct {
		ID   string
		Note string
	}
	if err := json.Unmarshal([]byte(cr.Content), &created); err != nil {
		t.Fatalf("whole-content Unmarshal must succeed (raw suffix appends break it): %v\ncontent: %s", err, cr.Content)
	}
	if created.ID == "" {
		t.Fatalf("created task JSON missing ID: %s", cr.Content)
	}
	if !strings.Contains(created.Note, "blocked by uncompleted dependencies") {
		t.Errorf("blocked-state note must live in the JSON note field, got: %s", cr.Content)
	}
}

// Recurring cron_create keeps the bare job JSON shape (no note field).
func TestIssue5748CronCreateRecurringStaysBareJSON(t *testing.T) {
	s := cron.NewScheduler(func(string, bool) {}, "")
	defer s.Shutdown()
	tool := CronCreateTool{Scheduler: s}

	result, err := tool.Execute(context.Background(), json.RawMessage(
		"{\"cron\": \"*/5 * * * *\", \"prompt\": \"loop\", \"recurring\": true}"))
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("recurring create failed: %s", result.Content)
	}
	if v := LeadingJSONViolation(result.Content); v != "" {
		t.Fatalf("recurring create violates contract: %s (content: %s)", v, result.Content)
	}
	if strings.Contains(result.Content, "note") {
		t.Errorf("recurring create should not carry a note field: %s", result.Content)
	}
	var job struct {
		ID        string
		Recurring bool
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(result.Content)), &job); err != nil {
		t.Fatalf("whole-content Unmarshal must succeed: %v (content: %s)", err, result.Content)
	}
	if !job.Recurring {
		t.Errorf("recurring=true must create a recurring job: %s", result.Content)
	}
}
