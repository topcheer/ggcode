package mcp

// MCP 2026-07-28 stateless protocol core (SEP-2575) client support.
//
// The 2026-07-28 revision removed the protocol-level session and the
// initialize/notifications/initialized handshake. Every request now carries
// its protocol version, client identity and client capabilities as
// per-request _meta members (io.modelcontextprotocol/protocolVersion,
// .../clientInfo, .../clientCapabilities), and servers MUST implement
// server/discover to advertise their supported versions, capabilities and
// identity up front.
//
// A version mismatch surfaces as UnsupportedProtocolVersionError: JSON-RPC
// error code -32022 whose data carries the "supported" version list (and the
// "requested" version). A client SHOULD select a mutually supported version
// and retry, or surface an actionable error when none exists.
//
// Dual-era interoperability (spec basic/versioning): a client that supports
// both eras detects the server's era with transport-specific mechanics —
// stdio probes with server/discover and falls back on any error that is not
// a recognized modern error; a recognized modern error (-32022) identifies a
// modern server and the client retries with a supported version instead of
// falling back. ggcode is a dual-era client: legacy (initialize handshake,
// protocol versions 2024-11-05..2025-11-25) remains the default and
// unchanged, and this opt-in stateless mode is enabled per server via
// Client.EnableStateless (config: mcp server "stateless: true").

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// ProtocolVersion20260728 is the first stateless (modern-era) MCP protocol
// revision: per-request _meta versioning, server/discover, and the
// UnsupportedProtocolVersionError retry contract (SEP-2575).
const ProtocolVersion20260728 = "2026-07-28"

// _meta key names for the modern per-request envelope (SEP-2575).
const (
	MetaKeyProtocolVersion    = "io.modelcontextprotocol/protocolVersion"
	MetaKeyClientInfo         = "io.modelcontextprotocol/clientInfo"
	MetaKeyClientCapabilities = "io.modelcontextprotocol/clientCapabilities"
	MetaKeyServerInfo         = "io.modelcontextprotocol/serverInfo"
)

// ErrorCodeUnsupportedProtocolVersion is the 2026-07-28 server-error code
// for a request whose _meta protocol version the server does not implement
// (data.supported lists the versions it does).
const ErrorCodeUnsupportedProtocolVersion = -32022

// ErrorCodeMethodNotFound is the standard JSON-RPC "method not found" code;
// on the server/discover probe it identifies a legacy (handshake-era) server
// per the dual-era fallback rules.
const ErrorCodeMethodNotFound = -32601

// clientMetaName is the client identity ggcode advertises in the per-request
// envelope and in legacy initialize (kept consistent with Initialize).
func clientMetaName() Implementation { return Implementation{Name: "ggcode", Version: "0.1.0"} }

// UnsupportedProtocolVersionError is returned when a server rejects a
// request with the 2026-07-28 -32022 error. Supported lists the protocol
// versions the server advertised; Requested echoes the rejected version
// when the server included it in the error data.
type UnsupportedProtocolVersionError struct {
	Requested string
	Supported []string
	Err       *Error
}

func (e *UnsupportedProtocolVersionError) Error() string {
	return fmt.Sprintf("mcp: server does not support protocol version %q (supported: %s)",
		e.Requested, strings.Join(e.Supported, ", "))
}

// Unwrap exposes the raw JSON-RPC error to errors.Is/As chains.
func (e *UnsupportedProtocolVersionError) Unwrap() error { return e.Err }

// asUnsupportedProtocolVersion recognizes the -32022 modern error inside any
// error chain. Per the dual-era rules, this code specifically (not a bare
// method-not-found) identifies a modern server.
func asUnsupportedProtocolVersion(err error) (*UnsupportedProtocolVersionError, bool) {
	var jerr *Error
	if !errors.As(err, &jerr) || jerr == nil {
		return nil, false
	}
	if jerr.Code != ErrorCodeUnsupportedProtocolVersion {
		return nil, false
	}
	u := &UnsupportedProtocolVersionError{Err: jerr}
	if len(jerr.Data) > 0 {
		var data struct {
			Supported []string `json:"supported"`
			Requested string   `json:"requested"`
		}
		if json.Unmarshal(jerr.Data, &data) == nil {
			u.Supported = data.Supported
			u.Requested = data.Requested
		}
	}
	return u, true
}

// modernMCPProtocolVersions lists the stateless-era protocol versions this
// client can speak, most preferred first. With today's client the list holds
// exactly one entry; the slice (rather than a single constant) keeps the
// version-selection and retry paths honest for future revisions.
var modernMCPProtocolVersions = []string{ProtocolVersion20260728}

// selectModernVersion picks the most preferred modern version the server
// supports, or "" when there is no mutual version (the dual-era fallback
// signal: no mutually supported modern version → use the legacy handshake).
func selectModernVersion(supported []string) string {
	for _, mine := range modernMCPProtocolVersions {
		for _, s := range supported {
			if s == mine {
				return mine
			}
		}
	}
	return ""
}

// DiscoverMeta carries the modern result _meta members.
type DiscoverMeta struct {
	ServerInfo *Implementation `json:"io.modelcontextprotocol/serverInfo,omitempty"`
}

// DiscoverResult is the result of server/discover (spec 2026-07-28,
// server/discover). Servers SHOULD include serverInfo under _meta; it is
// self-reported, so callers must not make security decisions from it.
type DiscoverResult struct {
	// ResultType is "complete" for ordinary results; 2026-07-28 servers may
	// also return "input_required" interim results (MRTR, SEP-2322), which
	// never applies to discover. Clients must treat an omitted field as
	// "complete" (pre-modern servers), so an empty string is tolerated.
	ResultType        string     `json:"resultType,omitempty"`
	SupportedVersions []string   `json:"supportedVersions"`
	Capabilities      ServerCaps `json:"capabilities,omitempty"`
	Instructions      string     `json:"instructions,omitempty"`
	// CacheableResult: server/discover supports caching (SEP-2549). An
	// absent or non-positive TTLms means do-not-cache.
	CacheableResult
	Meta *DiscoverMeta `json:"_meta,omitempty"`
}

// ServerInfo extracts the self-reported server identity from the result
// _meta (zero value when the server omitted it).
func (r *DiscoverResult) ServerInfo() Implementation {
	if r == nil || r.Meta == nil || r.Meta.ServerInfo == nil {
		return Implementation{}
	}
	return *r.Meta.ServerInfo
}

// EnableStateless opts the client into modern (stateless, per-request _meta)
// operation. The next Initialize probes server/discover first; a success
// switches the client to the negotiated modern version and skips the legacy
// handshake entirely, while any failure that is not a recognized modern
// error falls back to the legacy initialize path (dual-era client, spec
// basic/versioning). stdio clients SHOULD probe first so legacy servers fail
// deterministically instead of hanging an era-ambiguous initialize.
//
// Legacy behavior is byte-for-byte unchanged until this is called.
func (c *Client) EnableStateless() {
	c.mu.Lock()
	c.stateless = true
	c.mu.Unlock()
}

// StatelessEnabled reports whether the stateless opt-in is set.
func (c *Client) StatelessEnabled() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.stateless
}

// ModernVersion returns the modern protocol version the client is currently
// operating under, or "" when operating in legacy (handshake) mode.
func (c *Client) ModernVersion() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.modernVersion
}

// modernServerInfoLocked returns the server identity advertised by
// server/discover. Caller holds c.mu.
func (c *Client) modernServerInfoLocked() (Implementation, bool) {
	if c.modernServerInfo == nil {
		return Implementation{}, false
	}
	return *c.modernServerInfo, true
}

// ServerInfo returns the server identity from server/discover (modern mode)
// or from the legacy initialize result (ServerInfo field), zero value when
// neither advertised one.
func (c *Client) ServerInfo() Implementation {
	c.mu.Lock()
	if si, ok := c.modernServerInfoLocked(); ok {
		c.mu.Unlock()
		return si
	}
	si := c.legacyServerInfo
	c.mu.Unlock()
	return si
}

// setModernVersionIfChanged switches the client to version and reports
// whether the version actually changed. The unchanged case also gates the
// -32022 retry: retrying a request with the same version the server just
// rejected can only reproduce the failure.
func (c *Client) setModernVersionIfChanged(version string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.modernVersion == version {
		return false
	}
	c.modernVersion = version
	return true
}

// clientCapsLocked builds the client capability object for the envelope and
// the legacy initialize request. Caller holds c.mu (the handler accessors
// are the mu-synchronized *Locked variants).
func (c *Client) clientCapsLocked() ClientCaps {
	caps := ClientCaps{
		Roots: struct {
			ListChanged bool `json:"listChanged,omitempty"`
		}{ListChanged: true},
	}
	if c.samplingHandler != nil {
		// MCP 2025-11-25 (SEP-1577): ggcode's sampling path accepts tools and
		// toolChoice, forwards them to the LLM provider, and relays tool_use
		// results with stopReason "toolUse" - advertise that support.
		caps.Sampling = &SamplingCapability{Tools: &struct{}{}}
	}
	if c.elicitationHandler != nil {
		// MCP 2025-11-25: advertise both elicitation modes. ggcode supports
		// form mode in-band (ask_user surfaces) and URL mode as a
		// consent-gated out-of-band handoff (it never auto-opens URLs).
		caps.Elicitation = &ElicitationCapability{Form: &struct{}{}, URL: &struct{}{}}
	}
	// SEP-1686: the client always implements the task management methods
	// (tasks/get, tasks/list, tasks/cancel, tasks/result), so declare the
	// tasks capability unconditionally - it flows through both the legacy
	// initialize params and the modern per-request _meta envelope.
	caps.Tasks = &struct{}{}
	return caps
}

// clientCaps snapshots clientCapsLocked under c.mu.
func (c *Client) clientCaps() ClientCaps {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.clientCapsLocked()
}

// modernRequestMeta builds the per-request _meta envelope (spec example
// shape: protocolVersion + clientInfo + clientCapabilities).
func (c *Client) modernRequestMeta(version string) map[string]any {
	return map[string]any{
		MetaKeyProtocolVersion:    version,
		MetaKeyClientInfo:         clientMetaName(),
		MetaKeyClientCapabilities: c.clientCaps(),
	}
}

// injectMeta merges meta into a marshaled params object's _meta field,
// preserving any caller-provided _meta members (e.g. progressToken) and
// overlaying the client-level envelope keys. Unmarshalable or non-object
// params are returned unchanged — envelopes are additive metadata, never a
// reason to fail a request.
func injectMeta(paramsJSON []byte, meta map[string]any) []byte {
	var params map[string]json.RawMessage
	if len(paramsJSON) == 0 || json.Unmarshal(paramsJSON, &params) != nil || params == nil {
		return paramsJSON
	}
	existing := map[string]any{}
	if raw, ok := params["_meta"]; ok {
		_ = json.Unmarshal(raw, &existing)
		if existing == nil {
			existing = map[string]any{}
		}
	}
	for k, v := range meta {
		existing[k] = v
	}
	raw, err := json.Marshal(existing)
	if err != nil {
		return paramsJSON
	}
	params["_meta"] = raw
	out, err := json.Marshal(params)
	if err != nil {
		return paramsJSON
	}
	return out
}

// statelessWanted snapshots the opt-in flag under c.mu.
func (c *Client) statelessWanted() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.stateless
}

// Discover sends the server/discover request (MCP 2026-07-28, SEP-2575). It
// carries no body parameters beyond the standard _meta envelope, which is
// always attached here regardless of the client's current mode — discover is
// itself a modern method and the spec example shows the envelope on it.
//
// The returned result supports caching via its CacheableResult fields, and
// callers can surface identity/capabilities from a single call instead of
// probing tools/list, prompts/list and resources/list separately.
func (c *Client) Discover(ctx context.Context) (*DiscoverResult, error) {
	if c.closed.Load() {
		return nil, fmt.Errorf("mcp[%s]: connection closed", c.name)
	}
	paramsJSON := injectMeta([]byte("{}"), c.modernRequestMeta(ProtocolVersion20260728))
	var params json.RawMessage = paramsJSON
	var result DiscoverResult
	if err := c.sendRequest(ctx, "server/discover", params, &result); err != nil {
		return nil, fmt.Errorf("mcp[%s]: server/discover: %w", c.name, err)
	}
	return &result, nil
}

// probeModern runs the server/discover probe and, on success, switches the
// client into modern stateless operation: the negotiated modern version and
// server capabilities are cached exactly like the legacy handshake state
// (so capability gates, the HTTP MCP-Protocol-Version header and listing
// caches all keep working), and a legacy-shaped InitializeResult is
// synthesized from the discover payload.
func (c *Client) probeModern(ctx context.Context) (*InitializeResult, error) {
	res, err := c.Discover(ctx)
	if err != nil {
		return nil, err
	}
	version := selectModernVersion(res.SupportedVersions)
	if version == "" {
		return nil, fmt.Errorf("mcp[%s]: no mutually supported modern protocol version (server supports %s)",
			c.name, strings.Join(res.SupportedVersions, ", "))
	}

	serverInfo := res.ServerInfo()
	c.mu.Lock()
	c.instructions = res.Instructions
	if serverInfo != (Implementation{}) {
		c.modernServerInfo = &serverInfo
	}
	c.mu.Unlock()

	// setNegotiatedState takes c.mu itself — call it outside the block
	// above. It also clears the listing cache, mirroring a fresh handshake.
	c.setNegotiatedState(version, res.Capabilities)

	// Record the modern mode version last so concurrent request paths see a
	// fully populated negotiated state the moment they observe the envelope.
	c.mu.Lock()
	c.modernVersion = version
	c.mu.Unlock()

	return &InitializeResult{
		ProtocolVersion: version,
		Capabilities:    res.Capabilities,
		ServerInfo:      serverInfo,
		Instructions:    res.Instructions,
	}, nil
}

// DiscoverStateless probes the server and reports its modern-era identity
// without changing the client's operating mode: useful for doctor-style
// diagnostics and UI display. The error wraps the typed
// UnsupportedProtocolVersionError when the server rejects the request's
// protocol version.
func (c *Client) DiscoverStateless(ctx context.Context) (*DiscoverResult, error) {
	return c.Discover(ctx)
}

// envelopeModernMeta attaches the modern per-request _meta envelope when the
// client is operating in stateless mode. Legacy traffic is untouched.
func (c *Client) envelopeModernMeta(method string, paramsJSON []byte) []byte {
	c.mu.Lock()
	if !c.stateless || c.modernVersion == "" {
		c.mu.Unlock()
		return paramsJSON
	}
	meta := map[string]any{
		MetaKeyProtocolVersion:    c.modernVersion,
		MetaKeyClientInfo:         clientMetaName(),
		MetaKeyClientCapabilities: c.clientCapsLocked(),
	}
	c.mu.Unlock()
	return injectMeta(paramsJSON, meta)
}
