package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/util"
)

const (
	llmOverseerMinIters = 40
	llmOverseerEvery    = 24
	llmOverseerMaxCalls = 2
	llmOverseerWindow   = 24
	llmOverseerTimeout  = 8 * time.Second
)

const (
	verdictOnTrack       = "on_track"
	verdictWrongApproach = "wrong_approach"
	verdictWrongProblem  = "wrong_problem"
)

type llmOverseerState struct {
	callsDone int
	lastIter  int
}

type llmOverseerVerdictJSON struct {
	Verdict    string `json:"verdict"`
	Evidence   string `json:"evidence"`
	Suggestion string `json:"suggestion"`
}

func (a *Agent) snapshotTrajectory(k int) []trajectoryEntry {
	if a.overseer == nil {
		return nil
	}
	a.overseer.mu.Lock()
	defer a.overseer.mu.Unlock()
	n := len(a.overseer.trajectory)
	if n == 0 {
		return nil
	}
	if k > n {
		k = n
	}
	out := make([]trajectoryEntry, k)
	copy(out, a.overseer.trajectory[n-k:])
	return out
}

func (a *Agent) llmOverseerCheck(iteration int) string {
	if a.auxResolved == nil || a.overseer == nil {
		return ""
	}
	if iteration < llmOverseerMinIters {
		return ""
	}
	if iteration-a.llmOverseer.lastIter < llmOverseerEvery {
		return ""
	}
	if a.llmOverseer.callsDone >= llmOverseerMaxCalls {
		return ""
	}
	traj := a.snapshotTrajectory(llmOverseerWindow)
	if len(traj) == 0 {
		return ""
	}

	a.llmOverseer.lastIter = iteration
	a.llmOverseer.callsDone++

	hint := a.llmOverseerCall(traj, iteration)
	if hint == "" {
		debug.Log("llm-overseer", "iteration %d: analysis inconclusive/silent-fail (%d/%d calls used)",
			iteration, a.llmOverseer.callsDone, llmOverseerMaxCalls)
	}
	return hint
}

func llmOverseerTaskHint(a *Agent) string {
	if g := a.getAutopilotGoal(); g != "" {
		return g
	}
	msgs := a.Messages()
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role != "user" {
			continue
		}
		for _, block := range msgs[i].Content {
			if block.Type == "text" {
				return block.Text
			}
		}
	}
	return ""
}

func (a *Agent) llmOverseerCall(traj []trajectoryEntry, iteration int) string {
	var b strings.Builder
	for i, e := range traj {
		mark := "ok"
		if e.isError {
			mark = "ERR"
		}
		line := e.toolName
		if e.fileHint != "" {
			line += " " + e.fileHint
		}
		fmt.Fprintf(&b, "%3d. [%s] %s\n", i+1, mark, line)
	}

	systemPrompt := `You are an execution overseer watching a coding agent's recent tool-call trajectory. ` +
		`Judge ONLY the trajectory shown, not the task statement. ` +
		`Reply with a single JSON object and nothing else: ` +
		`{"verdict":"on_track|wrong_approach|wrong_problem","evidence":"<one sentence citing trajectory indices>","suggestion":"<one concrete corrective instruction>"}. ` +
		`Use wrong_problem only when the work is clearly not the user's actual request. ` +
		`When in doubt, answer on_track.`

	taskHint := llmOverseerTaskHint(a)
	if len(taskHint) > 600 {
		taskHint = taskHint[:util.SnapToRuneStart(taskHint, 600)]
	}

	userPrompt := fmt.Sprintf(
		"## Task (truncated)\n%s\n\n## Last %d tool calls\n%s\nJudge the trajectory.",
		taskHint, len(traj), b.String())

	messages := []provider.Message{
		{Role: "system", Content: []provider.ContentBlock{{Type: "text", Text: systemPrompt}}},
		{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: userPrompt}}},
	}

	ctx, cancel := context.WithTimeout(context.Background(), llmOverseerTimeout)
	defer cancel()

	resp, err := a.auxProviderFor().Chat(ctx, messages, nil)
	if err != nil {
		debug.Log("llm-overseer", "aux chat failed at iteration %d: %v", iteration, err)
		return ""
	}
	if resp.Usage.InputTokens > 0 || resp.Usage.OutputTokens > 0 {
		a.emitUsageWithSource(resp.Usage, "llm-overseer")
	}

	var raw string
	for _, block := range resp.Message.Content {
		if block.Type == "text" {
			raw += block.Text
		}
	}
	v := llmOverseerParseVerdict(raw)
	if v == nil {
		debug.Log("llm-overseer", "malformed verdict at iteration %d (len=%d)", iteration, len(raw))
		return ""
	}

	switch v.Verdict {
	case verdictWrongApproach, verdictWrongProblem:
		hint := fmt.Sprintf("[llm-overseer] %s: %s — %s",
			v.Verdict, v.Evidence, v.Suggestion)
		debug.Log("llm-overseer", "verdict=%s at iteration %d", v.Verdict, iteration)
		return hint
	default:
		return ""
	}
}

func llmOverseerParseVerdict(raw string) *llmOverseerVerdictJSON {
	if raw == "" {
		return nil
	}
	start := strings.Index(raw, "{")
	end := strings.LastIndex(raw, "}")
	if start < 0 || end <= start {
		return nil
	}
	var v llmOverseerVerdictJSON
	if err := json.Unmarshal([]byte(raw[start:end+1]), &v); err != nil {
		return nil
	}
	if v.Verdict == "" {
		return nil
	}
	return &v
}
