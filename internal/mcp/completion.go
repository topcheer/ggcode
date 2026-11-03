package mcp

import (
	"context"
	"fmt"
)

type CompleteRequest struct {
	Ref CompleteReference

	Argument string

	Value string

	ContextArgs map[string]string
}

type CompleteReference struct {
	Name string `json:"name,omitempty"`

	URI string `json:"uri,omitempty"`
}

func (r CompleteReference) referenceType() string {
	if r.URI != "" {
		return "ref/resource"
	}
	return "ref/prompt"
}

type completeRef struct {
	Type string `json:"type"`
	Name string `json:"name,omitempty"`
	URI  string `json:"uri,omitempty"`
}

type CompleteArgument struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type CompleteContext struct {
	Arguments map[string]string `json:"arguments,omitempty"`
}

type CompleteParams struct {
	Ref      completeRef      `json:"ref"`
	Argument CompleteArgument `json:"argument"`
	Context  *CompleteContext `json:"context,omitempty"`
}

type Completion struct {
	Values []string `json:"values"`

	Total *int `json:"total,omitempty"`

	HasMore bool `json:"hasMore,omitempty"`
}

type CompleteResult struct {
	Completion Completion `json:"completion"`
}

func (c *Client) Complete(ctx context.Context, req CompleteRequest) (*CompleteResult, error) {
	if c.closed.Load() {
		return nil, fmt.Errorf("mcp[%s]: connection closed", c.name)
	}
	if !c.HasCompletion() {
		return nil, fmt.Errorf("mcp[%s]: server does not advertise completions", c.name)
	}
	if req.Ref.Name == "" && req.Ref.URI == "" {
		return nil, fmt.Errorf("mcp[%s]: completion/complete: reference requires a prompt name or resource URI", c.name)
	}
	if req.Ref.Name != "" && req.Ref.URI != "" {
		return nil, fmt.Errorf("mcp[%s]: completion/complete: reference must be either a prompt name or a resource URI, not both", c.name)
	}
	params := CompleteParams{
		Ref: completeRef{
			Type: req.Ref.referenceType(),
			Name: req.Ref.Name,
			URI:  req.Ref.URI,
		},
		Argument: CompleteArgument{Name: req.Argument, Value: req.Value},
	}
	if len(req.ContextArgs) > 0 {
		params.Context = &CompleteContext{Arguments: req.ContextArgs}
	}
	var result CompleteResult
	if err := c.sendRequest(ctx, "completion/complete", params, &result); err != nil {
		return nil, fmt.Errorf("mcp[%s]: completion/complete: %w", c.name, err)
	}
	return &result, nil
}
