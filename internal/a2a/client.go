package a2a

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/safego"
	"github.com/topcheer/ggcode/internal/util"
)

// TokenProvider obtains an OAuth2/OIDC access token interactively.
// Implementations include PKCE flow, Device flow, or any custom mechanism.
type TokenProvider interface {
	GetToken(ctx context.Context) (accessToken, refreshToken string, expiry time.Time, err error)
}

// Client is an A2A protocol client that sends tasks to remote agents.
type Client struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
	card       atomic.Pointer[AgentCard] // #934: cached agent card (nil until discovered); atomic - MCPBridgeTools shares one Client across bridge tools called concurrently by external MCP clients

	// mu protects all auth-related fields below from concurrent access.
	mu            sync.RWMutex
	authMethod    string // "", "apiKey", "bearer", "mtls"
	bearerToken   string // cached OAuth2 access token
	refreshToken  string // for token refresh
	tokenExpiry   time.Time
	tokenProvider TokenProvider // auto-acquire token when needed
	apiKeyName    string        // #1458-A: card-declared header name
	apiKeyIn      string        // #1458-A: card-declared location (header/query)
}

// ClientOption configures a Client.
type ClientOption func(*Client)

// WithBearerToken sets a pre-obtained OAuth2/OIDC bearer token.
func WithBearerToken(token string) ClientOption {
	return func(c *Client) {
		c.bearerToken = token
		c.authMethod = "bearer"
	}
}

// WithMTLS configures the client to use mutual TLS.
func WithMTLS(tlsConfig *tls.Config) ClientOption {
	return func(c *Client) {
		// Use util.WrapTransport to preserve proxy support (ProxyFromEnvironment)
		// and insecure-mode handling that the non-mTLS path gets automatically.
		transport := util.WrapTransport(&http.Transport{
			TLSClientConfig:   tlsConfig,
			ForceAttemptHTTP2: false,
		})
		// mTLS explicitly requires full certificate verification for both
		// directions. Override any InsecureSkipVerify that WrapTransport may
		// have set from the GGCODE_INSECURE env var (issue #19).
		transport.TLSClientConfig.InsecureSkipVerify = false
		// #1458-B: Client.Timeout covers the ENTIRE interaction including
		// response-body reads - the hard 15min cut killed long SSE streams
		// (batch/deep-research agents are A2A's core scenarios) with no way
		// for callers to extend it (ctx deadlines don't override a shorter
		// Client.Timeout). Header-only timeout + ctx governs duration now.
		transport.ResponseHeaderTimeout = 15 * time.Minute
		c.httpClient = &http.Client{
			Transport: transport,
			// #1458-C: strip the custom X-API-Key header on cross-host
			// redirects - Go only strips built-in sensitive headers
			// (Authorization/Cookies), a custom key followed a 302 to any
			// host verbatim.
			CheckRedirect: stripKeyOnRedirect,
		}
		c.authMethod = "mtls"
	}
}

// WithTokenProvider sets a token provider for automatic OAuth2 token acquisition.
// When NegotiateAuth encounters a server requiring bearer auth and no token is
// available, it calls the provider to obtain one (e.g., via PKCE or Device flow).
func WithTokenProvider(p TokenProvider) ClientOption {
	return func(c *Client) {
		c.tokenProvider = p
	}
}

// NewClient creates a new A2A client targeting the given server URL.
func NewClient(baseURL, apiKey string, opts ...ClientOption) *Client {
	c := &Client{
		baseURL:    strings.TrimRight(baseURL, "/"),
		apiKey:     apiKey,
		httpClient: util.NewInsecureAwareClient(15 * time.Minute),
	}
	if apiKey != "" && c.authMethod == "" {
		c.authMethod = "apiKey"
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// Discover fetches and caches the remote agent's Agent Card.
func (c *Client) Discover(ctx context.Context) (*AgentCard, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		c.baseURL+"/.well-known/agent.json", nil)
	if err != nil {
		return nil, fmt.Errorf("a2a discover: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("a2a discover: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("a2a discover: HTTP %d", resp.StatusCode)
	}

	var card AgentCard
	if err := json.NewDecoder(resp.Body).Decode(&card); err != nil {
		return nil, fmt.Errorf("a2a discover: decode: %w", err)
	}

	c.card.Store(&card)
	return &card, nil
}

// Card returns the cached agent card (nil if Discover hasn't been called).
func (c *Client) Card() *AgentCard { return c.card.Load() }

// NegotiateAuth reads the Agent Card's securitySchemes and prepares the
// appropriate authentication mechanism.
//
// This must be called after Discover(). It checks what the server requires
// and configures the client accordingly:
//
//   - If server declares no security → no auth needed
//   - If server has apiKey scheme and client has apiKey → use API Key
//   - If server has oauth2/openIdConnect scheme → use Bearer token
//     (must be set via WithBearerToken or SetBearerToken before calling)
//   - If server has mutualTLS scheme → use mTLS
//     (must be configured via WithMTLS option)
//
// Returns an error if the server requires an auth mechanism the client
// hasn't been configured for.
func (c *Client) NegotiateAuth() error {
	card := c.card.Load()
	if card == nil {
		return fmt.Errorf("a2a: NegotiateAuth called before Discover")
	}

	// No security requirements → nothing to do
	if len(card.Security) == 0 && len(card.SecuritySchemes) == 0 {
		c.mu.Lock()
		c.authMethod = ""
		c.mu.Unlock()
		return nil
	}

	// Try to match client capabilities with server requirements
	for _, req := range card.Security {
		for schemeName := range req {
			scheme, ok := card.SecuritySchemes[schemeName]
			if !ok {
				continue
			}

			switch scheme.Type {
			case "apiKey":
				c.mu.RLock()
				hasKey := c.apiKey != ""
				c.mu.RUnlock()
				if hasKey {
					c.mu.Lock()
					c.authMethod = "apiKey"
					// #1458-A: honor the card's declared transport - the old
					// path accepted any apiKey scheme but setAuth ALWAYS sent
					// X-API-Key, so a card declaring "name":"X-Goog-Api-Key"
					// or in:"query" negotiated fine then failed with 401 on
					// the first RPC (masked in ggcode-to-ggcode by the server's
					// own hard-coded header).
					c.apiKeyName = scheme.Name
					c.apiKeyIn = scheme.Location
					if c.apiKeyName == "" {
						c.apiKeyName = "X-API-Key"
					}
					if c.apiKeyIn == "" {
						c.apiKeyIn = "header"
					}
					if c.apiKeyIn == "query" {
						// The client cannot rewrite the endpoint URL per-card
						// here; report explicitly instead of 401-at-first-call.
						return fmt.Errorf("a2a: card requires apiKey in query param %q - not supported", scheme.Name)
					}
					c.mu.Unlock()
					return nil
				}
			case "http", "bearer":
				if c.tryBearerToken() {
					return nil
				}
			case "oauth2", "openIdConnect":
				if c.tryBearerToken() {
					return nil
				}
			case "mutualTLS":
				c.mu.RLock()
				isMTLS := c.authMethod == "mtls"
				c.mu.RUnlock()
				if isMTLS {
					return nil
				}
			}
		}
	}

	// Also check if client already has a configured auth that might work.
	// The apiKey fallback is only allowed when the server actually declares
	// an apiKey security scheme — otherwise a client constructed with
	// NewClient(url, "some-key") would silently "succeed" against a server
	// that only accepts bearer/oauth2 and fail later with 401 on the first
	// real request (fix #257).
	c.mu.RLock()
	method := c.authMethod
	c.mu.RUnlock()
	if method != "" {
		if method != "apiKey" || c.cardDeclaresAPIKey() {
			return nil
		}
		return fmt.Errorf("a2a: server requires bearer/oauth2 but client only has an apiKey (server schemes: %v); use WithBearerToken or WithTokenProvider",
			schemeNames(c.card.Load().SecuritySchemes))
	}

	return fmt.Errorf("a2a: server requires authentication but client has no matching credential (schemes: %v)",
		schemeNames(c.card.Load().SecuritySchemes))
}

// cardDeclaresAPIKey reports whether the discovered AgentCard declares any
// apiKey security scheme (fix #257).
func (c *Client) cardDeclaresAPIKey() bool {
	for _, scheme := range c.card.Load().SecuritySchemes {
		if scheme.Type == "apiKey" {
			return true
		}
	}
	return false
}

// tryBearerToken checks if we have a valid bearer token, or tries to obtain one.
func (c *Client) tryBearerToken() bool {
	// Check for a non-expired token under read lock
	c.mu.RLock()
	token := c.bearerToken
	expiry := c.tokenExpiry
	provider := c.tokenProvider
	c.mu.RUnlock()

	if token != "" && (expiry.IsZero() || time.Now().Before(expiry)) {
		c.mu.Lock()
		c.authMethod = "bearer"
		c.mu.Unlock()
		return true
	}

	// Try to obtain a token via the configured provider.
	// The GetToken call runs outside the lock to avoid blocking other
	// readers during interactive OAuth2 flows (can take minutes).
	if provider != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()

		accessToken, refreshToken, newExpiry, err := provider.GetToken(ctx)
		if err != nil {
			// #1458-C: device-code denial / timeout / network errors were
			// swallowed into a generic 'no matching credential' - at least
			// surface the cause for diagnostics.
			debug.Logf("[a2a] token provider failed: %v", err)
		}
		if err == nil && accessToken != "" {
			c.mu.Lock()
			c.bearerToken = accessToken
			c.refreshToken = refreshToken
			c.tokenExpiry = newExpiry
			c.authMethod = "bearer"
			c.mu.Unlock()
			return true
		}
	}

	return false
}

// SetBearerToken updates the bearer token (e.g., after OAuth2 token refresh).
func (c *Client) SetBearerToken(token string) {
	c.mu.Lock()
	c.bearerToken = token
	c.authMethod = "bearer"
	c.mu.Unlock()
}

// SetAPIKey updates the API key.
func (c *Client) SetAPIKey(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.apiKey = key
	if c.authMethod == "" {
		c.authMethod = "apiKey"
	}
}

// AuthMethod returns the negotiated authentication method.
func (c *Client) AuthMethod() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.authMethod
}

// setAuth applies the negotiated auth to an outgoing HTTP request.
func (c *Client) setAuth(req *http.Request) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	switch c.authMethod {
	case "apiKey":
		name := c.apiKeyName
		if name == "" {
			name = "X-API-Key"
		}
		req.Header.Set(name, c.apiKey)
	case "bearer":
		req.Header.Set("Authorization", "Bearer "+c.bearerToken)
		// mTLS is handled at TLS level, no headers needed
	}
}

// SendMessage sends a synchronous message and waits for task completion.
// If existingTaskID is non-empty, continues an existing task (multi-turn).
func (c *Client) SendMessage(ctx context.Context, skill, text string, existingTaskID ...string) (*Task, error) {
	taskID := ""
	if len(existingTaskID) > 0 {
		taskID = existingTaskID[0]
	}
	params := SendMessageParams{
		Message: Message{
			Role:      "user",
			MessageID: generateID(),
			Parts:     []Part{{Kind: "text", Text: text}},
		},
		Skill:  skill,
		TaskID: taskID,
	}

	var result Task
	if err := c.rpc(ctx, "message/send", params, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// SendMessageWithConfig sends a message with full configuration options.
func (c *Client) SendMessageWithConfig(ctx context.Context, params SendMessageParams) (*Task, error) {
	var result Task
	if err := c.rpc(ctx, "message/send", params, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// SendMessageStream sends a message and returns a channel of SSE events.
func (c *Client) SendMessageStream(ctx context.Context, skill, text string) (<-chan JSONRPCResponse, error) {
	params := SendMessageParams{
		Message: Message{
			Role:      "user",
			MessageID: generateID(),
			Parts:     []Part{{Kind: "text", Text: text}},
		},
		Skill: skill,
	}

	paramsJSON, err := json.Marshal(params)
	if err != nil {
		return nil, fmt.Errorf("a2a stream: marshal params: %w", err)
	}
	rpcReq := JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`1`),
		Method:  "message/stream",
		Params:  paramsJSON,
	}
	body, err := json.Marshal(rpcReq)
	if err != nil {
		return nil, fmt.Errorf("a2a stream: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("a2a stream: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	c.setAuth(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("a2a stream: http request: %w", err)
	}

	// #446: a 200 response may still carry a sync JSON-RPC error (the
	// server writes errors as HTTP 200 + application/json before SSE
	// headers are set) — parse that instead of feeding the JSON body to
	// the SSE decoder (which would yield a silent empty stream + nil error).
	if ct := resp.Header.Get("Content-Type"); strings.Contains(ct, "application/json") {
		respBody, _ := util.ReadAll(resp.Body, util.ReadLimitGeneral)
		resp.Body.Close()
		var rpcResp JSONRPCResponse
		if json.Unmarshal(respBody, &rpcResp) == nil && rpcResp.Error != nil {
			return nil, rpcResp.Error
		}
		return nil, fmt.Errorf("a2a stream: unexpected JSON response")
	}

	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("a2a stream: HTTP %d", resp.StatusCode)
	}

	ch := make(chan JSONRPCResponse, 32)
	// #448: pass ctx so decodeSSE can abort blocked channel sends when the
	// consumer is gone — a bare send leaked the goroutine AND the HTTP
	// connection permanently (buffer 32 only delayed it).
	safego.Go("a2a.client.streamRead", func() {
		defer close(ch)
		defer resp.Body.Close()
		decodeSSECtx(ctx, resp.Body, ch)
	})

	return ch, nil
}

// GetTask retrieves the current state of a task.
func (c *Client) GetTask(ctx context.Context, taskID string) (*Task, error) {
	params := GetTaskParams{ID: taskID}
	var result Task
	if err := c.rpc(ctx, "tasks/get", params, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// ListTasksResult holds the paginated response from tasks/list.
type ListTasksResult struct {
	Tasks     []Task `json:"tasks"`
	NextToken string `json:"nextToken,omitempty"`
}

// ListTasks retrieves a paginated list of tasks from the remote agent.
func (c *Client) ListTasks(ctx context.Context, pageToken string, pageSize int) (*ListTasksResult, error) {
	params := map[string]interface{}{}
	if pageToken != "" {
		params["pageToken"] = pageToken
	}
	if pageSize > 0 {
		params["pageSize"] = pageSize
	}
	var result ListTasksResult
	if err := c.rpc(ctx, "tasks/list", params, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// CancelTask requests cancellation of a running task.
func (c *Client) CancelTask(ctx context.Context, taskID string) (*Task, error) {
	params := CancelTaskParams{ID: taskID}
	var result Task
	if err := c.rpc(ctx, "tasks/cancel", params, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// GetExtendedAgentCard retrieves the authenticated extended agent card.
func (c *Client) GetExtendedAgentCard(ctx context.Context) (json.RawMessage, error) {
	var result json.RawMessage
	if err := c.rpc(ctx, "agent/getExtendedCard", struct{}{}, &result); err != nil {
		return nil, err
	}
	return result, nil
}

// SetPushConfig creates or updates a push notification config.
func (c *Client) SetPushConfig(ctx context.Context, cfg PushNotificationConfig) (*PushNotificationConfig, error) {
	var result PushNotificationConfig
	if err := c.rpc(ctx, "tasks/pushNotificationConfig/set", cfg, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// GetPushConfig retrieves a push notification config by ID.
func (c *Client) GetPushConfig(ctx context.Context, id string) (*PushNotificationConfig, error) {
	var result PushNotificationConfig
	if err := c.rpc(ctx, "tasks/pushNotificationConfig/get", map[string]string{"id": id}, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// ListPushConfigs returns all push notification configs.
func (c *Client) ListPushConfigs(ctx context.Context) ([]PushNotificationConfig, error) {
	var result []PushNotificationConfig
	if err := c.rpc(ctx, "tasks/pushNotificationConfig/list", struct{}{}, &result); err != nil {
		return nil, err
	}
	return result, nil
}

// DeletePushConfig removes a push notification config by ID.
func (c *Client) DeletePushConfig(ctx context.Context, id string) error {
	return c.rpc(ctx, "tasks/pushNotificationConfig/delete", map[string]string{"id": id}, nil)
}

// Resubscribe reconnects to a task's SSE stream. Use this when a previous
// SendMessageStream connection was interrupted.
func (c *Client) Resubscribe(ctx context.Context, taskID string) (<-chan JSONRPCResponse, error) {
	params := TaskSubscriptionParams{ID: taskID}
	paramsJSON, err := json.Marshal(params)
	if err != nil {
		return nil, fmt.Errorf("a2a resubscribe: marshal params: %w", err)
	}
	rpcReq := JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`1`),
		Method:  "tasks/resubscribe",
		Params:  paramsJSON,
	}
	body, err := json.Marshal(rpcReq)
	if err != nil {
		return nil, fmt.Errorf("a2a resubscribe: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("a2a resubscribe: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	c.setAuth(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("a2a resubscribe: http request: %w", err)
	}

	// Check Content-Type: if JSON (not SSE), this is a sync error response.
	ct := resp.Header.Get("Content-Type")
	if strings.Contains(ct, "application/json") {
		defer resp.Body.Close()
		respBody, _ := util.ReadAll(resp.Body, util.ReadLimitGeneral)
		var rpcResp JSONRPCResponse
		if json.Unmarshal(respBody, &rpcResp) == nil && rpcResp.Error != nil {
			return nil, rpcResp.Error
		}
		return nil, fmt.Errorf("a2a resubscribe: unexpected JSON response")
	}

	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("a2a resubscribe: HTTP %d", resp.StatusCode)
	}

	ch := make(chan JSONRPCResponse, 32)
	// #448: ctx-guarded decode, same as SendMessageStream.
	safego.Go("a2a.client.resubscribeRead", func() {
		defer close(ch)
		defer resp.Body.Close()
		decodeSSECtx(ctx, resp.Body, ch)
	})

	return ch, nil
}

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

func (c *Client) rpc(ctx context.Context, method string, params interface{}, result interface{}) error {
	paramsJSON, err := json.Marshal(params)
	if err != nil {
		return fmt.Errorf("a2a %s: marshal params: %w", method, err)
	}
	rpcReq := JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`1`),
		Method:  method,
		Params:  paramsJSON,
	}
	body, err := json.Marshal(rpcReq)
	if err != nil {
		return fmt.Errorf("a2a %s: marshal request: %w", method, err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("a2a %s: %w", method, err)
	}
	req.Header.Set("Content-Type", "application/json")
	c.setAuth(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("a2a %s: %w", method, err)
	}
	defer resp.Body.Close()

	respBody, err := util.ReadAll(resp.Body, util.ReadLimitGeneral)
	if err != nil {
		return fmt.Errorf("a2a %s: read: %w", method, err)
	}

	if resp.StatusCode != http.StatusOK {
		var rpcResp JSONRPCResponse
		if err := json.Unmarshal(respBody, &rpcResp); err == nil && rpcResp.Error != nil {
			return rpcResp.Error
		}
		msg := strings.TrimSpace(string(respBody))
		if msg == "" {
			msg = http.StatusText(resp.StatusCode)
		}
		return fmt.Errorf("a2a %s: HTTP %d: %s", method, resp.StatusCode, msg)
	}

	var rpcResp JSONRPCResponse
	if err := json.Unmarshal(respBody, &rpcResp); err != nil {
		return fmt.Errorf("a2a %s: decode: %w", method, err)
	}

	if rpcResp.Error != nil {
		return rpcResp.Error
	}

	if result != nil && rpcResp.Result != nil {
		resultJSON, err := json.Marshal(rpcResp.Result)
		if err != nil {
			return fmt.Errorf("a2a %s: re-marshal result: %w", method, err)
		}
		if err := json.Unmarshal(resultJSON, result); err != nil {
			return fmt.Errorf("a2a %s: unmarshal result: %w", method, err)
		}
	}

	return nil
}

// emitSSEData parses one accumulated SSE event payload and sends it on ch.
// Sends are ctx-guarded (#448). Returns false if ctx fired before the send,
// so the caller can stop decoding. Unparseable payloads are dropped silently,
// matching the original per-event behavior.
func emitSSEData(ctx context.Context, ch chan<- JSONRPCResponse, data string) bool {
	var resp JSONRPCResponse
	if json.Unmarshal([]byte(data), &resp) != nil {
		return true
	}
	select {
	case ch <- resp:
		return true
	case <-ctx.Done():
		return false
	}
}

// emitSSEFailure sends the terminal JSON-RPC internal-error event for a
// failed stream read (#565 F) so the consumer sees the stream died instead
// of waiting forever on a silently truncated one.
func emitSSEFailure(ctx context.Context, ch chan<- JSONRPCResponse, err error) {
	select {
	case ch <- JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`1`),
		Error: &JSONRPCError{
			Code:    -32603,
			Message: fmt.Sprintf("SSE stream read failed: %v", err),
		},
	}:
	case <-ctx.Done():
	}
}

// decodeSSECtx reads Server-Sent Events from r and sends them on ch. Sends
// are ctx-guarded (#448): a consumer that stops reading (cancel, timeout,
// early task-ID pickup) must not leave this goroutine blocked forever on
// ch <- with the HTTP body unclosed.
func decodeSSECtx(ctx context.Context, r io.Reader, ch chan<- JSONRPCResponse) {
	scanner := bufio.NewScanner(r)
	// #565 F: default 64KB token limit silently aborted mid-event on large
	// artifacts — scanner.Scan() returned false, scanner.Err() was never
	// checked, and the terminal status event after the oversized one was
	// lost with zero errors. 8MB covers artifact payloads well above the
	// server's 4MB request cap and SSE line-splitting worst case.
	scanner.Buffer(make([]byte, 64*1024), 8<<20)
	var dataBuf strings.Builder

	for scanner.Scan() {
		line := scanner.Text()

		// Blank line = event boundary: flush accumulated data.
		if line == "" {
			if dataBuf.Len() > 0 {
				if !emitSSEData(ctx, ch, dataBuf.String()) {
					return
				}
				dataBuf.Reset()
			}
			continue
		}

		// Comment lines (starting with ":") are ignored per SSE spec.
		if strings.HasPrefix(line, ":") {
			continue
		}

		// Accumulate data lines. Other SSE fields (event:, id:, retry:)
		// are silently ignored.
		if strings.HasPrefix(line, "data: ") {
			dataBuf.WriteString(strings.TrimPrefix(line, "data: "))
			dataBuf.WriteByte('\n')
		} else if strings.HasPrefix(line, "data:") {
			// "data:" without space (also valid per spec).
			dataBuf.WriteString(strings.TrimPrefix(line, "data:"))
			dataBuf.WriteByte('\n')
		}
	}

	// #565 F: scanner errors were swallowed — a token-too-long abort looked
	// like a normal EOF. Best-effort flush of a complete trailing event,
	// then the error terminal event.
	if err := scanner.Err(); err != nil {
		if dataBuf.Len() > 0 {
			emitSSEData(ctx, ch, strings.TrimRight(dataBuf.String(), "\n"))
		}
		emitSSEFailure(ctx, ch, err)
		return
	}

	// Flush any remaining data at EOF.
	if dataBuf.Len() > 0 {
		emitSSEData(ctx, ch, strings.TrimRight(dataBuf.String(), "\n"))
	}
}

// decodeSSE reads Server-Sent Events from a reader and sends them on ch.
// Handles multi-line data fields per SSE spec: consecutive "data:" lines are
// joined with "\n" before parsing. It is decodeSSECtx without cancellation —
// context.Background() never fires, so every send is a plain blocking send,
// byte-for-byte the legacy behavior (#565 F fix included).
func decodeSSE(r io.Reader, ch chan<- JSONRPCResponse) {
	decodeSSECtx(context.Background(), r, ch)
}

// schemeNames extracts scheme type names for error messages.
func schemeNames(schemes map[string]Security) []string {
	names := make([]string, 0, len(schemes))
	for _, s := range schemes {
		names = append(names, s.Type)
	}
	return names
}

// stripKeyOnRedirect removes the custom X-API-Key header when a redirect
// crosses hosts (#1458-C) - the stdlib only strips built-in sensitive
// headers (Authorization, Cookies), custom keys followed 302s verbatim.
func stripKeyOnRedirect(req *http.Request, via []*http.Request) error {
	if len(via) > 0 && via[0].Host != req.Host {
		req.Header.Del("X-API-Key")
		// Card-declared names ride the same header path.
		for _, h := range []string{"X-Goog-Api-Key", "Api-Key", "X-Api-Key"} {
			req.Header.Del(h)
		}
	}
	if len(via) >= 10 {
		return fmt.Errorf("stopped after 10 redirects")
	}
	return nil
}
