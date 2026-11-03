package a2a

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
)

const (
	MetadataKeyOnBehalfOf = "ggcode.dev/on-behalf-of"
	MetadataKeyDelegation = "ggcode.dev/delegation"
)

type delegationPayload struct {
	Intent          string `json:"intent,omitempty"`
	IssuedAt        string `json:"issued-at,omitempty"`
	DelegationDepth int    `json:"delegation-depth"`
}

type DelegationPolicy struct {
	RequireOnBehalfOf bool

	MaxDepth int
}

func (c *Client) SetDelegation(onBehalfOf, intent string) {
	c.mu.Lock()
	c.delegationOnBehalfOf = onBehalfOf
	c.delegationIntent = intent
	c.mu.Unlock()
}

func (c *Client) injectDelegationMeta(msg *Message) {
	c.mu.RLock()
	onBehalfOf, intent := c.delegationOnBehalfOf, c.delegationIntent
	c.mu.RUnlock()
	if onBehalfOf == "" && intent == "" {
		return
	}

	merged := make(map[string]json.RawMessage)
	if len(msg.Metadata) > 0 {
		if err := json.Unmarshal(msg.Metadata, &merged); err != nil {
			debug.Log("a2a", "delegation: skip injection, existing message metadata is not a JSON object: %v", err)
			return
		}
	}
	if onBehalfOf != "" {
		b, err := json.Marshal(onBehalfOf)
		if err == nil {
			merged[MetadataKeyOnBehalfOf] = b
		}
	}
	payload := delegationPayload{
		Intent:          intent,
		IssuedAt:        time.Now().UTC().Format(time.RFC3339),
		DelegationDepth: 1,
	}
	if b, err := json.Marshal(payload); err == nil {
		merged[MetadataKeyDelegation] = b
	}
	out, err := json.Marshal(merged)
	if err != nil {
		debug.Log("a2a", "delegation: skip injection, marshal merged metadata: %v", err)
		return
	}
	msg.Metadata = out
}

func (s *Server) checkDelegation(w http.ResponseWriter, id json.RawMessage, msg *Message) bool {
	if !s.delegation.RequireOnBehalfOf && s.delegation.MaxDepth <= 0 {
		return true
	}
	var meta map[string]json.RawMessage
	if len(msg.Metadata) > 0 {
		if err := json.Unmarshal(msg.Metadata, &meta); err != nil {
			meta = nil
		}
	}
	if s.delegation.RequireOnBehalfOf {
		var principal string
		raw, ok := meta[MetadataKeyOnBehalfOf]
		if !ok || json.Unmarshal(raw, &principal) != nil || principal == "" {
			writeRPCError(w, id, &JSONRPCError{
				Code:    -32000,
				Message: "delegation policy: task missing ggcode.dev/on-behalf-of declaration",
			})
			return false
		}
	}
	if s.delegation.MaxDepth > 0 {
		if raw, ok := meta[MetadataKeyDelegation]; ok {
			var p delegationPayload
			if json.Unmarshal(raw, &p) == nil && p.DelegationDepth > s.delegation.MaxDepth {
				writeRPCError(w, id, &JSONRPCError{
					Code:    -32000,
					Message: fmt.Sprintf("delegation policy: delegation-depth %d exceeds max %d", p.DelegationDepth, s.delegation.MaxDepth),
				})
				return false
			}
		}
	}
	return true
}
