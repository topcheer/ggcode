package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

const ProtocolVersion20260728 = "2026-07-28"

const (
	MetaKeyProtocolVersion    = "io.modelcontextprotocol/protocolVersion"
	MetaKeyClientInfo         = "io.modelcontextprotocol/clientInfo"
	MetaKeyClientCapabilities = "io.modelcontextprotocol/clientCapabilities"
	MetaKeyServerInfo         = "io.modelcontextprotocol/serverInfo"
)

const ErrorCodeUnsupportedProtocolVersion = -32022

const ErrorCodeMethodNotFound = -32601

func clientMetaName() Implementation { return Implementation{Name: "ggcode", Version: "0.1.0"} }

type UnsupportedProtocolVersionError struct {
	Requested string
	Supported []string
	Err       *Error
}

func (e *UnsupportedProtocolVersionError) Error() string {
	return fmt.Sprintf("mcp: server does not support protocol version %q (supported: %s)",
		e.Requested, strings.Join(e.Supported, ", "))
}

func (e *UnsupportedProtocolVersionError) Unwrap() error { return e.Err }

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

var modernMCPProtocolVersions = []string{ProtocolVersion20260728}

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

type DiscoverMeta struct {
	ServerInfo *Implementation `json:"io.modelcontextprotocol/serverInfo,omitempty"`
}

type DiscoverResult struct {
	ResultType        string     `json:"resultType,omitempty"`
	SupportedVersions []string   `json:"supportedVersions"`
	Capabilities      ServerCaps `json:"capabilities,omitempty"`
	Instructions      string     `json:"instructions,omitempty"`

	CacheableResult
	Meta *DiscoverMeta `json:"_meta,omitempty"`
}

func (r *DiscoverResult) ServerInfo() Implementation {
	if r == nil || r.Meta == nil || r.Meta.ServerInfo == nil {
		return Implementation{}
	}
	return *r.Meta.ServerInfo
}

func (c *Client) EnableStateless() {
	c.mu.Lock()
	c.stateless = true
	c.mu.Unlock()
}

func (c *Client) StatelessEnabled() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.stateless
}

func (c *Client) ModernVersion() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.modernVersion
}

func (c *Client) modernServerInfoLocked() (Implementation, bool) {
	if c.modernServerInfo == nil {
		return Implementation{}, false
	}
	return *c.modernServerInfo, true
}

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

func (c *Client) setModernVersionIfChanged(version string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.modernVersion == version {
		return false
	}
	c.modernVersion = version
	return true
}

func (c *Client) clientCapsLocked() ClientCaps {
	caps := ClientCaps{
		Roots: struct {
			ListChanged bool `json:"listChanged,omitempty"`
		}{ListChanged: true},
	}
	if c.samplingHandler != nil {

		caps.Sampling = &SamplingCapability{Tools: &struct{}{}}
	}
	if c.elicitationHandler != nil {

		caps.Elicitation = &ElicitationCapability{Form: &struct{}{}, URL: &struct{}{}}
	}

	caps.Tasks = &struct{}{}
	return caps
}

func (c *Client) clientCaps() ClientCaps {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.clientCapsLocked()
}

func (c *Client) modernRequestMeta(version string) map[string]any {
	return map[string]any{
		MetaKeyProtocolVersion:    version,
		MetaKeyClientInfo:         clientMetaName(),
		MetaKeyClientCapabilities: c.clientCaps(),
	}
}

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

func (c *Client) statelessWanted() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.stateless
}

func (c *Client) Discover(ctx context.Context) (*DiscoverResult, error) {
	if c.closed.Load() {
		return nil, fmt.Errorf("mcp[%s]: connection closed", c.name)
	}

	version := c.ModernVersion()
	if version == "" {
		version = ProtocolVersion20260728
	}
	paramsJSON := injectMeta([]byte("{}"), c.modernRequestMeta(version))
	var params json.RawMessage = paramsJSON
	var result DiscoverResult
	if err := c.sendRequest(ctx, "server/discover", params, &result); err != nil {
		return nil, fmt.Errorf("mcp[%s]: server/discover: %w", c.name, err)
	}
	return &result, nil
}

func (c *Client) probeModern(ctx context.Context) (*InitializeResult, error) {
	res, err := c.Discover(ctx)
	if err != nil {

		u, unsupported := asUnsupportedProtocolVersion(err)
		if !unsupported {
			return nil, err
		}

		v := selectModernVersion(u.Supported)
		if v == "" && len(u.Supported) > 0 {
			v = u.Supported[0]
		}

		if v == "" || !c.setModernVersionIfChanged(v) {
			return nil, u
		}
		retry, rerr := c.Discover(ctx)
		if rerr != nil {

			return nil, errors.Join(
				fmt.Errorf("mcp[%s]: discover retry after -32022 negotiation with %s failed", c.name, v),
				rerr, u)
		}
		res = retry
	}
	version := selectModernVersion(res.SupportedVersions)
	if version == "" {

		c.mu.Lock()
		set := c.modernVersion
		c.mu.Unlock()
		if set != "" {
			for _, s := range res.SupportedVersions {
				if s == set {
					version = set
					break
				}
			}
		}
	}
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

	c.setNegotiatedState(version, res.Capabilities)

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

func (c *Client) DiscoverStateless(ctx context.Context) (*DiscoverResult, error) {
	return c.Discover(ctx)
}

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
