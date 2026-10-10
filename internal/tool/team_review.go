package tool

// Teammate batch review digest (r14, scaled oversight): one supervisor
// reviewing N parallel teammate deliveries in a single surface.
//
// Gap context (verdict research-r14-team-review-digest-go): the machine
// side already aggregates results (teammate_results) and waits in
// parallel (wait_agent MAX=8), but there is no human-side batch verdict
// surface - "review N teammate outputs and approve / request-changes /
// reject each" did not exist (grep batch_approv/approve_all/
// review_digest found only i18n copy). This is the core lever of the
// scaled-oversight trend (Anthropic 2026 trends report #3;
// arXiv:2508.11126): one supervisor, N agents.
//
// team_review_digest renders the pending-review digest; team_review_decide
// applies one verdict per teammate: approve completes the teammate's
// board task (sa90 attribution chain untouched), changes/reject bounces
// the note back to the teammate and resets the task to pending.

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/topcheer/ggcode/internal/swarm"
	"github.com/topcheer/ggcode/internal/task"
)

// reviewConfidenceWords marks results whose tail claims verification.
var reviewConfidenceWords = []string{"verified", "passed", "pass", "green", "all tests", "ok\t"}

// teamReviewItem is one digest entry, decoupled from swarm types so the
// rendering is unit-testable without a live manager.
type teamReviewItem struct {
	TeammateID   string
	TeammateName string
	TaskSubject  string
	Summary      string // first lines of the latest result
	HasResult    bool
	Confident    bool // tail of result claims verification
}

// renderTeamReviewDigest renders the batch-review digest. Pure function.
func renderTeamReviewDigest(items []teamReviewItem) string {
	if len(items) == 0 {
		return "No teammate output pending review.\n"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Batch review digest: %d teammate(s) pending verdict\n\n", len(items))
	for _, it := range items {
		name := it.TeammateName
		if name == "" {
			name = it.TeammateID
		}
		conf := "unverified"
		if it.Confident {
			conf = "VERIFIED"
		}
		fmt.Fprintf(&b, "─── %s (%s) [%s] ───\n", name, it.TeammateID, conf)
		if it.TaskSubject != "" {
			fmt.Fprintf(&b, "task: %s\n", it.TaskSubject)
		}
		if it.HasResult {
			fmt.Fprintf(&b, "%s\n", it.Summary)
		} else {
			b.WriteString("(no result yet)\n")
		}
		b.WriteString("\n")
	}
	b.WriteString("Decide each item with team_review_decide{team_id, teammate_id, verdict: approve|changes| reject, note}.\n")
	return b.String()
}

// summarizeResult takes the first lines of a result for the digest.
func summarizeResult(result string, maxLines, maxChars int) string {
	if maxLines <= 0 {
		maxLines = 3
	}
	lines := strings.Split(strings.TrimRight(result, "\n"), "\n")
	if len(lines) > maxLines {
		lines = lines[:maxLines]
	}
	s := strings.Join(lines, "\n")
	if len(s) > maxChars {
		s = s[:maxChars] + "..."
	}
	return s
}

// resultClaimsVerification checks the tail of a result for confidence words.
func resultClaimsVerification(result string) bool {
	tail := result
	if len(tail) > 400 {
		tail = tail[len(tail)-400:]
	}
	low := strings.ToLower(tail)
	for _, w := range reviewConfidenceWords {
		if strings.Contains(low, w) {
			return true
		}
	}
	return false
}

// latestTeammateTask finds the most recently updated board task owned by
// the teammate (any status) - the natural target of a review verdict.
func latestTeammateTask(tm *task.Manager, teammateID string) (task.Task, bool) {
	if tm == nil {
		return task.Task{}, false
	}
	tasks := tm.List()
	var best task.Task
	found := false
	for _, t := range tasks {
		if t.Owner != teammateID {
			continue
		}
		if !found || t.UpdatedAt.After(best.UpdatedAt) {
			best = t
			found = true
		}
	}
	return best, found
}

// ————————————————————————————————————————
// TeamReviewDigestTool
// ————————————————————————————————————————

type TeamReviewDigestTool struct {
	Manager *swarm.Manager
}

func (t TeamReviewDigestTool) Name() string { return "team_review_digest" }
func (t TeamReviewDigestTool) Description() string {
	return "Render a batch review digest of all teammate deliveries in a team: per teammate - name, board task subject, first lines of the latest output, and a VERIFIED/unverified confidence flag (tail-of-result word check). " +
		"Use when N teammates have delivered work and you (or the user) need to review all of it in one pass, then decide each item with team_review_decide. Scaled oversight for parallel teammate work."
}
func (t TeamReviewDigestTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"team_id": {"type": "string", "description": "Team ID"},
			"description": {
				"type": "string",
				"description": "REQUIRED. Brief activity label shown in the UI. Write in the user's language. You MUST always provide this field."
			}
		},
		"required": ["team_id", "description"]
	}`)
}
func (t TeamReviewDigestTool) Execute(_ context.Context, input json.RawMessage) (Result, error) {
	if t.Manager == nil {
		return Result{IsError: true, Content: "team_review_digest: swarm manager not available"}, nil
	}
	var args struct {
		TeamID string `json:"team_id"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid input: %v", err)}, nil
	}
	team, ok := t.Manager.GetTeam(args.TeamID)
	if !ok {
		return Result{IsError: true, Content: fmt.Sprintf("team %q not found", args.TeamID)}, nil
	}
	results := t.Manager.GetTeamResults(args.TeamID)
	tm := t.Manager.GetTaskManager(args.TeamID)

	items := make([]teamReviewItem, 0, len(team.Teammates))
	for _, ts := range team.Teammates {
		item := teamReviewItem{TeammateID: ts.ID, TeammateName: ts.Name, HasResult: false}
		if res, ok := results[ts.ID]; ok && res != "" {
			item.HasResult = true
			item.Summary = summarizeResult(res, 3, 600)
			item.Confident = resultClaimsVerification(res)
		}
		if t, found := latestTeammateTask(tm, ts.ID); found {
			item.TaskSubject = t.Subject
		}
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].TeammateID < items[j].TeammateID })
	return Result{Content: renderTeamReviewDigest(items)}, nil
}

// ————————————————————————————————————————
// TeamReviewDecideTool
// ————————————————————————————————————————

type TeamReviewDecideTool struct {
	Manager *swarm.Manager
}

func (t TeamReviewDecideTool) Name() string { return "team_review_decide" }
func (t TeamReviewDecideTool) Description() string {
	return "Apply a review verdict to one teammate's latest delivery: approve|changes|reject (optionally with a note). " +
		"approve completes the teammate's latest board task (normal attribution chain). changes/reject sends the note back to the teammate and resets the task to pending for rework. " +
		"Use after team_review_digest to decide items one by one."
}
func (t TeamReviewDecideTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"team_id": {"type": "string", "description": "Team ID"},
			"teammate_id": {"type": "string", "description": "Teammate the verdict applies to"},
			"verdict": {"type": "string", "enum": ["approve", "changes", "reject"], "description": "approve = accept and complete the board task; changes = request rework (note sent back, task reset to pending); reject = refuse delivery (same routing as changes)."},
			"note": {"type": "string", "description": "Optional verdict rationale; required in practice for changes/reject so the teammate knows what to fix."},
			"description": {
				"type": "string",
				"description": "REQUIRED. Brief activity label shown in the UI. Write in the user's language. You MUST always provide this field."
			}
		},
		"required": ["team_id", "teammate_id", "verdict", "description"]
	}`)
}
func (t TeamReviewDecideTool) Execute(_ context.Context, input json.RawMessage) (Result, error) {
	if t.Manager == nil {
		return Result{IsError: true, Content: "team_review_decide: swarm manager not available"}, nil
	}
	var args struct {
		TeamID     string `json:"team_id"`
		TeammateID string `json:"teammate_id"`
		Verdict    string `json:"verdict"`
		Note       string `json:"note"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid input: %v", err)}, nil
	}
	if args.Verdict != "approve" && args.Verdict != "changes" && args.Verdict != "reject" {
		return Result{IsError: true, Content: fmt.Sprintf("invalid verdict %q (want approve|changes|reject)", args.Verdict)}, nil
	}
	team, ok := t.Manager.GetTeam(args.TeamID)
	if !ok {
		return Result{IsError: true, Content: fmt.Sprintf("team %q not found", args.TeamID)}, nil
	}
	known := false
	for _, ts := range team.Teammates {
		if ts.ID == args.TeammateID {
			known = true
			break
		}
	}
	if !known {
		return Result{IsError: true, Content: fmt.Sprintf("teammate %q not found in team %q", args.TeammateID, args.TeamID)}, nil
	}

	tm := t.Manager.GetTaskManager(args.TeamID)
	boardTask, found := latestTeammateTask(tm, args.TeammateID)

	switch args.Verdict {
	case "approve":
		if !found {
			return Result{IsError: true, Content: fmt.Sprintf("no board task owned by %q - nothing to complete; if work was ad-hoc (send_message), just acknowledge directly", args.TeammateID)}, nil
		}
		completed := task.TaskStatus("completed")
		if _, err := tm.Update(boardTask.ID, task.UpdateOptions{Status: &completed}); err != nil {
			return Result{IsError: true, Content: fmt.Sprintf("complete task %s: %v", boardTask.ID, err)}, nil
		}
		return Result{Content: fmt.Sprintf("APPROVED %s: board task %q completed.\n", args.TeammateID, boardTask.Subject)}, nil
	default: // changes | reject
		if args.Note == "" {
			return Result{IsError: true, Content: fmt.Sprintf("%s needs a note so the teammate knows what to fix", args.Verdict)}, nil
		}
		msg := fmt.Sprintf("[review %s] %s", args.Verdict, args.Note)
		if err := t.Manager.SendToTeammate(args.TeamID, args.TeammateID, swarm.MailMessage{
			From: "reviewer", Content: msg, Summary: fmt.Sprintf("review %s: %s", args.Verdict, firstLine(args.Note)), Type: "message",
		}); err != nil {
			return Result{IsError: true, Content: fmt.Sprintf("bounce note to %s: %v", args.TeammateID, err)}, nil
		}
		if found && boardTask.Status == "completed" {
			pending := task.TaskStatus("pending")
			if _, err := tm.Update(boardTask.ID, task.UpdateOptions{Status: &pending}); err != nil {
				return Result{IsError: true, Content: fmt.Sprintf("reset task %s to pending: %v", boardTask.ID, err)}, nil
			}
		}
		return Result{Content: fmt.Sprintf("%s %s: note sent back for rework.\n", strings.ToUpper(args.Verdict[:1])+args.Verdict[1:], args.TeammateID)}, nil
	}
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	if len(s) > 120 {
		return s[:120]
	}
	return s
}
