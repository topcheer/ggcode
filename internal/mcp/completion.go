package mcp

import (
	"context"
	"fmt"
)

// MCP completion support (specification 2025-06-18, "Completion"):
// servers that advertise the `completions` capability can return argument
// autocompletion suggestions for prompts (ref/prompt) and resource templates
// (ref/resource). Clients send completion/complete with a partial argument
// value and receive relevance-ranked suggestions (max 100 per response).

// CompleteRequest describes one completion/complete request.
type CompleteRequest struct {
	// Ref identifies what is being completed: a prompt (ref/prompt) or a
	// resource template URI (ref/resource). Exactly one must be set.
	Ref CompleteReference
	// Argument is the prompt/URI-template argument being completed.
	Argument string
	// Value is the current partial argument value.
	Value string
	// ContextArgs optionally carries already-resolved sibling arguments so
	// servers can provide context-aware suggestions (spec: clients SHOULD
	// include them for multi-argument refs).
	ContextArgs map[string]string
}

// CompleteReference identifies what is being completed. Exactly one of Name
// (ref/prompt) or URI (ref/resource) must be set; the spec's ref type
// discriminator is derived from which field is set.
type CompleteReference struct {
	// Name references a prompt by name (type "ref/prompt").
	Name string `json:"name,omitempty"`
	// URI references a resource template by URI (type "ref/resource").
	URI string `json:"uri,omitempty"`
}

// referenceType derives the spec's ref discriminator from the reference kind.
func (r CompleteReference) referenceType() string {
	if r.URI != "" {
		return "ref/resource"
	}
	return "ref/prompt"
}

// completeRef is the wire form of CompleteReference.
type completeRef struct {
	Type string `json:"type"`
	Name string `json:"name,omitempty"`
	URI  string `json:"uri,omitempty"`
}

// CompleteArgument is the argument being completed.
type CompleteArgument struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// CompleteContext carries already-resolved arguments.
type CompleteContext struct {
	Arguments map[string]string `json:"arguments,omitempty"`
}

// CompleteParams is the completion/complete request parameters object.
type CompleteParams struct {
	Ref      completeRef      `json:"ref"`
	Argument CompleteArgument `json:"argument"`
	Context  *CompleteContext `json:"context,omitempty"`
}

// Completion is the completion result object.
type Completion struct {
	// Values are the suggestions, ranked by relevance (spec max: 100).
	Values []string `json:"values"`
	// Total is the optional total number of available matches.
	Total *int `json:"total,omitempty"`
	// HasMore indicates whether additional results exist beyond Values.
	HasMore bool `json:"hasMore,omitempty"`
}

// CompleteResult is the completion/complete response result.
type CompleteResult struct {
	Completion Completion `json:"completion"`
}

// Complete sends a completion/complete request for the given reference.
//
// The request is gated on the server's completions capability (mirrors
// SubscribeResource) so a server that does not advertise the capability
// receives no unsupported call (spec error -32601 contract).
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
