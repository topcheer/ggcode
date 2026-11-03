package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
)

const (
	ResultTypeComplete = "complete"

	ResultTypeInputRequired = "input_required"
)

const maxMRTRRoundTrips = 8

type mrtrEnvelope struct {
	ResultType    string                     `json:"resultType,omitempty"`
	InputRequests map[string]json.RawMessage `json:"inputRequests,omitempty"`
	RequestState  string                     `json:"requestState,omitempty"`
}

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

			return json.Unmarshal(raw, out)
		case ResultTypeInputRequired:
			if round >= maxMRTRRoundTrips {
				return fmt.Errorf("mcp[%s]: %s exceeded %d input_required round trips (MRTR loop guard)", c.name, method, maxMRTRRoundTrips)
			}
			if len(env.InputRequests) == 0 && env.RequestState == "" {

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

			debug.Log("mcp-client", "server=%s MRTR retry #%d for %s (%d input requests, requestState %d bytes)",
				c.name, round+1, method, len(env.InputRequests), len(env.RequestState))

		default:
			return fmt.Errorf("mcp[%s]: %s returned unrecognized resultType %q (treated as invalid protocol response)", c.name, method, env.ResultType)
		}
	}
}

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

		result.Content = nil
	}
	return json.Marshal(result)
}

func (c *Client) resolveMRTSampling(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	handler := c.samplingHandlerLocked()
	if handler == nil {
		return nil, fmt.Errorf("sampling not supported")
	}
	params, err := ParseSamplingParams(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid sampling params: %w", err)
	}

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

func resolveMRTRoots() (json.RawMessage, error) {
	rootURI, err := currentRootURI()
	if err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{
		"roots": []map[string]string{{"uri": rootURI}},
	})
}
