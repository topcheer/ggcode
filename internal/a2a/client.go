package a2a

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/safego"
	"github.com/topcheer/ggcode/internal/util"
)

type TokenProvider interface {
	GetToken(ctx context.Context) (accessToken, refreshToken string, expiry time.Time, err error)
}

type Client struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
	card       atomic.Pointer[AgentCard]

	mu            sync.RWMutex
	authMethod    string
	bearerToken   string
	refreshToken  string
	tokenExpiry   time.Time
	tokenProvider TokenProvider
	apiKeyName    string
	apiKeyIn      string

	delegationOnBehalfOf string
	delegationIntent     string

	activateExtensions atomic.Pointer[[]string]
}

type ClientOption func(*Client)

func WithBearerToken(token string) ClientOption {
	return func(c *Client) {
		c.bearerToken = token
		c.authMethod = "bearer"
	}
}

func WithMTLS(tlsConfig *tls.Config) ClientOption {
	return func(c *Client) {
		transport := util.WrapTransport(&http.Transport{
			TLSClientConfig:   tlsConfig,
			ForceAttemptHTTP2: false,
		})
		transport.TLSClientConfig.InsecureSkipVerify = false
		transport.ResponseHeaderTimeout = 15 * time.Minute
		c.httpClient = &http.Client{
			Transport:     transport,
			CheckRedirect: c.checkRedirect,
		}
		c.authMethod = "mtls"
	}
}

func WithTokenProvider(p TokenProvider) ClientOption {
	return func(c *Client) {
		c.tokenProvider = p
	}
}

func WithActivateExtensions(uris []string) ClientOption {
	return func(c *Client) {
		c.SetActivateExtensions(uris)
	}
}

func (c *Client) SetActivateExtensions(uris []string) {
	c.activateExtensions.Store(&uris)
}

func (c *Client) activatedExtensions() []string {
	if p := c.activateExtensions.Load(); p != nil {
		return *p
	}
	return nil
}

func (c *Client) MissingRequiredExtensions() []string {
	card := c.Card()
	if card == nil {
		return nil
	}
	have := make(map[string]struct{})
	for _, uri := range c.activatedExtensions() {
		have[uri] = struct{}{}
	}
	var missing []string
	for _, ext := range card.DeclaredExtensions() {
		if ext.Required {
			if _, ok := have[ext.URI]; !ok {
				missing = append(missing, ext.URI)
			}
		}
	}
	return missing
}

func (c *Client) applyExtensions(req *http.Request) error {
	if missing := c.MissingRequiredExtensions(); len(missing) > 0 {
		return fmt.Errorf("remote agent requires A2A extension(s) not activated: %s", strings.Join(missing, ", "))
	}
	if uris := c.activatedExtensions(); len(uris) > 0 {
		req.Header.Set(A2AExtensionsHeader, FormatA2AExtensionsHeader(uris))
	}
	return nil
}

func NewClient(baseURL, apiKey string, opts ...ClientOption) *Client {
	client := util.NewInsecureAwareClient(15 * time.Minute)
	if tr, ok := client.Transport.(interface {
		http.RoundTripper
		Clone() *http.Transport
	}); ok {
		t2 := tr.Clone()
		t2.ResponseHeaderTimeout = 15 * time.Minute
		client.Transport = t2
		client.Timeout = 0
	}
	c := &Client{
		baseURL:    strings.TrimRight(baseURL, "/"),
		apiKey:     apiKey,
		httpClient: client,
	}
	client.CheckRedirect = c.checkRedirect
	if apiKey != "" && c.authMethod == "" {
		c.authMethod = "apiKey"
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

func (c *Client) Discover(ctx context.Context) (*AgentCard, error) {
	card, err := c.fetchCard(ctx, "/.well-known/agent-card.json")
	if err != nil {
		if !errors.Is(err, errCardWellKnownNotFound) && !errors.Is(err, errCardInvalid) {
			return nil, err
		}
		card, err = c.fetchCard(ctx, "/.well-known/agent.json")
	}
	return card, err
}

var errCardWellKnownNotFound = errors.New("a2a discover: well-known card path not served")

var errCardInvalid = errors.New("a2a discover: response is not a valid agent card")

func (c *Client) fetchCard(ctx context.Context, path string) (*AgentCard, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return nil, fmt.Errorf("a2a discover: %w", err)
	}
	req.Header.Set("Accept", acceptHeader)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("a2a discover: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusMethodNotAllowed {
		return nil, errCardWellKnownNotFound
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("a2a discover: HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, cardMaxBodyBytes))
	if err != nil {
		return nil, fmt.Errorf("a2a discover: read: %w", err)
	}

	var card AgentCard
	if err := json.Unmarshal(body, &card); err != nil {
		return nil, fmt.Errorf("a2a discover: decode: %w", err)
	}
	if card.Name == "" && card.URL == "" {
		return nil, errCardInvalid
	}

	if len(card.Signatures) > 0 {
		results, tampered := verifyAgentCardSignature(ctx, body, fetchJWKS)
		for _, r := range results {
			debug.Log("a2a.card", "signature %d (alg=%s kid=%s): %s%v", r.Index, r.Alg, r.KID, r.Status, r.Err)
		}
		if tampered {
			return nil, &CardTamperedError{Index: 0, Alg: results[0].Alg}
		}
	}

	c.card.Store(&card)
	return &card, nil
}

func (c *Client) Card() *AgentCard { return c.card.Load() }

func (c *Client) NegotiateAuth() error {
	card := c.card.Load()
	if card == nil {
		return fmt.Errorf("a2a: NegotiateAuth called before Discover")
	}

	if len(card.Security) == 0 && len(card.SecuritySchemes) == 0 {
		c.mu.Lock()
		c.authMethod = ""
		c.mu.Unlock()
		return nil
	}

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
					c.apiKeyName = scheme.Name
					c.apiKeyIn = scheme.Location
					if c.apiKeyName == "" {
						c.apiKeyName = "X-API-Key"
					}
					if c.apiKeyIn == "" {
						c.apiKeyIn = "header"
					}
					if c.apiKeyIn == "query" {
						c.mu.Unlock()
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

func (c *Client) cardDeclaresAPIKey() bool {
	for _, scheme := range c.card.Load().SecuritySchemes {
		if scheme.Type == "apiKey" {
			return true
		}
	}
	return false
}

func (c *Client) tryBearerToken() bool {
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

	if provider != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()

		accessToken, refreshToken, newExpiry, err := provider.GetToken(ctx)
		if err != nil {
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

func (c *Client) SetBearerToken(token string) {
	c.mu.Lock()
	c.bearerToken = token
	c.authMethod = "bearer"
	c.mu.Unlock()
}

func (c *Client) SetAPIKey(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.apiKey = key
	if c.authMethod == "" {
		c.authMethod = "apiKey"
	}
}

func (c *Client) AuthMethod() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.authMethod
}

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
	}
}

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
	c.injectDelegationMeta(&params.Message)

	var result Task
	if err := c.rpc(ctx, "message/send", params, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *Client) SendMessageWithConfig(ctx context.Context, params SendMessageParams) (*Task, error) {
	c.injectDelegationMeta(&params.Message)
	var result Task
	if err := c.rpc(ctx, "message/send", params, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *Client) SendMessageStream(ctx context.Context, skill, text string) (<-chan JSONRPCResponse, error) {
	params := SendMessageParams{
		Message: Message{
			Role:      "user",
			MessageID: generateID(),
			Parts:     []Part{{Kind: "text", Text: text}},
		},
		Skill: skill,
	}
	c.injectDelegationMeta(&params.Message)

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
	req.Header.Set("Content-Type", c.requestContentType())
	req.Header.Set("Accept", acceptHeader)
	if err := c.applyExtensions(req); err != nil {
		return nil, fmt.Errorf("a2a stream: %w", err)
	}
	c.setAuth(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("a2a stream: http request: %w", err)
	}

	if ct := resp.Header.Get("Content-Type"); isJSONMedia(ct) {
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
	safego.Go("a2a.client.streamRead", func() {
		defer close(ch)
		defer resp.Body.Close()
		decodeSSECtx(ctx, resp.Body, ch)
	})

	return ch, nil
}

func (c *Client) GetTask(ctx context.Context, taskID string) (*Task, error) {
	params := GetTaskParams{ID: taskID}
	var result Task
	if err := c.rpc(ctx, "tasks/get", params, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

type ListTasksResult struct {
	Tasks     []Task `json:"tasks"`
	NextToken string `json:"nextToken,omitempty"`
}

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

func (c *Client) CancelTask(ctx context.Context, taskID string) (*Task, error) {
	params := CancelTaskParams{ID: taskID}
	var result Task
	if err := c.rpc(ctx, "tasks/cancel", params, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *Client) GetExtendedAgentCard(ctx context.Context) (json.RawMessage, error) {
	var result json.RawMessage
	if err := c.rpc(ctx, "agent/getExtendedCard", struct{}{}, &result); err != nil {
		return nil, err
	}
	if hasCardSignatures(result) {
		_, tampered := verifyAgentCardSignature(ctx, result, fetchJWKS)
		if tampered {
			return nil, fmt.Errorf("a2a: extended agent card signature does not verify - card may be tampered")
		}
	}
	return result, nil
}

func (c *Client) SetPushConfig(ctx context.Context, cfg PushNotificationConfig) (*PushNotificationConfig, error) {
	var result PushNotificationConfig
	if err := c.rpc(ctx, "tasks/pushNotificationConfig/set", cfg, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *Client) GetPushConfig(ctx context.Context, id string) (*PushNotificationConfig, error) {
	var result PushNotificationConfig
	if err := c.rpc(ctx, "tasks/pushNotificationConfig/get", map[string]string{"id": id}, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *Client) ListPushConfigs(ctx context.Context) ([]PushNotificationConfig, error) {
	var result []PushNotificationConfig
	if err := c.rpc(ctx, "tasks/pushNotificationConfig/list", struct{}{}, &result); err != nil {
		return nil, err
	}
	return result, nil
}

func (c *Client) DeletePushConfig(ctx context.Context, id string) error {
	return c.rpc(ctx, "tasks/pushNotificationConfig/delete", map[string]string{"id": id}, nil)
}

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
	req.Header.Set("Content-Type", c.requestContentType())
	req.Header.Set("Accept", acceptHeader)
	if err := c.applyExtensions(req); err != nil {
		return nil, fmt.Errorf("a2a resubscribe: %w", err)
	}
	c.setAuth(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("a2a resubscribe: http request: %w", err)
	}

	ct := resp.Header.Get("Content-Type")
	if isJSONMedia(ct) {
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
	safego.Go("a2a.client.resubscribeRead", func() {
		defer close(ch)
		defer resp.Body.Close()
		decodeSSECtx(ctx, resp.Body, ch)
	})

	return ch, nil
}

const acceptHeader = "application/a2a+json, application/json"

func (c *Client) requestContentType() string {
	if card := c.Card(); card != nil && strings.HasPrefix(card.ProtocolReversion, "1") {
		return "application/a2a+json"
	}
	return "application/json"
}

func isJSONMedia(ct string) bool {
	ct = strings.ToLower(ct)
	return strings.Contains(ct, "application/json") || strings.Contains(ct, "a2a+json")
}

const rpcMaxAttempts = 3

func isRetryableHTTPStatus(code int) bool {
	return code == http.StatusTooManyRequests || (code >= 500 && code != http.StatusNotImplemented)
}

func retryRPCLater(ctx context.Context, attempt int, backoff *time.Duration, method string) bool {
	if attempt >= rpcMaxAttempts {
		return false
	}
	wait := time.Duration(rand.Int63n(int64(*backoff) + 1))
	*backoff *= 2
	debug.Log("a2a", "rpc %s: transient failure, retry %d/%d in %v", method, attempt, rpcMaxAttempts-1, wait)
	select {
	case <-ctx.Done():
		return false
	case <-time.After(wait):
		return true
	}
}

func (c *Client) rpc(ctx context.Context, method string, params interface{}, result interface{}) error {
	pb := peerBreakerFor(c.baseURL)
	if pb != nil && !pb.allow() {
		return &peerCircuitOpenError{Peer: c.baseURL, Method: method, Cooldown: peerBreakerCooldown}
	}
	err := c.rpcCore(ctx, method, params, result)
	if pb != nil {
		pb.record(classifyRPCError(err))
	}
	return err
}

func (c *Client) rpcCore(ctx context.Context, method string, params interface{}, result interface{}) error {
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

	backoff := 100 * time.Millisecond
	for attempt := 1; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL, bytes.NewReader(body))
		if err != nil {
			return fmt.Errorf("a2a %s: %w", method, err)
		}
		req.Header.Set("Content-Type", c.requestContentType())
		req.Header.Set("Accept", acceptHeader)
		if err := c.applyExtensions(req); err != nil {
			return fmt.Errorf("a2a %s: %w", method, err)
		}
		c.setAuth(req)

		resp, err := c.httpClient.Do(req)
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return fmt.Errorf("a2a %s: %w", method, err)
			}
			if retryRPCLater(ctx, attempt, &backoff, method) {
				continue
			}
			return fmt.Errorf("a2a %s: %w", method, err)
		}

		respBody, err := util.ReadAll(resp.Body, util.ReadLimitGeneral)
		resp.Body.Close()
		if err != nil {
			if retryRPCLater(ctx, attempt, &backoff, method) {
				continue
			}
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
			if isRetryableHTTPStatus(resp.StatusCode) && retryRPCLater(ctx, attempt, &backoff, method) {
				continue
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
}

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

func decodeSSECtx(ctx context.Context, r io.Reader, ch chan<- JSONRPCResponse) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 8<<20)
	var dataBuf strings.Builder

	for scanner.Scan() {
		line := scanner.Text()

		if line == "" {
			if dataBuf.Len() > 0 {
				if !emitSSEData(ctx, ch, dataBuf.String()) {
					return
				}
				dataBuf.Reset()
			}
			continue
		}

		if strings.HasPrefix(line, ":") {
			continue
		}

		if strings.HasPrefix(line, "data: ") {
			dataBuf.WriteString(strings.TrimPrefix(line, "data: "))
			dataBuf.WriteByte('\n')
		} else if strings.HasPrefix(line, "data:") {
			dataBuf.WriteString(strings.TrimPrefix(line, "data:"))
			dataBuf.WriteByte('\n')
		}
	}

	if err := scanner.Err(); err != nil {
		if dataBuf.Len() > 0 {
			emitSSEData(ctx, ch, strings.TrimRight(dataBuf.String(), "\n"))
		}
		emitSSEFailure(ctx, ch, err)
		return
	}

	if dataBuf.Len() > 0 {
		emitSSEData(ctx, ch, strings.TrimRight(dataBuf.String(), "\n"))
	}
}

func decodeSSE(r io.Reader, ch chan<- JSONRPCResponse) {
	decodeSSECtx(context.Background(), r, ch)
}

func schemeNames(schemes map[string]Security) []string {
	names := make([]string, 0, len(schemes))
	for _, s := range schemes {
		names = append(names, s.Type)
	}
	return names
}

func (c *Client) checkRedirect(req *http.Request, via []*http.Request) error {
	if len(via) > 0 && via[0].Host != req.Host {
		c.mu.RLock()
		name := c.apiKeyName
		c.mu.RUnlock()
		if name != "" {
			req.Header.Del(name)
		}
		req.Header.Del("X-API-Key")
		for _, h := range []string{"X-Goog-Api-Key", "Api-Key", "X-Api-Key"} {
			req.Header.Del(h)
		}
	}
	if len(via) >= 10 {
		return fmt.Errorf("stopped after 10 redirects")
	}
	return nil
}
