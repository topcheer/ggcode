package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
)

// Multi-Request Tool Result (MRTR) client support — "resultType:
// input_required" (MCP protocol revision 2026-07-28 / SEP-2322).
//
// A compliant server may answer a tools/call, prompts/get or resources/read
// request with a result whose resultType is "input_required" instead of
// "complete". The result then carries an inputRequests map keyed by opaque
// request IDs, where each value is a *deferred* request object
// ({"method": ..., "params": ...}) the client must resolve locally — via the
// same elicitation/create, sampling/createMessage or roots/list handlers used
// for interactive server-initiated requests — plus an opaque requestState
// token. The client then retries the SAME logical request with a NEW JSON-RPC
// id, attaching inputResponses and echoing requestState in the params.
//
// Notes:
//   - The 2026-07-28 revision REMOVED notifications/elicitation/complete, so
//     servers that need to correlate a URL-mode elicitation across retries
//     encode their own identifier in requestState. We therefore do not run
//     the pendingURLElicitation tracking for MRTR-resolved requests.
//   - requestState is opaque: we echo it byte-for-byte and never log its
//     contents (only its length).
//   - An unrecognized resultType is treated as an invalid protocol response
//     (spec: clients SHOULD do so), and the retry loop is bounded to prevent
//     a malicious server from keeping a call alive forever.

const (
	// ResultTypeComplete marks a regular, fully-formed result (also the
	// effective value when resultType is absent, for pre-MRTR servers).
	ResultTypeComplete = "complete"
	// ResultTypeInputRequired marks a result asking the client to resolve
	// deferred input requests and retry.
	ResultTypeInputRequired = "input_required"
)

// maxMRTRRoundTrips bounds input_required round trips for a single logical
// request. Each round requires fresh user/LLM interaction, so 8 is generous
// for legitimate multi-step flows while capping abuse.
const maxMRTRRoundTrips = 8

// mrtrEnvelope is the shape shared by every result carrying an MRTR
// resultType. All fields are optional: a "complete" result carries none of
// them, and a spec-legal "input_required" result always has inputRequests,
// requestState, or both (exactly one of the two MAY be absent — see
// mrtrEnvelope below for which).
type mrtrEnvelope struct {
	ResultType    string                     `json:"resultType,omitempty"`
	InputRequests map[string]json.RawMessage `json:"inputRequests,omitempty"`
	RequestState  string                     `json:"requestState,omitempty"`
}

// mrtrRetryParams is implemented by the params types of the request methods
// that support input_required results (tools/call, prompts/get,
// resources/read) so the shared MRTR loop can attach the retry fields.
type mrtrRetryParams interface {
	setMRTRRetry(responses map[string]json.RawMessage, requestState string)
}

func (p *CallToolParams) setMRTRRetry(responses map[string]json.RawMessage, requestState string) {
	p.InputResponses = responses
	p.RequestState = requestState
}

func (p *GetPromptParams) setMRTRRetry(responses map[string]json.RawMessage, requestState string) {
	p.InputResponses = responses
	p.RequestState = requestState
}

func (p *ReadResourceParams) setMRTRRetry(responses map[string]json.RawMessage, requestState string) {
	p.InputResponses = responses
	p.RequestState = requestState
}

// callWithMRTR sends a request via sendRequest and transparently resolves
// input_required results, retrying with resolved input responses until the
// server returns a complete result or the loop guard trips. Each retry uses a
// fresh JSON-RPC id (sendRequest always allocates a new one), as the spec
// requires.
func (c *Client) callWithMRTR(ctx context.Context, method string, params mrtrRetryParams, out interface{}) error {
	return c.mrtrLoop(ctx, method, params,
		func() (json.RawMessage, error) {
			var raw json.RawMessage
			if err := c.sendRequest(ctx, method, params, &raw); err != nil {
				return nil, err
			}
			return raw, nil
		},
		out)
}

// mrtrLoop is the transport-independent MRTR state machine, factored out so
// the retry/protocol-validation logic is unit-testable without a server. It
// only touches c for input-request resolution (handler access).
func (c *Client) mrtrLoop(ctx context.Context, method string, params mrtrRetryParams, send func() (json.RawMessage, error), out interface{}) error {
	for round := 0; ; round++ {
		raw, err := send()
		if err != nil {
			return err
		}
		var env mrtrEnvelope
		if err := json.Unmarshal(raw, &env); err != nil {
			return fmt.Errorf("mcp[%s]: %s returned malformed result: %w", c.name, method, err)
		}
		switch env.ResultType {
		case "", ResultTypeComplete:
			return json.Unmarshal(raw, out)
		case ResultTypeTask:
			// SEP-1686 task descriptor: pass the raw envelope through so the
			// caller (CallToolAsTask) can run the poll/result protocol. Only
			// task-augmented requests can legitimately receive this shape;
			// non-task callers decode it into their result type and surface
			// the mismatch themselves.
			return json.Unmarshal(raw, out)
		case ResultTypeInputRequired:
			if round >= maxMRTRRoundTrips {
				return fmt.Errorf("mcp[%s]: %s exceeded %d input_required round trips (MRTR loop guard)", c.name, method, maxMRTRRoundTrips)
			}
			if len(env.InputRequests) == 0 && env.RequestState == "" {
				// Spec: servers MUST include inputRequests, or requestState
				// when there are no input requests. Neither → protocol
				// violation; retrying blindly could loop forever.
				return fmt.Errorf("mcp[%s]: %s returned input_required with neither inputRequests nor requestState", c.name, method)
			}
			var responses map[string]json.RawMessage
			if len(env.InputRequests) > 0 {
				responses, err = c.resolveInputRequests(ctx, env.InputRequests)
				if err != nil {
					return fmt.Errorf("mcp[%s]: %s MRTR input resolution: %w", c.name, method, err)
				}
			}
			params.setMRTRRetry(responses, env.RequestState)
			// requestState is opaque server data — log only its length.
			debug.Log("mcp-client", "server=%s MRTR retry #%d for %s (%d input requests, requestState %d bytes)",
				c.name, round+1, method, len(env.InputRequests), len(env.RequestState))
			// Loop: the next send() allocates a fresh JSON-RPC id.
		default:
			return fmt.Errorf("mcp[%s]: %s returned unrecognized resultType %q (treated as invalid protocol response)", c.name, method, env.ResultType)
		}
	}
}

// resolveInputRequests resolves every deferred input request of an
// input_required result locally, reusing the exact handlers that serve
// interactive server-initiated requests. Methods outside the spec's allowed
// set (elicitation/create, sampling/createMessage, roots/list) are rejected:
// the spec fixes the InputRequest values, and a client that blindly executed
// arbitrary embedded "requests" would hand the server a request-forgery
// primitive.
func (c *Client) resolveInputRequests(ctx context.Context, inputRequests map[string]json.RawMessage) (map[string]json.RawMessage, error) {
	responses := make(map[string]json.RawMessage, len(inputRequests))
	for key, raw := range inputRequests {
		var probe struct {
			Method string `json:"method"`
		}
		if err := json.Unmarshal(raw, &probe); err != nil {
			return nil, fmt.Errorf("input request %q: not an object with a method: %w", key, err)
		}
		var (
			resp json.RawMessage
			err  error
		)
		switch probe.Method {
		case "elicitation/create":
			resp, err = c.resolveMRTElicitation(ctx, raw)
		case "sampling/createMessage":
			resp, err = c.resolveMRTSampling(ctx, raw)
		case "roots/list":
			resp, err = resolveMRTRoots()
		default:
			return nil, fmt.Errorf("input request %q uses unsupported method %q (allowed: elicitation/create, sampling/createMessage, roots/list)", key, probe.Method)
		}
		if err != nil {
			return nil, fmt.Errorf("input request %q: %w", key, err)
		}
		responses[key] = resp
	}
	return responses, nil
}

// resolveMRTElicitation answers a deferred elicitation/create input request
// with the registered elicitation handler. Mirrors handleElicitation's
// validation and URL-mode content stripping, minus the JSON-RPC plumbing and
// pendingURLElicitation tracking (which the 2026-07-28 revision removed
// entirely; servers correlate retries via requestState).
func (c *Client) resolveMRTElicitation(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	handler := c.elicitationHandlerLocked()
	if handler == nil {
		return nil, fmt.Errorf("elicitation not supported")
	}
	params, err := ParseElicitationParams(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid elicitation params: %w", err)
	}
	if err := validateElicitationParams(params); err != nil {
		return nil, fmt.Errorf("invalid elicitation params: %w", err)
	}
	hctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	result, err := handler(hctx, params)
	if err != nil {
		return nil, fmt.Errorf("elicitation failed: %w", err)
	}
	if params.EffectiveMode() == ElicitationModeURL && result != nil {
		// URL-mode: the interaction happens out-of-band; no data passes
		// through the client response.
		result.Content = nil
	}
	return json.Marshal(result)
}

// resolveMRTSampling answers a deferred sampling/createMessage input request
// with the registered sampling handler. Mirrors handleSampling.
func (c *Client) resolveMRTSampling(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	handler := c.samplingHandlerLocked()
	if handler == nil {
		return nil, fmt.Errorf("sampling not supported")
	}
	params, err := ParseSamplingParams(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid sampling params: %w", err)
	}
	// #2514: mirror the interactive handleSampling validation exactly.
	// ParseSamplingParams covers the parse level; ValidateSamplingParams adds
	// the SEP-1577 structural checks (tool result balance, mixed-content
	// tool_result turns) so a malformed deferred sampling request fails with
	// a self-correctable error instead of reaching the LLM handler.
	if err := ValidateSamplingParams(params); err != nil {
		return nil, fmt.Errorf("invalid sampling params: %w", err)
	}
	hctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	result, err := handler(hctx, params)
	if err != nil {
		return nil, fmt.Errorf("sampling failed: %w", err)
	}
	return json.Marshal(result)
}

// resolveMRTRoots answers a deferred roots/list input request with the
// current workspace root, matching the interactive roots/list response shape.
func resolveMRTRoots() (json.RawMessage, error) {
	rootURI, err := currentRootURI()
	if err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{
		"roots": []map[string]string{{"uri": rootURI}},
	})
}
