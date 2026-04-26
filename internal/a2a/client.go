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
	"time"

	"github.com/topcheer/ggcode/internal/safego"
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
	card       *AgentCard // cached agent card (nil until discovered)

	// Negotiated auth state (populated after Discover or explicit config)
	authMethod    string // "", "apiKey", "bearer", "mtls"
	bearerToken   string // cached OAuth2 access token
	refreshToken  string // for token refresh
	tokenExpiry   time.Time
	tokenProvider TokenProvider // auto-acquire token when needed
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
		c.httpClient = &http.Client{
			Timeout: 15 * time.Minute,
			Transport: &http.Transport{
				TLSClientConfig: tlsConfig,
			},
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
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		httpClient: &http.Client{
			Timeout: 15 * time.Minute,
		},
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

	c.card = &card
	return &card, nil
}

// Card returns the cached agent card (nil if Discover hasn't been called).
func (c *Client) Card() *AgentCard { return c.card }

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
	if c.card == nil {
		return fmt.Errorf("a2a: NegotiateAuth called before Discover")
	}

	// No security requirements → nothing to do
	if len(c.card.Security) == 0 && len(c.card.SecuritySchemes) == 0 {
		c.authMethod = ""
		return nil
	}

	// Try to match client capabilities with server requirements
	for _, req := range c.card.Security {
		for schemeName := range req {
			scheme, ok := c.card.SecuritySchemes[schemeName]
			if !ok {
				continue
			}

			switch scheme.Type {
			case "apiKey":
				if c.apiKey != "" {
					c.authMethod = "apiKey"
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
				if c.authMethod == "mtls" {
					return nil
				}
			}
		}
	}

	// Also check if client already has a configured auth that might work
	if c.authMethod != "" {
		return nil
	}

	return fmt.Errorf("a2a: server requires authentication but client has no matching credential (schemes: %v)",
		schemeNames(c.card.SecuritySchemes))
}

// tryBearerToken checks if we have a valid bearer token, or tries to obtain one.
func (c *Client) tryBearerToken() bool {
	// Already have a non-expired token
	if c.bearerToken != "" && c.tokenExpiry.IsZero() || time.Now().Before(c.tokenExpiry) {
		c.authMethod = "bearer"
		return true
	}

	// Try to obtain a token via the configured provider
	if c.tokenProvider != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()

		accessToken, refreshToken, expiry, err := c.tokenProvider.GetToken(ctx)
		if err == nil && accessToken != "" {
			c.bearerToken = accessToken
			c.refreshToken = refreshToken
			c.tokenExpiry = expiry
			c.authMethod = "bearer"
			return true
		}
	}

	return false
}

// SetBearerToken updates the bearer token (e.g., after OAuth2 token refresh).
func (c *Client) SetBearerToken(token string) {
	c.bearerToken = token
	c.authMethod = "bearer"
}

// SetAPIKey updates the API key.
func (c *Client) SetAPIKey(key string) {
	c.apiKey = key
	if c.authMethod == "" {
		c.authMethod = "apiKey"
	}
}

// AuthMethod returns the negotiated authentication method.
func (c *Client) AuthMethod() string { return c.authMethod }

// setAuth applies the negotiated auth to an outgoing HTTP request.
func (c *Client) setAuth(req *http.Request) {
	switch c.authMethod {
	case "apiKey":
		req.Header.Set("X-API-Key", c.apiKey)
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

	paramsJSON, _ := json.Marshal(params)
	rpcReq := JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`1`),
		Method:  "message/stream",
		Params:  paramsJSON,
	}
	body, _ := json.Marshal(rpcReq)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	c.setAuth(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("a2a stream: HTTP %d", resp.StatusCode)
	}

	ch := make(chan JSONRPCResponse, 32)
	safego.Go("a2a.client.streamRead", func() {
		defer close(ch)
		defer resp.Body.Close()
		decodeSSE(resp.Body, ch)
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
	paramsJSON, _ := json.Marshal(params)
	rpcReq := JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`1`),
		Method:  "tasks/resubscribe",
		Params:  paramsJSON,
	}
	body, _ := json.Marshal(rpcReq)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	c.setAuth(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}

	// Check Content-Type: if JSON (not SSE), this is a sync error response.
	ct := resp.Header.Get("Content-Type")
	if strings.Contains(ct, "application/json") {
		defer resp.Body.Close()
		respBody, _ := io.ReadAll(resp.Body)
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
	safego.Go("a2a.client.resubscribeRead", func() {
		defer close(ch)
		defer resp.Body.Close()
		decodeSSE(resp.Body, ch)
	})

	return ch, nil
}

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

func (c *Client) rpc(ctx context.Context, method string, params interface{}, result interface{}) error {
	paramsJSON, _ := json.Marshal(params)
	rpcReq := JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`1`),
		Method:  method,
		Params:  paramsJSON,
	}
	body, _ := json.Marshal(rpcReq)

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

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("a2a %s: read: %w", method, err)
	}

	var rpcResp JSONRPCResponse
	if err := json.Unmarshal(respBody, &rpcResp); err != nil {
		return fmt.Errorf("a2a %s: decode: %w", method, err)
	}

	if rpcResp.Error != nil {
		return rpcResp.Error
	}

	if result != nil && rpcResp.Result != nil {
		resultJSON, _ := json.Marshal(rpcResp.Result)
		if err := json.Unmarshal(resultJSON, result); err != nil {
			return fmt.Errorf("a2a %s: unmarshal result: %w", method, err)
		}
	}

	return nil
}

// decodeSSE reads Server-Sent Events from a reader and sends them on ch.
// Handles multi-line data fields per SSE spec: consecutive "data:" lines are
// joined with "\n" before parsing.
func decodeSSE(r io.Reader, ch chan<- JSONRPCResponse) {
	scanner := bufio.NewScanner(r)
	var dataBuf strings.Builder

	for scanner.Scan() {
		line := scanner.Text()

		// Blank line = event boundary. Flush accumulated data.
		if line == "" {
			if dataBuf.Len() > 0 {
				data := dataBuf.String()
				dataBuf.Reset()
				var resp JSONRPCResponse
				if json.Unmarshal([]byte(data), &resp) == nil {
					ch <- resp
				}
			}
			continue
		}

		// Comment lines (starting with ":") are ignored per SSE spec.
		if strings.HasPrefix(line, ":") {
			continue
		}

		// Accumulate data lines.
		if strings.HasPrefix(line, "data: ") {
			dataBuf.WriteString(strings.TrimPrefix(line, "data: "))
			dataBuf.WriteByte('\n')
		} else if strings.HasPrefix(line, "data:") {
			// "data:" without space (also valid per spec).
			dataBuf.WriteString(strings.TrimPrefix(line, "data:"))
			dataBuf.WriteByte('\n')
		}
		// Other SSE fields (event:, id:, retry:) are silently ignored.
	}

	// Flush any remaining data at EOF.
	if dataBuf.Len() > 0 {
		data := strings.TrimRight(dataBuf.String(), "\n")
		var resp JSONRPCResponse
		if json.Unmarshal([]byte(data), &resp) == nil {
			ch <- resp
		}
	}
}

// schemeNames extracts scheme type names for error messages.
func schemeNames(schemes map[string]Security) []string {
	names := make([]string, 0, len(schemes))
	for _, s := range schemes {
		names = append(names, s.Type)
	}
	return names
}
