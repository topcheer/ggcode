package tool

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/topcheer/ggcode/internal/subagent"
)

// WorkflowRunTool implements the workflow_run tool (r436: orchestration
// externalization, after Anthropic dynamic workflows). The agent emits a
// task graph as JSON; an external runtime executes it (parallel workers,
// adversarial verifiers, one synthesizer); the parent context receives ONLY
// the final report. Delegation economics mirror spawn_agent: a workflow
// re-reads context per step and multiplies token cost - use it for jobs too
// big for one context window (repo-wide audits, many-file migrations),
// never for tasks a single pass handles fine.
type WorkflowRunTool struct {
	Manager *subagent.Manager
	// Run executes the graph. Production wiring: agentruntime.WorkflowRunnerFor
	// (cycle-breaker: internal/tool cannot import internal/agentruntime).
	Run func(ctx context.Context, req WorkflowRequest) string
}

// WorkflowRequest is the tool-facing request. WorkflowSpec lives in
// agentruntime; the tool layer only carries the JSON, and the runtime
// re-validates, so this is a transparent envelope.
type WorkflowRequest struct {
	Workflow json.RawMessage `json:"workflow"` // WorkflowSpec JSON: {steps:[{id,task,dependsOn,verifier,tools,model}], synthesis}
	Context  string          `json:"context,omitempty"`
}

func (t WorkflowRunTool) Name() string { return "workflow_run" }

func (t WorkflowRunTool) Description() string {
	return "Execute a generated task graph (dynamic workflow) for jobs too big for one context window: decompose into steps, the runtime runs independent sub-agents per step in parallel layers, adversarially verifies results, and synthesizes ONE final answer - the parent context receives only the final report, not intermediate results. Structurally prevents agentic laziness (graph enforces every step), self-preferential bias (verifier agents try to REFUTE each result), and goal drift (constraints live in the graph). Use for: repo-wide audits/reviews, many-file migrations, stress-testing a plan from multiple angles, large triage. NOT for single bugs, sequentially-dependent work, or token-tight sessions (each step re-reads context; cost multiplies). Steps: 1-16, max 8 per parallel layer, dependency cycles are rejected."
}

func (t WorkflowRunTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
	"type": "object",
	"properties": {
		"workflow": {
			"type": "object",
			"description": "The task graph. Fields: steps (array of {id: unique short id, task: complete self-sufficient task contract for that step's sub-agent, dependsOn: optional array of step ids that must finish first, verifier: optional adversarial instruction - the verifier sub-agent will try to REFUTE this step's result, tools: optional tool whitelist, model: optional model override}), synthesis (instruction for the final synthesizer folding all surviving step outputs into the single answer). Design rules: each task must be fully self-contained (its sub-agent sees nothing else); fan out genuinely independent work; verify steps whose findings will drive action.",
			"required": ["steps", "synthesis"]
		},
		"context": {
			"type": "string",
			"description": "Optional extra context appended to every step task (shared project framing)."
		},
		"description": {
			"type": "string",
			"description": "REQUIRED. Brief activity label shown in the UI. Write in the user's language (e.g. 'Running audit workflow', '执行审计工作流')."
		}
	},
	"required": ["workflow", "description"]
}`)
}

func (t WorkflowRunTool) Execute(ctx context.Context, input json.RawMessage) (Result, error) {
	if t.Run == nil {
		return Result{IsError: true, Content: "workflow_run: no runner wired (registration site bug)"}, nil
	}
	var in struct {
		Workflow json.RawMessage `json:"workflow"`
		Context  string          `json:"context"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid input: %v", err)}, nil
	}
	if len(in.Workflow) == 0 {
		return Result{IsError: true, Content: "workflow_run: 'workflow' (task graph object) is required"}, nil
	}
	out := t.Run(ctx, WorkflowRequest{Workflow: in.Workflow, Context: in.Context})
	return Result{Content: out}, nil
}
