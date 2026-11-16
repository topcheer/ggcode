package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
)

// #5699: MCP deferred answer queue for input_required tasks.
//
// Esc semantics: when an MRTR input_required round routes through an
// interactive handler (elicitation/sampling) and the user presses Esc, the
// handler returns ErrDeferInputRequest. The MRTR loop then snapshots the
// round (requestState + inputRequests + the original call params) into a
// client-side pending-answer record that reuses the tasks.go envelope shape
// (Task embedded: status=input_required, TTL, createdAt/lastUpdatedAt) and
// unwinds the tool call with *PendingAnswerError instead of failing it.
//
// Lifecycle semantics (restart/compaction):
//   - The queue is per-process, in-memory, and intentionally decoupled from
//     conversation compaction (C1 idle-report): compaction never touches it.
//   - On process restart the queue clears. Server-side resources are governed
//     by the server's own task TTL; answering a lost ID returns a
//     not-found/expired error advising a re-issue of the tool call.
//   - Entry TTL (DefaultPendingAnswerTTL) and queue depth (MaxPendingAnswers)
//     bound accumulation: expired entries are swept on every access, and a
//     full queue refuses new deferrals loudly instead of evicting live work.

const (
	// DefaultPendingAnswerTTL bounds how long a deferred input_required round
	// stays answerable (borrowed from the tasks.go TTL pattern; expiry
	// wording mirrors zz_tasks69 classification).
	DefaultPendingAnswerTTL = 30 * time.Minute

	// MaxPendingAnswers bounds the per-client pending-answer queue depth so a
	// user mashing Esc can never accumulate unbounded state.
	MaxPendingAnswers = 32
)

// ErrDeferInputRequest is the sentinel an elicitation/sampling handler
// returns when the user deferred (Esc) instead of answering. Only meaningful
// while the calling context is still alive; handlers that see a cancelled
// context must keep returning their ordinary cancel path.
var ErrDeferInputRequest = errors.New("input request deferred by user")

// PendingAnswer is a deferred MRTR input_required round, persisted in the
// task envelope shape (Task embed) so /tasks-equivalent surfaces can list
// and answer it later.
type PendingAnswer struct {
	Task

	ID            string                     `json:"pendingId"`
	Server        string                     `json:"server"`
	Method        string                     `json:"method"`
	ToolName      string                     `json:"toolName,omitempty"`
	ResumeParams  json.RawMessage            `json:"resumeParams,omitempty"`
	InputRequests map[string]json.RawMessage `json:"inputRequests,omitempty"`
	RequestState  string                     `json:"requestState,omitempty"`
	ExpiresAt     time.Time                  `json:"expiresAt"`
}

func formatDurationForNotice(d time.Duration) string {
	if d <= 0 {
		return "0s"
	}
	mins := int(d.Minutes())
	if mins >= 1 {
		return fmt.Sprintf("%dm", mins)
	}
	return fmt.Sprintf("%ds", int(d.Seconds()))
}

// ExpiresIn renders the remaining answerable window for UIs.
func (p PendingAnswer) ExpiresIn(now time.Time) time.Duration {
	if now.After(p.ExpiresAt) {
		return 0
	}
	return p.ExpiresAt.Sub(now)
}

// PendingAnswerError is returned from a tool call that was deferred (Esc on
// input_required): the call is suspended with a queued pending answer, not
// failed.
type PendingAnswerError struct {
	PA PendingAnswer
}

func (e *PendingAnswerError) Error() string {
	tool := e.PA.ToolName
	if tool == "" {
		tool = e.PA.Method
	}
	return fmt.Sprintf("mcp[%s]: %s input_required deferred by user (pending answer %s queued; expires %s)",
		e.PA.Server, tool, e.PA.ID, e.PA.ExpiresAt.UTC().Format(time.RFC3339))
}

// ResumedAnswer is the outcome of answering a pending answer.
type ResumedAnswer struct {
	// Requeued is non-nil when the server answered with another
	// input_required round; that round is queued as a fresh pending answer.
	Requeued *PendingAnswer
	// Result is non-nil for completed tools/call resumes.
	Result *CallToolResult
	// Raw is the completed result for non tools/call methods.
	Raw json.RawMessage
}

func (c *Client) pendingTTL() time.Duration {
	if c.pendingTTLOverride > 0 {
		return c.pendingTTLOverride
	}
	return DefaultPendingAnswerTTL
}

// sweepPendingLocked drops expired entries; caller holds pendingMu.
func (c *Client) sweepPendingLocked(now time.Time) {
	if len(c.pendingByID) == 0 {
		return
	}
	kept := c.pendingOrder[:0]
	for _, id := range c.pendingOrder {
		if pa, ok := c.pendingByID[id]; ok && now.After(pa.ExpiresAt) {
			delete(c.pendingByID, id)
			debug.Log("mcp-pending", "server=%s pending=%s expired after TTL (swept)", c.name, id)
			continue
		}
		kept = append(kept, id)
	}
	c.pendingOrder = kept
}

// queuePendingAnswer records a deferred input_required round. Returns an
// error when the queue is full (after sweeping expired entries) so callers
// can fail loudly instead of silently dropping live work.
func (c *Client) queuePendingAnswer(method string, params mrtrRetryParams, env mrtrEnvelope) (*PendingAnswer, error) {
	snapshot, err := json.Marshal(params)
	if err != nil {
		return nil, fmt.Errorf("marshaling resume params: %w", err)
	}
	now := time.Now()
	ttl := c.pendingTTL()

	c.pendingMu.Lock()
	c.sweepPendingLocked(now)
	if len(c.pendingByID) >= MaxPendingAnswers {
		c.pendingMu.Unlock()
		return nil, fmt.Errorf("pending-answer queue full (%d live entries; oldest still answerable); answer or discard existing entries before deferring more", len(c.pendingByID))
	}
	c.pendingSeq++
	id := fmt.Sprintf("pa-%d", c.pendingSeq)
	toolName := ""
	if cp, ok := params.(*CallToolParams); ok {
		toolName = cp.Name
	}
	pa := &PendingAnswer{
		Task: Task{
			TaskID:        id,
			Status:        TaskStatusInputRequired,
			StatusMessage: pendingStatusMessage(env.InputRequests),
			TTL:           ttl.Milliseconds(),
			CreatedAt:     now.UTC().Format(time.RFC3339),
			LastUpdatedAt: now.UTC().Format(time.RFC3339),
		},
		ID:            id,
		Server:        c.name,
		Method:        method,
		ToolName:      toolName,
		ResumeParams:  snapshot,
		InputRequests: env.InputRequests,
		RequestState:  env.RequestState,
		ExpiresAt:     now.Add(ttl),
	}
	if c.pendingByID == nil {
		c.pendingByID = make(map[string]*PendingAnswer)
	}
	c.pendingByID[id] = pa
	c.pendingOrder = append(c.pendingOrder, id)
	c.pendingMu.Unlock()

	debug.Log("mcp-pending", "server=%s pending=%s queued for %s %s (requestState %d bytes, %d input requests, ttl %s)",
		c.name, id, method, toolName, len(env.RequestState), len(env.InputRequests), ttl)
	return pa, nil
}

// pendingStatusMessage extracts a human-readable summary from the round's
// input requests (elicitation message when available).
func pendingStatusMessage(requests map[string]json.RawMessage) string {
	keys := make([]string, 0, len(requests))
	for k := range requests {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		var probe struct {
			Method  string `json:"method"`
			Message string `json:"message"`
		}
		if err := json.Unmarshal(requests[k], &probe); err != nil {
			continue
		}
		if probe.Message != "" {
			return probe.Message
		}
	}
	if len(keys) > 0 {
		return fmt.Sprintf("%d input request(s) await answers", len(keys))
	}
	return "awaiting user answer"
}

// ListPendingAnswers returns the live pending answers (expired swept), in
// queue order.
func (c *Client) ListPendingAnswers() []PendingAnswer {
	now := time.Now()
	c.pendingMu.Lock()
	c.sweepPendingLocked(now)
	out := make([]PendingAnswer, 0, len(c.pendingByID))
	for _, id := range c.pendingOrder {
		if pa, ok := c.pendingByID[id]; ok {
			out = append(out, *pa)
		}
	}
	c.pendingMu.Unlock()
	return out
}

// PendingAnswerCount reports the number of live pending answers.
func (c *Client) PendingAnswerCount() int {
	now := time.Now()
	c.pendingMu.Lock()
	c.sweepPendingLocked(now)
	n := len(c.pendingByID)
	c.pendingMu.Unlock()
	return n
}

// takePending removes and returns a live pending answer by ID.
func (c *Client) takePending(id string) (*PendingAnswer, error) {
	now := time.Now()
	c.pendingMu.Lock()
	c.sweepPendingLocked(now)
	pa, ok := c.pendingByID[id]
	if !ok {
		c.pendingMu.Unlock()
		return nil, fmt.Errorf("mcp[%s]: no pending answer %q (it expired, was discarded, or the process restarted); re-issue the tool call if the work is still needed", c.name, id)
	}
	delete(c.pendingByID, id)
	for i, pid := range c.pendingOrder {
		if pid == id {
			c.pendingOrder = append(c.pendingOrder[:i], c.pendingOrder[i+1:]...)
			break
		}
	}
	c.pendingMu.Unlock()
	return pa, nil
}

// DiscardPendingInput drops a pending answer without answering.
func (c *Client) DiscardPendingInput(id string) bool {
	c.pendingMu.Lock()
	_, ok := c.pendingByID[id]
	if ok {
		delete(c.pendingByID, id)
		for i, pid := range c.pendingOrder {
			if pid == id {
				c.pendingOrder = append(c.pendingOrder[:i], c.pendingOrder[i+1:]...)
				break
			}
		}
	}
	c.pendingMu.Unlock()
	if ok {
		debug.Log("mcp-pending", "server=%s pending=%s discarded by user", c.name, id)
	}
	return ok
}

// AnswerPendingInput answers a queued pending answer and resumes the call
// with the carried requestState. It never invokes interactive handlers: if
// the server asks again, the new round is re-queued as a fresh pending
// answer instead of blocking.
func (c *Client) AnswerPendingInput(ctx context.Context, id string, responses map[string]json.RawMessage) (*ResumedAnswer, error) {
	if c.closed.Load() {
		return nil, fmt.Errorf("mcp[%s]: connection closed", c.name)
	}
	rec, err := c.takePending(id)
	if err != nil {
		return nil, err
	}

	var params mrtrRetryParams
	switch rec.Method {
	case "tools/call":
		params = &CallToolParams{}
	case "prompts/get":
		params = &GetPromptParams{}
	case "resources/read":
		params = &ReadResourceParams{}
	default:
		return nil, fmt.Errorf("mcp[%s]: pending answer %s carries unsupported resume method %q", c.name, id, rec.Method)
	}
	if len(rec.ResumeParams) > 0 {
		if err := json.Unmarshal(rec.ResumeParams, params); err != nil {
			return nil, fmt.Errorf("mcp[%s]: pending answer %s resume params malformed: %w", c.name, id, err)
		}
	}
	params.setMRTRRetry(responses, rec.RequestState)

	var raw json.RawMessage
	if err := c.sendRequest(ctx, rec.Method, params, &raw); err != nil {
		return nil, fmt.Errorf("mcp[%s]: resuming %s for %s: %w", c.name, rec.Method, id, err)
	}
	var env mrtrEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("mcp[%s]: %s returned malformed result while resuming %s: %w", c.name, rec.Method, id, err)
	}
	switch env.ResultType {
	case "", ResultTypeComplete:
		if rec.Method == "tools/call" {
			var out CallToolResult
			if err := json.Unmarshal(raw, &out); err != nil {
				return nil, fmt.Errorf("mcp[%s]: tools/call result malformed while resuming %s: %w", c.name, id, err)
			}
			return &ResumedAnswer{Result: &out}, nil
		}
		return &ResumedAnswer{Raw: raw}, nil
	case ResultTypeInputRequired:
		if len(env.InputRequests) == 0 && env.RequestState == "" {
			return nil, fmt.Errorf("mcp[%s]: %s returned input_required with neither inputRequests nor requestState while resuming %s", c.name, rec.Method, id)
		}
		np, qerr := c.queuePendingAnswer(rec.Method, params, env)
		if qerr != nil {
			return nil, fmt.Errorf("mcp[%s]: re-queueing follow-up round for %s refused: %w", c.name, id, qerr)
		}
		return &ResumedAnswer{Requeued: np}, nil
	case ResultTypeTask:
		return c.resumeTaskEnvelope(ctx, raw, id)
	default:
		return nil, fmt.Errorf("mcp[%s]: %s returned unrecognized resultType %q while resuming %s", c.name, rec.Method, env.ResultType, id)
	}
}

// resumeTaskEnvelope follows a task envelope produced by the resumed call,
// mirroring the CallToolAsTask completion path.
func (c *Client) resumeTaskEnvelope(ctx context.Context, raw json.RawMessage, pendingID string) (*ResumedAnswer, error) {
	var created CreateTaskResult
	if err := json.Unmarshal(raw, &created); err != nil {
		return nil, fmt.Errorf("mcp[%s]: task envelope malformed while resuming %s: %w", c.name, pendingID, err)
	}
	if created.TaskID == "" {
		return nil, fmt.Errorf("mcp[%s]: task envelope without taskId while resuming %s", c.name, pendingID)
	}
	task, err := c.awaitTaskLoop(ctx, created.Task, created.TaskID, c.GetTask)
	if err != nil {
		return nil, err
	}
	switch task.Status {
	case TaskStatusCompleted:
		result, err := c.TaskResult(ctx, task.TaskID)
		if err != nil {
			return nil, err
		}
		return &ResumedAnswer{Result: result}, nil
	case TaskStatusFailed:
		return nil, fmt.Errorf("mcp[%s]: task %s failed after resume of %s: %s", c.name, task.TaskID, pendingID, task.StatusMessage)
	case TaskStatusCancelled:
		return nil, fmt.Errorf("mcp[%s]: task %s was cancelled after resume of %s", c.name, task.TaskID, pendingID)
	default:
		return nil, fmt.Errorf("mcp[%s]: task %s ended in unexpected status %q after resume of %s", c.name, task.TaskID, task.Status, pendingID)
	}
}

// SummarizeCallToolResult renders a compact digest of a resumed tool result
// for model-facing notices and /mcp answer output.
func SummarizeCallToolResult(r *CallToolResult, limit int) string {
	if r == nil {
		return ""
	}
	var parts []string
	for _, c := range r.Content {
		if c.Type == "text" {
			parts = append(parts, c.Text)
			continue
		}
		parts = append(parts, fmt.Sprintf("[%s content omitted]", c.Type))
	}
	if len(parts) == 0 && len(r.StructuredContent) > 0 {
		parts = append(parts, string(r.StructuredContent))
	}
	s := strings.Join(parts, "\n")
	if r.IsError {
		s = "tool error: " + s
	}
	if limit > 0 && len(s) > limit {
		s = s[:limit] + "…"
	}
	return s
}

// BuildPendingAnswerResponses maps a user's freeform answer text onto the
// deferred round's input requests. rawJSON switches the text to a JSON
// object mapping request key -> response object (escape hatch for
// multi-request rounds and non-elicitation shapes).
func BuildPendingAnswerResponses(inputRequests map[string]json.RawMessage, text string, rawJSON bool) (map[string]json.RawMessage, error) {
	if len(inputRequests) == 0 {
		return nil, fmt.Errorf("pending round has no input requests to answer")
	}
	if rawJSON {
		var m map[string]json.RawMessage
		if err := json.Unmarshal([]byte(text), &m); err != nil {
			return nil, fmt.Errorf("--json payload must be an object mapping request key to response: %w", err)
		}
		for k := range m {
			if _, ok := inputRequests[k]; !ok {
				return nil, fmt.Errorf("--json payload carries key %q which is not one of the pending requests (%s)", k, strings.Join(sortedRequestKeys(inputRequests), ", "))
			}
		}
		return m, nil
	}
	if len(inputRequests) > 1 {
		return nil, fmt.Errorf("round carries %d input requests; answering requires --json '%s'", len(inputRequests), buildResponsesJSONHint(inputRequests))
	}
	key := sortedRequestKeys(inputRequests)[0]
	var probe struct {
		Method string `json:"method"`
	}
	if err := json.Unmarshal(inputRequests[key], &probe); err != nil {
		return nil, fmt.Errorf("input request %q: not an object with a method: %w", key, err)
	}
	switch probe.Method {
	case "elicitation/create":
		var params ElicitationParams
		if err := json.Unmarshal(inputRequests[key], &params); err != nil {
			return nil, fmt.Errorf("input request %q: invalid elicitation params: %w", key, err)
		}
		content, err := elicitationContentFromText(params, text)
		if err != nil {
			return nil, err
		}
		resp := ElicitationResult{Action: ElicitationActionAccept}
		if content != nil {
			resp.Content = content
		}
		raw, err := json.Marshal(resp)
		if err != nil {
			return nil, err
		}
		return map[string]json.RawMessage{key: raw}, nil
	case "sampling/createMessage":
		raw, err := json.Marshal(SamplingResult{
			Model:      "user-answer",
			StopReason: "end_turn",
			Role:       "assistant",
			Content:    SamplingContent{Type: "text", Text: text},
		})
		if err != nil {
			return nil, err
		}
		return map[string]json.RawMessage{key: raw}, nil
	default:
		return nil, fmt.Errorf("input request %q uses method %q which cannot be answered from freeform text; use --json", key, probe.Method)
	}
}

func sortedRequestKeys(requests map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(requests))
	for k := range requests {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func buildResponsesJSONHint(requests map[string]json.RawMessage) string {
	parts := make([]string, 0, len(requests))
	for _, k := range sortedRequestKeys(requests) {
		parts = append(parts, fmt.Sprintf(`"%s":{"action":"accept","content":{}}`, k))
	}
	return "{" + strings.Join(parts, ",") + "}"
}

// elicitationContentFromText maps freeform text (or key=value pairs) onto
// the elicitation schema.
func elicitationContentFromText(params ElicitationParams, text string) (map[string]any, error) {
	names := sortedSchemaProperties(params.Schema)
	switch len(names) {
	case 0:
		return nil, nil
	case 1:
		v, err := coerceElicitationValue(params.Schema.Properties[names[0]], text)
		if err != nil {
			return nil, fmt.Errorf("field %q: %w", names[0], err)
		}
		return map[string]any{names[0]: v}, nil
	default:
		fields, err := parseKeyValuePairs(text)
		if err != nil {
			return nil, fmt.Errorf("multiple fields require key=value pairs (or --json): %w", err)
		}
		content := make(map[string]any, len(names))
		for _, name := range names {
			raw, ok := fields[name]
			if !ok {
				return nil, fmt.Errorf("field %q missing from answer; provide: %s", name, strings.Join(names, ", "))
			}
			v, err := coerceElicitationValue(params.Schema.Properties[name], raw)
			if err != nil {
				return nil, fmt.Errorf("field %q: %w", name, err)
			}
			content[name] = v
		}
		return content, nil
	}
}

func sortedSchemaProperties(schema ElicitationSchema) []string {
	names := make([]string, 0, len(schema.Properties))
	for k := range schema.Properties {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}

func parseKeyValuePairs(text string) (map[string]string, error) {
	out := map[string]string{}
	for _, tok := range strings.Fields(text) {
		i := strings.Index(tok, "=")
		if i <= 0 {
			return nil, fmt.Errorf("token %q is not key=value", tok)
		}
		out[tok[:i]] = tok[i+1:]
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no key=value pairs found")
	}
	return out, nil
}

func coerceElicitationValue(field ElicitationFieldSchema, text string) (any, error) {
	if len(field.Enum) > 0 {
		for _, opt := range field.Enum {
			if strings.EqualFold(FormatEnumValue(opt), strings.TrimSpace(text)) {
				return opt, nil
			}
		}
		labels := make([]string, 0, len(field.Enum))
		for _, opt := range field.Enum {
			labels = append(labels, FormatEnumValue(opt))
		}
		return nil, fmt.Errorf("%q is not one of the allowed options: %s", text, strings.Join(labels, ", "))
	}
	switch field.Type {
	case "boolean":
		switch strings.ToLower(strings.TrimSpace(text)) {
		case "true", "yes":
			return true, nil
		case "false", "no":
			return false, nil
		}
		return nil, fmt.Errorf("%q is not a boolean (true/false)", text)
	case "integer":
		v, err := strconv.Atoi(strings.TrimSpace(text))
		if err != nil {
			return nil, fmt.Errorf("%q is not an integer", text)
		}
		return v, nil
	case "number":
		v, err := strconv.ParseFloat(strings.TrimSpace(text), 64)
		if err != nil {
			return nil, fmt.Errorf("%q is not a number", text)
		}
		return v, nil
	default:
		return text, nil
	}
}
