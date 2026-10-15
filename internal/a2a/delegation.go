package a2a

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
)

// Delegation metadata keys (A-JWT-lite, sa-84). Reverse-domain prefixed so
// they never collide with A2A standard fields; the semantics follow the
// WIMSE act_on_behalf work (draft-sweeney-wimse-credential-delegation) and
// arXiv:2509.13597 (A-JWT), reduced to declaration-only form.
//
// SECURITY: these are DECLARATIONS, not signatures. They do not prevent
// forgery or tampering and MUST NOT be used as an authentication basis —
// they give auditors and policy-enforcing receivers an anchor. Authentication
// remains the job of the existing inbound JWT iss/aud validation
// (internal/auth) and mTLS layers; this surface is orthogonal to those.
const (
	// MetadataKeyOnBehalfOf carries the principal (e.g. username) on whose
	// behalf the sending agent is acting.
	MetadataKeyOnBehalfOf = "ggcode.dev/on-behalf-of"
	// MetadataKeyDelegation carries the structured delegation payload
	// (intent summary, issue time, chain depth).
	MetadataKeyDelegation = "ggcode.dev/delegation"
)

// delegationPayload is the structured declaration nested under
// MetadataKeyDelegation in outgoing message metadata.
type delegationPayload struct {
	Intent          string `json:"intent,omitempty"`
	IssuedAt        string `json:"issued-at,omitempty"`
	DelegationDepth int    `json:"delegation-depth"`
}

// DelegationPolicy configures INBOUND (server-side) enforcement of the
// delegation declarations. The zero value disables every check — zero
// behavior change for existing deployments.
type DelegationPolicy struct {
	// RequireOnBehalfOf rejects inbound tasks that carry no
	// ggcode.dev/on-behalf-of declaration.
	RequireOnBehalfOf bool

	// MaxDepth rejects inbound tasks whose delegation-depth exceeds this
	// bound, guarding against unbounded agent-to-agent forwarding chains.
	// 0 (default) = no depth check. Recommended value: 3.
	MaxDepth int
}

// SetDelegation declares the principal this client acts on behalf of and a
// one-line intent summary that are injected into the metadata of every
// outgoing task (A-JWT-lite). Both values are optional; while both are
// empty nothing is injected (default, zero behavior change). Values can be
// cleared by passing two empty strings.
func (c *Client) SetDelegation(onBehalfOf, intent string) {
	c.mu.Lock()
	c.delegationOnBehalfOf = onBehalfOf
	c.delegationIntent = intent
	c.mu.Unlock()
}

// WithDelegation is the ClientOption form of SetDelegation.
func WithDelegation(onBehalfOf, intent string) ClientOption {
	return func(c *Client) { c.SetDelegation(onBehalfOf, intent) }
}

// injectDelegationMeta merges the delegation declaration into msg.Metadata.
// It never fails the send: on malformed pre-existing metadata the injection
// is skipped and logged — a cosmetic declaration must not break delivery.
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

// checkDelegation enforces the inbound DelegationPolicy on a task message.
// Default (disabled) policy performs no metadata parsing at all. Returns
// false when the request was rejected (JSON-RPC error already written).
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
		// The declaration must deserialize as a non-empty string. A raw JSON
		// null (or any non-string value such as 123/true/[]) is NOT a valid
		// on-behalf-of declaration and must be rejected (#4168).
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
