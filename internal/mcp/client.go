package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"

	"github.com/topcheer/ggcode/internal/auth"
	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/safego"
	"github.com/topcheer/ggcode/internal/util"
)

type Client struct {
	skipOSVCheck bool
	name         string
	transport    string
	command      string
	args         []string
	env          map[string]string
	url          string
	headers      map[string]string

	toolSchemas map[string]json.RawMessage

	resMu      sync.Mutex
	cmd        *exec.Cmd
	procCancel context.CancelFunc
	stdin      io.WriteCloser
	stdout     io.Reader
	reader     *bufio.Reader
	httpClient *http.Client
	wsMu       sync.Mutex
	readMu     sync.Mutex
	waiters    map[string]chan *Response
	wsConn     *websocket.Conn
	sessionID  string

	httpNotifLastEventID string
	negotiatedVersion    string
	instructions         string

	stateless        bool
	modernVersion    string
	modernServerInfo *Implementation
	legacyServerInfo Implementation

	taskPollOverride time.Duration
	mu               sync.Mutex
	stderrMu         sync.RWMutex
	stderrBuf        strings.Builder
	abortOnce        sync.Once
	nextID           atomic.Int64
	closed           atomic.Bool

	httpNotifDisabled atomic.Bool

	notifStreamCancel context.CancelFunc
	notifStreamGen    uint64

	lastReadProgress  atomic.Int64
	hangWatchdogArmed atomic.Bool

	hangAbort    atomic.Bool
	oauthHandler *OAuthHandler

	oauthProbed atomic.Bool

	processExit  chan struct{}
	procWaitDone chan struct{}

	notificationHandler func(method string, params json.RawMessage)

	notificationCh   chan *Notification
	notificationOnce sync.Once
	notificationDone chan struct{}

	samplingHandler SamplingHandler

	pendingURLElicitations map[string]struct{}
	elicitationHandler     ElicitationHandler

	serverCaps ServerCaps

	serverReqOnce sync.Once
	serverReqSem  chan struct{}

	listingCache listingCache

	subMu          sync.Mutex
	subs           map[string]*Subscription
	modernSub      *Subscription
	subListenState atomic.Int32
}

func (c *Client) negotiatedState() (string, ServerCaps) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.negotiatedVersion, c.serverCaps
}

func (c *Client) setNegotiatedState(version string, caps ServerCaps) {
	c.mu.Lock()
	c.negotiatedVersion = version
	c.serverCaps = caps
	c.mu.Unlock()

	c.listingCache.clear()
}

func NewClient(name, command string, args []string) *Client {
	return &Client{
		name:             name,
		transport:        "stdio",
		command:          command,
		args:             args,
		notificationCh:   make(chan *Notification, notificationChanSize),
		notificationDone: make(chan struct{}),
	}
}

func NewClientFromConfig(cfg config.MCPServerConfig) *Client {
	transport := strings.ToLower(strings.TrimSpace(cfg.Type))
	if transport == "" {

		if cfg.Command == "" && strings.TrimSpace(cfg.URL) != "" {
			transport = "http"
			debug.Log("mcp-client", "server %s: no type and no command; inferring http from url", cfg.Name)
		} else {
			transport = "stdio"
		}
	}

	if transport == "sse" {
		debug.Log("mcp-client", "server %s: normalizing sse transport to http", cfg.Name)
		transport = "http"
	}
	client := &Client{
		name:             cfg.Name,
		transport:        transport,
		skipOSVCheck:     cfg.SkipOSVCheck,
		command:          cfg.Command,
		args:             append([]string(nil), cfg.Args...),
		env:              cloneStringMap(cfg.Env),
		url:              cfg.URL,
		headers:          cloneStringMap(cfg.Headers),
		notificationCh:   make(chan *Notification, notificationChanSize),
		notificationDone: make(chan struct{}),
	}
	if transport == "http" {
		client.oauthHandler = NewOAuthHandler(cfg.Name, cfg.URL, auth.DefaultStore())
		if cfg.OAuthClientID != "" {
			client.oauthHandler.SetClientCredentials(cfg.OAuthClientID, cfg.OAuthClientSecret)
		}
	}

	if cfg.Stateless {
		client.EnableStateless()
	}
	return client
}

func (c *Client) Start(ctx context.Context) error {
	switch c.transport {
	case "http", "sse":

		c.httpClient = newMCPHTTPClient(0)
		return nil
	case "ws", "websocket":
		headers := http.Header{}
		for key, value := range c.headers {
			headers.Set(key, value)
		}
		dialer := *websocket.DefaultDialer
		dialer.Proxy = util.SmartProxyFunc()
		conn, _, err := dialer.DialContext(ctx, c.url, headers)
		if err != nil {
			return fmt.Errorf("mcp[%s]: websocket dial: %w", c.name, err)
		}
		c.setTransportWSConn(conn)
		return nil
	case "", "stdio":
	default:
		return fmt.Errorf("mcp[%s]: unsupported transport %q", c.name, c.transport)
	}

	if err := osvGate(ctx, c.name, c.command, c.args, c.skipOSVCheck); err != nil {
		return err
	}

	procCtx, cancelProc := context.WithCancel(context.Background())
	cmd := exec.CommandContext(procCtx, c.command, c.args...)
	configureMCPCommandProcess(cmd)
	if len(c.env) > 0 {
		cmd.Env = append(os.Environ(), flattenEnvMap(c.env)...)
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancelProc()
		return fmt.Errorf("mcp[%s]: stdin pipe: %w", c.name, err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancelProc()
		return fmt.Errorf("mcp[%s]: stdout pipe: %w", c.name, err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		cancelProc()
		return fmt.Errorf("mcp[%s]: stderr pipe: %w", c.name, err)
	}

	stderrDone := make(chan struct{})
	safego.Go("mcp.captureStderr", func() {
		defer close(stderrDone)
		c.captureStderr(stderr)
	})

	if err := cmd.Start(); err != nil {
		cancelProc()
		return fmt.Errorf("mcp[%s]: starting server: %w", c.name, err)
	}

	c.mu.Lock()
	c.setTransportProcess(cmd, cancelProc, stdin)
	c.stdout = stdout
	c.reader = bufio.NewReader(stdout)
	c.processExit = make(chan struct{})
	c.procWaitDone = make(chan struct{})
	c.mu.Unlock()

	safego.Go("mcp.procWatch", func() {
		<-stderrDone
		_ = cmd.Wait()

		close(c.procWaitDone)

		if !c.closed.Load() || c.hangAbort.Load() {
			c.closed.Store(true)
			close(c.processExit)
			debug.Log("mcp-procwatch", "server=%s process exited (watchdog=%v)", c.name, c.hangAbort.Load())
		}
	})

	return nil
}

const latestMCPProtocolVersion = "2025-11-25"

var knownMCPProtocolVersions = map[string]bool{
	"2024-11-05": true,
	"2025-03-26": true,
	"2025-06-18": true,
	"2025-11-25": true,
}

func (c *Client) NegotiatedVersion() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.negotiatedVersion
}

func (c *Client) Instructions() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.instructions
}

func (c *Client) Initialize(ctx context.Context) (*InitializeResult, error) {
	if c.statelessWanted() {
		result, probeErr := c.probeModern(ctx)
		if probeErr == nil {

			c.startHTTPNotificationStream()
			return result, nil
		}

		var uerr *UnsupportedProtocolVersionError
		if errors.As(probeErr, &uerr) {
			return nil, probeErr
		}

		debug.Log("mcp-client", "server=%s stateless probe failed, falling back to initialize handshake: %v", c.name, probeErr)
	}
	caps := c.clientCaps()
	params := InitializeParams{
		ProtocolVersion: latestMCPProtocolVersion,
		Capabilities:    caps,
		ClientInfo:      clientMetaName(),
	}
	var result InitializeResult
	if err := c.sendRequest(ctx, "initialize", params, &result); err != nil {

		if je, ok := jsonRPCErrorOf(err); ok && isUnsupportedProtocolVersionCode(je.Code) {
			debug.Log("mcp-client", "server=%s initialize rejected with UnsupportedProtocolVersion: %v", c.name, err)
			return nil, unsupportedProtocolVersionError(c.name, err)
		}
		return nil, fmt.Errorf("mcp[%s]: initialize: %w", c.name, err)
	}

	c.mu.Lock()
	c.instructions = result.Instructions
	c.mu.Unlock()

	serverVersion := result.ProtocolVersion
	if serverVersion == "" {
		return nil, fmt.Errorf("mcp[%s]: server returned empty protocolVersion", c.name)
	}
	if !knownMCPProtocolVersions[serverVersion] {
		return nil, fmt.Errorf("mcp[%s]: unsupported protocol version %q (client supports %s)",
			c.name, serverVersion, latestMCPProtocolVersion)
	}

	c.setNegotiatedState(serverVersion, result.Capabilities)

	c.mu.Lock()
	c.legacyServerInfo = result.ServerInfo
	c.mu.Unlock()

	notif := Notification{
		JSONRPC: "2.0",
		Method:  "notifications/initialized",
	}
	if err := c.sendNotification(ctx, notif); err != nil {

		debug.Log("mcp-client", "server=%s initialized notification send failed (handshake complete): %v", c.name, err)
	}

	c.startHTTPNotificationStream()

	return &result, nil
}

const maxPaginationPages = 100

func (c *Client) ListTools(ctx context.Context) ([]ToolDefinition, error) {

	if c.closed.Load() {
		return nil, fmt.Errorf("mcp[%s]: connection closed", c.name)
	}
	_, caps := c.negotiatedState()
	if caps.Tools == nil {
		debug.Log("mcp-client", "server=%s tools capability not advertised; returning empty tool list", c.name)
		return []ToolDefinition{}, nil
	}

	if v, ok := c.listingCache.get(cacheTools, ""); ok {
		return cloneCachedSlice(v.([]ToolDefinition)), nil
	}

	fetchEpoch := c.listingCache.epoch(cacheTools)
	var all []ToolDefinition
	var pages []CacheableResult
	cursor := ""
	for page := 0; page < maxPaginationPages; page++ {
		var result ListToolsResult
		if err := c.sendRequest(ctx, "tools/list", ListToolsParams{Cursor: cursor}, &result); err != nil {
			if len(all) > 0 {

				debug.Log("mcp-client", "server=%s tools/list page %d failed after %d tools: %v", c.name, page+1, len(all), err)
				return all, nil
			}
			return nil, fmt.Errorf("mcp[%s]: tools/list: %w", c.name, err)
		}
		all = append(all, result.Tools...)
		pages = append(pages, result.CacheableResult)
		if result.NextCursor == "" {
			c.storeListingsCache(cacheTools, "", all, pages, fetchEpoch)
			return c.finalizeListedTools(all), nil
		}
		cursor = result.NextCursor
	}
	debug.Log("mcp-client", "server=%s tools/list exceeded %d pages; stopping pagination", c.name, maxPaginationPages)
	return c.finalizeListedTools(all), nil
}

func (c *Client) finalizeListedTools(all []ToolDefinition) []ToolDefinition {
	if c.transport == "http" {
		kept := make([]ToolDefinition, 0, len(all))
		for _, td := range all {
			if v := headerAnnotationViolations(td.InputSchema); len(v) > 0 {
				debug.Log("mcp-http", "server=%s excluded tool %q from tools/list: %s", c.name, td.Name, strings.Join(v, "; "))
				continue
			}
			kept = append(kept, td)
		}
		all = kept
	}
	schemas := make(map[string]json.RawMessage, len(all))
	for _, td := range all {
		schemas[td.Name] = td.InputSchema
	}
	c.mu.Lock()
	c.toolSchemas = schemas
	c.mu.Unlock()
	return all
}

func (c *Client) ListPrompts(ctx context.Context) ([]PromptDefinition, error) {

	if v, ok := c.listingCache.get(cachePrompts, ""); ok {
		return cloneCachedSlice(v.([]PromptDefinition)), nil
	}
	fetchEpoch := c.listingCache.epoch(cachePrompts)
	var all []PromptDefinition
	var pages []CacheableResult
	cursor := ""
	for page := 0; page < maxPaginationPages; page++ {
		params := struct {
			Cursor string `json:"cursor,omitempty"`
		}{Cursor: cursor}
		var result ListPromptsResult
		if err := c.sendRequest(ctx, "prompts/list", params, &result); err != nil {
			if len(all) > 0 {
				debug.Log("mcp-client", "server=%s prompts/list page %d failed after %d prompts: %v", c.name, page+1, len(all), err)
				return all, nil
			}
			return nil, fmt.Errorf("mcp[%s]: prompts/list: %w", c.name, err)
		}
		all = append(all, result.Prompts...)
		pages = append(pages, result.CacheableResult)
		if result.NextCursor == "" {
			c.storeListingsCache(cachePrompts, "", all, pages, fetchEpoch)
			return all, nil
		}
		cursor = result.NextCursor
	}
	debug.Log("mcp-client", "server=%s prompts/list exceeded %d pages; stopping pagination", c.name, maxPaginationPages)
	return all, nil
}

func (c *Client) ListResources(ctx context.Context) ([]ResourceDefinition, error) {

	if v, ok := c.listingCache.get(cacheResources, ""); ok {
		return cloneCachedSlice(v.([]ResourceDefinition)), nil
	}
	fetchEpoch := c.listingCache.epoch(cacheResources)
	var all []ResourceDefinition
	var pages []CacheableResult
	cursor := ""
	for page := 0; page < maxPaginationPages; page++ {
		params := struct {
			Cursor string `json:"cursor,omitempty"`
		}{Cursor: cursor}
		var result ListResourcesResult
		if err := c.sendRequest(ctx, "resources/list", params, &result); err != nil {
			if len(all) > 0 {
				debug.Log("mcp-client", "server=%s resources/list page %d failed after %d resources: %v", c.name, page+1, len(all), err)
				return all, nil
			}
			return nil, fmt.Errorf("mcp[%s]: resources/list: %w", c.name, err)
		}
		all = append(all, result.Resources...)
		pages = append(pages, result.CacheableResult)
		if result.NextCursor == "" {
			c.storeListingsCache(cacheResources, "", all, pages, fetchEpoch)
			return all, nil
		}
		cursor = result.NextCursor
	}
	debug.Log("mcp-client", "server=%s resources/list exceeded %d pages; stopping pagination", c.name, maxPaginationPages)
	return all, nil
}

func (c *Client) GetPrompt(ctx context.Context, name string, args map[string]interface{}) (*GetPromptResult, error) {
	params := GetPromptParams{
		Name:      name,
		Arguments: args,
	}
	var result GetPromptResult
	if err := c.callWithMRTR(ctx, "prompts/get", &params, &result); err != nil {
		return nil, fmt.Errorf("mcp[%s]: prompts/get: %w", c.name, err)
	}
	return &result, nil
}

func (c *Client) ReadResource(ctx context.Context, uri string) (*ReadResourceResult, error) {

	if v, ok := c.listingCache.get(cacheResourceRead, uri); ok {
		res := v.(ReadResourceResult)

		res.Contents = cloneCachedSlice(res.Contents)
		return &res, nil
	}

	fetchEpoch := c.listingCache.epoch(cacheResourceRead)
	params := ReadResourceParams{URI: uri}
	var result ReadResourceResult
	if err := c.callWithMRTR(ctx, "resources/read", &params, &result); err != nil {

		return nil, fmt.Errorf("mcp[%s]: resources/read: %w", c.name, annotateResourceReadError(err, uri))
	}

	cached := result
	cached.Contents = cloneCachedSlice(result.Contents)
	c.storeListingsCache(cacheResourceRead, uri, cached, []CacheableResult{result.CacheableResult}, fetchEpoch)
	return &result, nil
}

func (c *Client) SubscribeResource(ctx context.Context, uri string) error {
	if !c.HasResourceSubscribe() {
		return fmt.Errorf("mcp[%s]: server does not advertise resources.subscribe", c.name)
	}
	params := ReadResourceParams{URI: uri}
	var result struct{}
	if err := c.sendRequest(ctx, "resources/subscribe", params, &result); err != nil {
		return fmt.Errorf("mcp[%s]: resources/subscribe: %w", c.name, err)
	}
	return nil
}

func (c *Client) UnsubscribeResource(ctx context.Context, uri string) error {
	if !c.HasResourceSubscribe() {
		return fmt.Errorf("mcp[%s]: server does not advertise resources.subscribe", c.name)
	}
	params := ReadResourceParams{URI: uri}
	var result struct{}
	if err := c.sendRequest(ctx, "resources/unsubscribe", params, &result); err != nil {
		return fmt.Errorf("mcp[%s]: resources/unsubscribe: %w", c.name, err)
	}
	return nil
}

func (c *Client) CallTool(ctx context.Context, name string, args map[string]interface{}) (*CallToolResult, error) {
	params := CallToolParams{
		Name:      name,
		Arguments: args,
	}
	var result CallToolResult
	if err := c.callWithMRTR(ctx, "tools/call", &params, &result); err != nil {
		return nil, fmt.Errorf("mcp[%s]: tools/call %s: %w", c.name, name, err)
	}
	return &result, nil
}

func (c *Client) Close() error {

	c.Abort()

	c.mu.Lock()
	cmd := c.snapshotTransport().cmd
	transport := c.transport
	c.sessionID = ""
	c.httpClient = nil
	c.setTransportProcess(nil, nil, nil)
	notifCancel := c.notifStreamCancel
	waitDone := c.procWaitDone
	oauthHandler := c.oauthHandler
	c.oauthHandler = nil

	c.pendingURLElicitations = nil
	c.mu.Unlock()

	if notifCancel != nil {
		notifCancel()
	}

	if oauthHandler != nil {
		oauthHandler.Close()
	}
	if (transport == "stdio" || transport == "") && cmd != nil {

		if waitDone != nil {
			select {
			case <-waitDone:
			case <-time.After(3 * time.Second):
			}
		}
	}
	return nil
}

func (c *Client) ForceReauth() error {
	c.mu.Lock()
	handler := c.oauthHandler
	c.mu.Unlock()
	if handler == nil {
		return nil
	}
	return handler.DeleteServerToken()
}

type transportResources struct {
	wsConn     *websocket.Conn
	stdin      io.WriteCloser
	cmd        *exec.Cmd
	procCancel context.CancelFunc
}

func (c *Client) snapshotTransport() transportResources {
	c.resMu.Lock()
	defer c.resMu.Unlock()
	return transportResources{wsConn: c.wsConn, stdin: c.stdin, cmd: c.cmd, procCancel: c.procCancel}
}

func (c *Client) setTransportProcess(cmd *exec.Cmd, cancel context.CancelFunc, stdin io.WriteCloser) {
	c.resMu.Lock()
	c.cmd = cmd
	c.procCancel = cancel
	c.stdin = stdin
	c.resMu.Unlock()
}

func (c *Client) setTransportWSConn(conn *websocket.Conn) {
	c.resMu.Lock()
	c.wsConn = conn
	c.resMu.Unlock()
}

func (c *Client) Abort() {
	c.abortOnce.Do(func() {

		c.closed.Store(true)

		tr := c.snapshotTransport()
		wsConn := tr.wsConn
		stdin := tr.stdin
		cmd := tr.cmd
		procCancel := tr.procCancel

		if wsConn != nil {
			_ = wsConn.Close()
		}
		if stdin != nil {
			_ = stdin.Close()
		}
		if procCancel != nil {
			procCancel()
		}
		if cmd != nil && cmd.Process != nil {

			killProcessGroup(cmd)
		}

		if c.notificationDone != nil {
			close(c.notificationDone)
		}

		c.closeAllSubscriptions(fmt.Errorf("mcp[%s]: connection closed", c.name))
	})
}

func (c *Client) Name() string { return c.name }

func (c *Client) ProcessExit() <-chan struct{} {
	return c.processExit
}

func (c *Client) IsClosed() bool {
	return c.closed.Load()
}

func (c *Client) nextRequestID() *ID {
	id := c.nextID.Add(1)
	i := NewIntID(id)
	return &i
}

const mcpRequestTimeout = 120 * time.Second

const maxHeaderContentLength = 16 << 20

const maxNDJSONLineLength = 16 << 20

func readBoundedLine(r *bufio.Reader, max int) ([]byte, error) {
	var buf []byte
	for {
		chunk, err := r.ReadSlice('\n')
		buf = append(buf, chunk...)
		if len(buf) > max {
			return nil, fmt.Errorf("line exceeds %d bytes (no newline within limit)", max)
		}
		if err == bufio.ErrBufferFull {
			continue
		}
		if err != nil {
			return nil, err
		}
		return buf, nil
	}
}

func (c *Client) sendRequest(ctx context.Context, method string, params interface{}, result interface{}) error {
	if c.closed.Load() {
		return fmt.Errorf("mcp[%s]: connection closed", c.name)
	}
	err := c.sendRequestOnce(ctx, method, params, result)

	if uerr, ok := asUnsupportedProtocolVersion(err); ok && method != "initialize" && method != "server/discover" {
		if nv := selectModernVersion(uerr.Supported); nv != "" && c.setModernVersionIfChanged(nv) {
			debug.Log("mcp-client", "server=%s switching to protocol version %s after UnsupportedProtocolVersionError", c.name, nv)
			retryErr := c.sendRequestOnce(ctx, method, params, result)
			if _, stillVersion := asUnsupportedProtocolVersion(retryErr); !stillVersion {
				return retryErr
			}

		}

		return uerr
	}
	return err
}

func (c *Client) sendRequestOnce(ctx context.Context, method string, params interface{}, result interface{}) error {

	ctx, cancel := context.WithTimeout(ctx, mcpRequestTimeout)
	defer cancel()

	paramsJSON, err := json.Marshal(params)
	if err != nil {
		return fmt.Errorf("mcp[%s]: marshal params for %s: %w", c.name, method, err)
	}

	paramsJSON = c.envelopeModernMeta(method, paramsJSON)

	req := Request{
		JSONRPC: "2.0",
		Method:  method,
		Params:  paramsJSON,
		ID:      c.nextRequestID(),
	}

	resp, err := c.sendRequestUnlocked(req, ctx)
	if err != nil {

		if req.ID != nil && method != "initialize" && ctx.Err() != nil && !c.closed.Load() {
			cause := ctx.Err()
			safego.Go("mcp.client.notifyCancelled", func() {
				c.notifyCancelled(req.ID, cause, method)
			})
		}
		return fmt.Errorf("mcp[%s]: send %s: %w", c.name, method, err)
	}

	if resp.IsError() {

		return decorateSpecError(resp.Error)
	}

	if result != nil && resp.Result != nil {
		if err := json.Unmarshal(resp.Result, result); err != nil {
			return fmt.Errorf("mcp[%s]: unmarshal result: %w", c.name, err)
		}
	}

	return nil
}

func (c *Client) sendRequestUnlocked(req Request, ctx context.Context) (*Response, error) {
	switch c.transport {
	case "ws", "websocket":
		return c.sendWSUnlocked(ctx, req)
	case "http":

		return c.sendHTTP(ctx, req)
	default:

		var waiter chan *Response
		if req.ID != nil {
			waiter = make(chan *Response, 1)
			c.registerWaiter(req.ID, waiter)
			defer c.unregisterWaiter(req.ID, waiter)
		}
		c.mu.Lock()
		if err := c.writeMessageUnlocked(req); err != nil {
			c.mu.Unlock()
			return nil, fmt.Errorf("mcp[%s]: write message: %w", c.name, err)
		}
		c.mu.Unlock()
		return c.readResponseWithWaiter(ctx, req.ID, waiter)
	}
}

type CancelledNotificationParams struct {
	RequestID *ID    `json:"requestId"`
	Reason    string `json:"reason,omitempty"`
}

func (c *Client) notifyCancelled(id *ID, cause error, method string) {
	params := CancelledNotificationParams{
		RequestID: id,
		Reason:    fmt.Sprintf("client cancelled %s: %v", method, cause),
	}
	raw, err := json.Marshal(params)
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = c.sendNotification(ctx, Notification{
		JSONRPC: "2.0",
		Method:  "notifications/cancelled",
		Params:  raw,
	})
}

func (c *Client) sendNotification(ctx context.Context, notif Notification) error {

	if c.transport == "http" {
		if c.closed.Load() {
			return fmt.Errorf("mcp[%s]: connection closed", c.name)
		}
		_, err := c.sendHTTP(ctx, notif)
		return err
	}

	if c.transport == "ws" || c.transport == "websocket" {
		_, err := c.sendWSNotification(ctx, notif)
		return err
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed.Load() {
		return fmt.Errorf("mcp[%s]: connection closed", c.name)
	}
	if err := c.writeMessageUnlocked(notif); err != nil {
		return fmt.Errorf("mcp[%s]: write message: %w", c.name, err)
	}
	return nil
}

func (c *Client) readResponseWithCancel(ctx context.Context, reqID *ID) (*Response, error) {

	var waiter chan *Response
	if reqID != nil {
		waiter = make(chan *Response, 1)
		c.registerWaiter(reqID, waiter)
		defer c.unregisterWaiter(reqID, waiter)
	}
	return c.readResponseWithWaiter(ctx, reqID, waiter)
}

func (c *Client) readResponseWithWaiter(ctx context.Context, reqID *ID, waiter chan *Response) (*Response, error) {
	type result struct {
		resp *Response
		err  error
	}

	done := make(chan result, 1)
	safego.Go("mcp.client.readResponse", func() {
		if waiter != nil {
			defer c.unregisterWaiter(reqID, waiter)
		}

		c.readMu.Lock()
		defer c.readMu.Unlock()
		resp, err := c.readResponse(ctx, reqID, waiter)
		done <- result{resp: resp, err: err}
	})
	select {
	case res := <-done:

		if res.resp != nil {
			return res.resp, nil
		}
		return res.resp, res.err
	case <-ctx.Done():

		select {
		case res := <-done:
			if res.resp != nil {
				return res.resp, nil
			}
		default:
		}

		if waiter != nil {
			c.unregisterWaiter(reqID, waiter)
		}

		if !c.hasOtherWaiters(waiter) {
			c.Abort()

			select {
			case res := <-done:

				if res.resp != nil {
					return res.resp, nil
				}
				return nil, c.withStderr(ctx.Err())
			case <-time.After(5 * time.Second):
				return nil, c.withStderr(fmt.Errorf("mcp[%s]: read goroutine did not return after abort: %w", c.name, ctx.Err()))
			}
		}

		c.armHangWatchdog()
		return nil, c.withStderr(fmt.Errorf("mcp[%s]: request cancelled while %d other request(s) in flight, connection kept: %w",
			c.name, c.waiterCountExcluding(waiter), ctx.Err()))
	}
}

const hungServerAbortGrace = 10 * time.Second

func (c *Client) armHangWatchdog() {
	if !c.hangWatchdogArmed.CompareAndSwap(false, true) {
		return
	}
	progressAtArm := c.lastReadProgress.Load()
	safego.Go("mcp.client.hangWatchdog", func() {
		defer c.hangWatchdogArmed.Store(false)
		time.Sleep(hungServerAbortGrace)
		if c.closed.Load() {
			return
		}
		if c.lastReadProgress.Load() == progressAtArm && c.waiterCountExcluding(nil) > 0 {

			c.hangAbort.Store(true)
			c.Abort()
		}
	})
}

func (c *Client) hasOtherWaiters(self chan *Response) bool {
	return c.waiterCountExcluding(self) > 0
}

func (c *Client) waiterCountExcluding(self chan *Response) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, ch := range c.waiters {
		if ch != self {
			n++
		}
	}
	return n
}

func (c *Client) writeMessage(msg interface{}) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.writeMessageUnlocked(msg)
}

func (c *Client) writeMessageUnlocked(msg interface{}) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("mcp[%s]: marshal message: %w", c.name, err)
	}
	if c.transport == "" || c.transport == "stdio" {
		data = append(data, '\n')
		return c.writeStdinWithDeadline(data)
	}
	header := fmt.Sprintf("Content-Length: %d\r\n\r\n", len(data))
	if err := c.writeStdinWithDeadline([]byte(header)); err != nil {
		return err
	}
	return c.writeStdinWithDeadline(data)
}

var mcpStdioWriteTimeout = 15 * time.Second

func (c *Client) writeStdinWithDeadline(data []byte) error {

	stdin := c.snapshotTransport().stdin
	if stdin == nil {
		return fmt.Errorf("mcp[%s]: stdin closed", c.name)
	}
	if c.closed.Load() {
		return fmt.Errorf("mcp[%s]: connection closed", c.name)
	}
	writeDone := make(chan error, 1)
	go func() {
		_, err := stdin.Write(data)
		writeDone <- err
	}()
	select {
	case err := <-writeDone:
		return err
	case <-time.After(mcpStdioWriteTimeout):

		c.Abort()
		select {
		case err := <-writeDone:
			if err == nil {

				err = fmt.Errorf("mcp[%s]: stdin write deadline exceeded", c.name)
			}
			return err
		case <-time.After(10 * time.Second):
			return fmt.Errorf("mcp[%s]: stdin write timed out after %s and did not unwound after Abort", c.name, mcpStdioWriteTimeout)
		}
	}
}

func (c *Client) sendHTTP(ctx context.Context, msg interface{}) (*Response, error) {
	return c.sendHTTPWithRetry(ctx, msg, true)
}

func (c *Client) sendHTTPWithRetry(ctx context.Context, msg interface{}, allowRetry bool) (*Response, error) {
	data, err := json.Marshal(msg)
	if err != nil {
		return nil, fmt.Errorf("mcp[%s]: marshal http message: %w", c.name, err)
	}

	var hdrProbe struct {
		Method string `json:"method"`
		Params struct {
			Name      string          `json:"name"`
			URI       string          `json:"uri"`
			Arguments json.RawMessage `json:"arguments"`
		} `json:"params"`
	}
	_ = json.Unmarshal(data, &hdrProbe)

	c.mu.Lock()
	httpClient := c.httpClient
	sessionID := c.sessionID
	headers := c.headers
	oauthHandler := c.oauthHandler

	protoVersion := c.negotiatedVersion

	var toolSchema json.RawMessage
	if hdrProbe.Method == "tools/call" {
		toolSchema = c.toolSchemas[hdrProbe.Params.Name]
	}
	c.mu.Unlock()
	if httpClient == nil {
		return nil, fmt.Errorf("mcp[%s]: connection closed", c.name)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("mcp[%s]: create request: %w", c.name, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	if protoVersion != "" {
		req.Header.Set("MCP-Protocol-Version", protoVersion)
	}
	if sessionID != "" {
		req.Header.Set("Mcp-Session-Id", sessionID)
	}

	if hdrProbe.Method != "" {
		req.Header.Set("Mcp-Method", hdrProbe.Method)
	}
	if hdrProbe.Params.Name != "" {
		req.Header.Set("Mcp-Name", hdrProbe.Params.Name)
	} else if hdrProbe.Params.URI != "" {
		req.Header.Set("Mcp-Name", hdrProbe.Params.URI)
	}
	if hdrProbe.Method == "tools/call" && len(toolSchema) > 0 && len(hdrProbe.Params.Arguments) > 0 {
		var args map[string]interface{}
		if err := json.Unmarshal(hdrProbe.Params.Arguments, &args); err == nil {
			for _, kv := range buildMCPParamHeaders(toolSchema, args) {
				req.Header.Set(kv[0], kv[1])
			}
		} else {
			debug.Log("mcp-http", "server=%s skip Mcp-Param headers: unmarshal arguments: %v", c.name, err)
		}
	}
	authHeader := ""
	if oauthHandler != nil {
		if token, _ := oauthHandler.GetAccessToken(ctx); token != "" {
			authHeader = "Bearer " + token
			req.Header.Set("Authorization", authHeader)
			debug.Log("mcp-http", "send_with_token server=%s has_token=true", c.name)
		} else {
			debug.Log("mcp-http", "send_no_token server=%s", c.name)
		}
	}

	if allowRetry && oauthHandler != nil && authHeader == "" &&
		headers["Authorization"] == "" && c.oauthProbed.CompareAndSwap(false, true) {
		probeCtx, probeCancel := context.WithTimeout(ctx, 5*time.Second)
		if oauthHandler.ProbeWellKnown(probeCtx) {
			probeCancel()
			return nil, &OAuthRequiredError{Handler: oauthHandler}
		}
		probeCancel()
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("mcp[%s]: http request: %w", c.name, err)
	}
	defer resp.Body.Close()
	if newSession := strings.TrimSpace(resp.Header.Get("Mcp-Session-Id")); newSession != "" {
		c.mu.Lock()
		if newSession != c.sessionID {

			c.httpNotifLastEventID = ""
		}
		c.sessionID = newSession
		c.mu.Unlock()
	}

	if streamed, handled, err := c.tryStreamSSEResponse(resp, msg); handled {
		return streamed, err
	}
	body, err := util.ReadAll(resp.Body, util.ReadLimitMCP)
	if err != nil {
		return nil, fmt.Errorf("mcp[%s]: read http body: %w", c.name, err)
	}
	contentType := resp.Header.Get("Content-Type")
	debug.Log("mcp-http", "response server=%s status=%d content_type=%s body_len=%d", c.name, resp.StatusCode, contentType, len(body))
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		if retried, handled, err := c.handleHTTPAuthChallenge(ctx, msg, resp, authHeader, allowRetry, oauthHandler); handled {
			return retried, err
		}
	}
	if resp.StatusCode == http.StatusNotFound && sessionID != "" && allowRetry {

		debug.Log("mcp-http", "server=%s 404 with session - dropping stale session and re-initializing", c.name)
		c.mu.Lock()
		c.sessionID = ""
		c.httpNotifLastEventID = ""
		c.mu.Unlock()
		if _, err := c.Initialize(ctx); err != nil {
			return nil, fmt.Errorf("mcp[%s]: re-init after 404: %w", c.name, err)
		}
		return c.sendHTTPWithRetry(ctx, msg, false)
	}
	if resp.StatusCode == http.StatusBadRequest && allowRetry && hdrProbe.Method == "tools/call" &&
		bytes.Contains(body, []byte("-32001")) {

		debug.Log("mcp-http", "server=%s tools/call rejected with HeaderMismatch (-32001); refreshing tool schemas and retrying", c.name)
		if _, err := c.ListTools(ctx); err != nil {
			debug.Log("mcp-http", "server=%s schema refresh after -32001 failed: %v", c.name, err)
		}
		return c.sendHTTPWithRetry(ctx, msg, false)
	}
	if resp.StatusCode >= 400 {
		bodyPreview := strings.TrimSpace(string(body))
		if len(bodyPreview) > 200 {
			bodyPreview = bodyPreview[:200]
		}
		return nil, fmt.Errorf("mcp[%s]: http status %d: %s", c.name, resp.StatusCode, bodyPreview)
	}
	switch msg.(type) {
	case Notification:
		return &Response{JSONRPC: "2.0"}, nil
	}

	return parseHTTPResponseForID(body, contentType, requestIDOf(msg))
}

func requestIDOf(msg interface{}) *ID {
	switch typed := msg.(type) {
	case *Request:
		return typed.ID
	case Request:
		return typed.ID
	}
	return nil
}

func (c *Client) tryStreamSSEResponse(resp *http.Response, msg interface{}) (streamed *Response, handled bool, err error) {
	contentType := resp.Header.Get("Content-Type")
	if resp.StatusCode >= 400 || !strings.Contains(strings.ToLower(contentType), "text/event-stream") {
		return nil, false, nil
	}
	if _, isNotif := msg.(Notification); isNotif {

		debug.Log("mcp-http", "response server=%s status=%d content_type=%s (notification, stream not awaited)", c.name, resp.StatusCode, contentType)
		return &Response{JSONRPC: "2.0"}, true, nil
	}
	streamed, serr := c.streamHTTPSSEResponse(resp.Body, requestIDOf(msg))
	if serr != nil {
		return nil, true, fmt.Errorf("mcp[%s]: read http body: %w", c.name, serr)
	}
	debug.Log("mcp-http", "response server=%s status=%d content_type=%s (streamed, early return on matching Response)", c.name, resp.StatusCode, contentType)
	return streamed, true, nil
}

func (c *Client) handleHTTPAuthChallenge(ctx context.Context, msg interface{}, resp *http.Response, authHeader string, allowRetry bool, oauthHandler *OAuthHandler) (retried *Response, handled bool, err error) {
	if oauthHandler == nil {
		return nil, false, nil
	}
	needsOAuth, _ := oauthHandler.Handle401(resp)
	if allowRetry {
		if token, _ := oauthHandler.GetAccessToken(ctx); token != "" && "Bearer "+token != authHeader {

			c.mu.Lock()
			if _, hasUserAuth := c.headers["Authorization"]; hasUserAuth {
				headers := cloneStringMap(c.headers)
				delete(headers, "Authorization")
				c.headers = headers
				debug.Log("mcp-http", "auth_switched server=%s from_apikey=true to_oauth=true", c.name)
			}
			c.mu.Unlock()
			debug.Log("mcp-http", "retry_after_discovery server=%s has_token=true", c.name)
			retried, err := c.sendHTTPWithRetry(ctx, msg, false)
			return retried, true, err
		}
	}
	if needsOAuth {
		return nil, true, &OAuthRequiredError{Handler: oauthHandler}
	}
	return nil, false, nil
}

func (c *Client) sendWSUnlocked(ctx context.Context, req Request) (*Response, error) {
	data, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("mcp[%s]: marshal ws message: %w", c.name, err)
	}

	var waiter chan *Response
	if req.ID != nil {
		waiter = make(chan *Response, 1)
		c.registerWaiter(req.ID, waiter)
		defer c.unregisterWaiter(req.ID, waiter)
	}

	c.mu.Lock()
	if c.closed.Load() {
		c.mu.Unlock()
		return nil, fmt.Errorf("mcp[%s]: connection closed", c.name)
	}

	wsConn := c.snapshotTransport().wsConn
	if wsConn == nil {
		c.mu.Unlock()
		return nil, fmt.Errorf("mcp[%s]: websocket connection not established", c.name)
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = wsConn.SetWriteDeadline(deadline)
	}
	if err := wsConn.WriteMessage(websocket.TextMessage, data); err != nil {
		c.mu.Unlock()
		return nil, fmt.Errorf("mcp[%s]: websocket write: %w", c.name, err)
	}
	c.mu.Unlock()

	return c.readWSResponse(ctx, req.ID, waiter)
}

func (c *Client) readWSResponse(ctx context.Context, reqID *ID, waiter chan *Response) (*Response, error) {
	type result struct {
		resp *Response
		err  error
	}
	done := make(chan result, 1)
	safego.Go("mcp.client.readWS", func() {
		resp, err := c.readWSLoop(ctx, reqID, waiter)
		done <- result{resp, err}
	})
	select {
	case res := <-done:
		return res.resp, res.err
	case <-ctx.Done():

		if !c.hasOtherWaiters(waiter) {
			c.Abort()
			select {
			case res := <-done:
				return res.resp, res.err
			case <-time.After(5 * time.Second):
				return nil, fmt.Errorf("mcp[%s]: ws read goroutine did not return after abort: %w", c.name, ctx.Err())
			}
		}

		return nil, fmt.Errorf("mcp[%s]: ws request cancelled while %d other request(s) in flight, connection kept: %w",
			c.name, c.waiterCountExcluding(waiter), ctx.Err())
	}
}

func (c *Client) readWSLoop(ctx context.Context, reqID *ID, waiter chan *Response) (*Response, error) {
	c.wsMu.Lock()
	defer c.wsMu.Unlock()
	for {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("mcp[%s]: context cancelled: %w", c.name, err)
		}
		if waiter != nil {
			select {
			case resp := <-waiter:
				return resp, nil
			default:
			}
		}
		if deadline, ok := ctx.Deadline(); ok {
			_ = c.snapshotTransport().wsConn.SetReadDeadline(deadline)
		}
		_, payload, err := c.snapshotTransport().wsConn.ReadMessage()
		if err != nil {
			return nil, fmt.Errorf("mcp[%s]: websocket read: %w", c.name, err)
		}
		parsed, err := ParseMessage(payload)
		if err != nil {
			return nil, fmt.Errorf("mcp[%s]: parse ws message: %w", c.name, err)
		}
		switch typed := parsed.(type) {
		case *Response:

			if isNullID(typed.ID) {
				c.deliverResponse(typed)
				continue
			}

			if reqID != nil && !responseIDMatches(typed.ID, reqID) {
				c.deliverResponse(typed)
				continue
			}
			return typed, nil
		case *Notification:

			c.processNotification(typed)
			continue
		case *Request:
			_ = c.respondToServerRequestWS(typed)
			continue
		}
	}
}

func (c *Client) sendWSNotification(ctx context.Context, msg interface{}) (*Response, error) {
	data, err := json.Marshal(msg)
	if err != nil {
		return nil, fmt.Errorf("mcp[%s]: marshal ws notification: %w", c.name, err)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed.Load() {
		return nil, fmt.Errorf("mcp[%s]: connection closed", c.name)
	}

	wsConn := c.snapshotTransport().wsConn
	if wsConn == nil {
		return nil, fmt.Errorf("mcp[%s]: websocket connection not established", c.name)
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = wsConn.SetWriteDeadline(deadline)
	}
	if err := wsConn.WriteMessage(websocket.TextMessage, data); err != nil {
		return nil, fmt.Errorf("mcp[%s]: websocket write: %w", c.name, err)
	}
	return &Response{JSONRPC: "2.0"}, nil
}

func parseHTTPResponse(body []byte, contentType string) (*Response, error) {
	return parseHTTPResponseForID(body, contentType, nil)
}

func parseHTTPResponseForID(body []byte, contentType string, reqID *ID) (*Response, error) {
	payload := body
	if strings.Contains(strings.ToLower(contentType), "text/event-stream") {

		resp, err := extractSSEResponse(body)
		if err != nil {
			return nil, err
		}
		return resp, nil
	}

	msg, err := ParseMessage(payload)
	if err != nil {

		if r := extractNDJSONResponseForID(body, reqID); r != nil {
			debug.Log("mcp-http", "parseHTTPResponse: whole-body parse failed, recovered Response via NDJSON fallback")
			return r, nil
		}
		return nil, fmt.Errorf("parse message: %w", err)
	}
	resp, ok := msg.(*Response)
	if ok {
		if reqID != nil && !isNullID(resp.ID) && !responseIDMatches(resp.ID, reqID) {
			debug.Log("mcp-http", "parseHTTPResponse: foreign response id (concurrent stream), trying extraction fallbacks")
		} else {
			return resp, nil
		}
	}

	debug.Log("mcp-http", "parseHTTPResponse: first message was %T, trying SSE fallback", msg)
	if r, err := extractSSEResponseForID(body, reqID); err == nil {
		return r, nil
	}

	if r := extractNDJSONResponseForID(body, reqID); r != nil {
		debug.Log("mcp-http", "parseHTTPResponse: recovered Response via NDJSON fallback")
		return r, nil
	}

	if !isJSONRPCMessage(body) {
		return nil, fmt.Errorf("non-JSON-RPC response (content-type %s): %s", contentType, previewBody(body))
	}
	return nil, fmt.Errorf("expected response, got %T", msg)
}

func isJSONRPCMessage(body []byte) bool {
	var probe struct {
		JSONRPC any `json:"jsonrpc"`
		ID      any `json:"id"`
		Result  any `json:"result"`
		Error   any `json:"error"`
		Method  any `json:"method"`
	}
	if err := json.Unmarshal(body, &probe); err != nil {
		return false
	}
	return probe.JSONRPC != nil || probe.ID != nil || probe.Result != nil ||
		probe.Error != nil || probe.Method != nil
}

func previewBody(body []byte) string {
	s := strings.TrimSpace(string(body))
	if len(s) > 300 {
		s = s[:300] + "..."
	}
	return s
}

func extractSSEResponse(body []byte) (*Response, error) {
	return extractSSEResponseForID(body, nil)
}

func extractSSEResponseForID(body []byte, reqID *ID) (*Response, error) {
	events, scanErr := extractAllSSEDataChecked(body)
	if len(events) == 0 {
		if scanErr != nil {

			return nil, fmt.Errorf("parsing SSE response: %w", scanErr)
		}
		return nil, fmt.Errorf("parsing SSE response: no data event found")
	}
	var lastParseErr error
	for _, payload := range events {
		msg, err := ParseMessage(payload)
		if err != nil {
			lastParseErr = err
			continue
		}
		if resp, ok := msg.(*Response); ok {
			if reqID != nil && !isNullID(resp.ID) && !responseIDMatches(resp.ID, reqID) {

				debug.Log("mcp-http", "extractSSEResponse: skipping response with foreign id (concurrent stream)")
				continue
			}
			return resp, nil
		}

		debug.Log("mcp-http", "parseHTTPResponse: skipping non-response SSE event %T", msg)
	}
	if lastParseErr != nil {
		return nil, fmt.Errorf("parsing SSE response: no valid JSON-RPC message found: %w", lastParseErr)
	}

	if scanErr != nil {
		return nil, fmt.Errorf("parsing SSE response: %w", scanErr)
	}
	return nil, fmt.Errorf("parsing SSE response: no Response found in %d event(s)", len(events))
}

func extractAllSSEDataChecked(body []byte) ([][]byte, error) {
	scanner := bufio.NewScanner(bytes.NewReader(body))
	scanner.Buffer(make([]byte, 0, 256*1024), 1024*1024)
	var events [][]byte
	var dataLines []string
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "data:"):
			dataLines = append(dataLines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		case strings.TrimSpace(line) == "" && len(dataLines) > 0:
			events = append(events, []byte(strings.Join(dataLines, "\n")))
			dataLines = nil
		}
	}
	if err := scanner.Err(); err != nil {
		return events, fmt.Errorf("scanning SSE events (line exceeds %d bytes?): %w", 1024*1024, err)
	}
	if len(dataLines) > 0 {
		events = append(events, []byte(strings.Join(dataLines, "\n")))
	}
	return events, nil
}

func (c *Client) streamHTTPSSEResponse(r io.Reader, reqID *ID) (*Response, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 256*1024), 1024*1024)
	var dataLines []string
	var pendingEventID string
	var found *Response
	var lastParseErr error
	events := 0

	flush := func() {
		if len(dataLines) == 0 {
			return
		}
		payload := []byte(strings.Join(dataLines, "\n"))
		dataLines = nil
		events++
		c.noteSSEEventID(pendingEventID)
		pendingEventID = ""
		msg, err := ParseMessage(payload)
		if err != nil {
			lastParseErr = err
			return
		}
		switch typed := msg.(type) {
		case *Response:
			if reqID != nil && !isNullID(typed.ID) && !responseIDMatches(typed.ID, reqID) {
				debug.Log("mcp-http", "streamHTTPSSEResponse: skipping response with foreign id (concurrent stream)")
				return
			}
			found = typed
		case *Notification:

			c.processNotification(typed)
		default:
			debug.Log("mcp-http", "streamHTTPSSEResponse: skipping non-response SSE event %T", msg)
		}
	}
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "data:"):
			dataLines = append(dataLines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		case strings.HasPrefix(line, "id:"):
			pendingEventID = strings.TrimSpace(strings.TrimPrefix(line, "id:"))
		case strings.TrimSpace(line) == "":
			flush()
			if found != nil {
				return found, nil
			}
		}
	}
	scanErr := scanner.Err()
	if found == nil {
		flush()
	}
	if found != nil {
		return found, nil
	}

	if scanErr != nil {
		return nil, fmt.Errorf("parsing SSE response: scanning SSE events (line exceeds %d bytes?): %w", 1024*1024, scanErr)
	}
	if events == 0 {
		return nil, fmt.Errorf("parsing SSE response: no data event found")
	}
	if lastParseErr != nil {
		return nil, fmt.Errorf("parsing SSE response: no valid JSON-RPC message found: %w", lastParseErr)
	}
	return nil, fmt.Errorf("parsing SSE response: no Response found in %d event(s)", events)
}

type httpNotifResult int

const (
	httpNotifDrop httpNotifResult = iota

	httpNotifUnsupported

	httpNotifSessionGone

	httpNotifClosing
)

func (c *Client) startHTTPNotificationStream() {
	if c.transport != "http" || c.closed.Load() || c.httpNotifDisabled.Load() {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	c.mu.Lock()
	if c.notifStreamCancel != nil {

		go c.notifStreamCancel()
	}
	c.notifStreamGen++
	myGen := c.notifStreamGen
	c.notifStreamCancel = cancel
	c.mu.Unlock()
	safego.Go("mcp.client.httpNotifStream", func() {
		defer cancel()

		defer func() {
			c.mu.Lock()

			if c.notifStreamGen == myGen {
				c.notifStreamCancel = nil
			}
			c.mu.Unlock()
		}()
		backoff := []time.Duration{5 * time.Second, 15 * time.Second, 30 * time.Second}
		attempt := 0
		for {
			res := c.readHTTPNotifStreamOnce(ctx)
			if res == httpNotifClosing || c.closed.Load() || ctx.Err() != nil {
				return
			}
			if res == httpNotifUnsupported {
				c.httpNotifDisabled.Store(true)
				debug.Log("mcp-http", "notif-stream server=%s standalone GET stream unsupported (405/non-SSE); disabled for this client", c.name)
				return
			}
			if res == httpNotifSessionGone {
				debug.Log("mcp-http", "notif-stream server=%s session expired (404); stopping (reconnect cycle rebuilds)", c.name)
				return
			}

			delay := 60 * time.Second
			if attempt < len(backoff) {
				delay = backoff[attempt]
			}
			attempt++
			debug.Log("mcp-http", "notif-stream server=%s dropped; retry in %s (attempt %d)", c.name, delay, attempt)
			if !c.sleepUntilClosed(delay) {
				return
			}
		}
	})
}

func (c *Client) readHTTPNotifStreamOnce(ctx context.Context) httpNotifResult {
	c.mu.Lock()
	httpClient := c.httpClient
	sessionID := c.sessionID
	headers := c.headers
	oauthHandler := c.oauthHandler
	url := c.url

	protoVersion := c.negotiatedVersion
	c.mu.Unlock()
	if httpClient == nil {
		return httpNotifClosing
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return httpNotifDrop
	}
	req.Header.Set("Accept", "text/event-stream")
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	if protoVersion != "" {
		req.Header.Set("MCP-Protocol-Version", protoVersion)
	}
	if sessionID != "" {
		req.Header.Set("Mcp-Session-Id", sessionID)
	}

	if lastEventID := c.lastEventIDSnapshot(); lastEventID != "" {
		req.Header.Set("Last-Event-ID", lastEventID)
		debug.Log("mcp-http", "notif-stream server=%s reconnecting with Last-Event-ID=%s", c.name, lastEventID)
	}
	if oauthHandler != nil {
		if token, _ := oauthHandler.GetAccessToken(req.Context()); token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		if c.closed.Load() || ctx.Err() != nil {
			return httpNotifClosing
		}
		return httpNotifDrop
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusMethodNotAllowed {
		return httpNotifUnsupported
	}
	if resp.StatusCode == http.StatusNotFound {
		return httpNotifSessionGone
	}
	if resp.StatusCode >= 400 {

		return httpNotifDrop
	}
	contentType := resp.Header.Get("Content-Type")
	if !strings.Contains(strings.ToLower(contentType), "text/event-stream") {

		return httpNotifUnsupported
	}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	var dataLines []string
	var pendingEventID string
	flush := func() {
		if len(dataLines) == 0 {

			if pendingEventID != "" {
				c.noteSSEEventID(pendingEventID)
				pendingEventID = ""
			}
			return
		}
		payload := []byte(strings.Join(dataLines, "\n"))
		dataLines = nil
		c.noteSSEEventID(pendingEventID)
		pendingEventID = ""
		msg, err := ParseMessage(payload)
		if err != nil {
			debug.Log("mcp-http", "notif-stream server=%s unparsable event: %v", c.name, err)
			return
		}
		switch typed := msg.(type) {
		case *Notification:
			c.processNotification(typed)
		default:

			debug.Log("mcp-http", "notif-stream server=%s skipping non-notification event %T", c.name, msg)
		}
	}
	for scanner.Scan() {
		if c.closed.Load() {
			return httpNotifClosing
		}
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "data:"):
			dataLines = append(dataLines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		case strings.HasPrefix(line, "id:"):
			pendingEventID = strings.TrimSpace(strings.TrimPrefix(line, "id:"))
		case strings.TrimSpace(line) == "":
			flush()
		}
	}

	flush()
	if err := scanner.Err(); err != nil {
		debug.Log("mcp-client", "http notif stream read error: %v", err)
	}
	if c.closed.Load() {
		return httpNotifClosing
	}
	return httpNotifDrop
}

func (c *Client) noteSSEEventID(id string) {
	if id == "" {
		return
	}
	c.mu.Lock()
	c.httpNotifLastEventID = id
	c.mu.Unlock()
}

func (c *Client) lastEventIDSnapshot() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.httpNotifLastEventID
}

func (c *Client) sleepUntilClosed(d time.Duration) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if c.closed.Load() {
			return false
		}
		time.Sleep(min(time.Until(deadline), time.Second))
	}
	return !c.closed.Load()
}

func extractNDJSONResponseForID(body []byte, reqID *ID) *Response {
	scanner := bufio.NewScanner(bytes.NewReader(body))
	scanner.Buffer(make([]byte, 0, 256*1024), 1024*1024)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 || line[0] != '{' {
			continue
		}
		msg, err := ParseMessage(line)
		if err != nil {
			continue
		}
		if resp, ok := msg.(*Response); ok {
			if reqID != nil && !isNullID(resp.ID) && !responseIDMatches(resp.ID, reqID) {
				continue
			}
			return resp
		}
		debug.Log("mcp-http", "extractNDJSONResponse: skipping non-response message %T", msg)
	}
	if err := scanner.Err(); err != nil {

		debug.Log("mcp-http", "extractNDJSONResponse: scan error (line exceeds buffer?): %v", err)
	}
	return nil
}

func cloneStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	cloned := make(map[string]string, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}

func flattenEnvMap(values map[string]string) []string {
	if len(values) == 0 {
		return nil
	}
	flat := make([]string, 0, len(values))
	for key, value := range values {
		flat = append(flat, key+"="+value)
	}
	return flat
}

func (c *Client) respondToServerRequestWS(req *Request) error {
	if req == nil || req.ID == nil {
		return nil
	}
	errResp := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      req.ID,
		"error": map[string]interface{}{
			"code":    -32601,
			"message": fmt.Sprintf("method not supported over WebSocket: %s", req.Method),
		},
	}
	data, err := json.Marshal(errResp)
	if err != nil {
		return err
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	wsConn := c.snapshotTransport().wsConn
	if wsConn == nil {
		return fmt.Errorf("mcp[%s]: websocket connection not established", c.name)
	}
	return wsConn.WriteMessage(websocket.TextMessage, data)
}

func (c *Client) readResponse(ctx context.Context, reqID *ID, waiter chan *Response) (*Response, error) {
	for {
		if waiter != nil {
			select {
			case resp := <-waiter:
				return resp, nil
			default:
			}
		}
		msg, err := c.readMessage(ctx)
		if err != nil {
			return nil, fmt.Errorf("mcp[%s]: read message: %w", c.name, err)
		}

		c.lastReadProgress.Store(time.Now().UnixNano())
		switch typed := msg.(type) {
		case *Response:

			if isNullID(typed.ID) {
				c.deliverResponse(typed)
				continue
			}

			if reqID != nil && !responseIDMatches(typed.ID, reqID) {
				c.deliverResponse(typed)
				continue
			}
			return typed, nil
		case *Notification:
			c.processNotification(typed)
			continue
		case *Request:

			if err := c.dispatchServerRequest(typed); err != nil {
				return nil, fmt.Errorf("mcp[%s]: handle server request: %w", c.name, err)
			}
		default:
			return nil, c.withStderr(fmt.Errorf("unexpected MCP message type %T", msg))
		}
	}
}

func responseIDMatches(raw json.RawMessage, reqID *ID) bool {
	if reqID == nil || len(raw) == 0 {
		return true
	}
	reqJSON, err := json.Marshal(reqID)
	if err != nil {
		return false
	}
	return bytes.Equal(bytes.TrimSpace(raw), bytes.TrimSpace(reqJSON))
}

func (c *Client) registerWaiter(reqID *ID, ch chan *Response) {
	key, err := json.Marshal(reqID)
	if err != nil {
		return
	}
	c.mu.Lock()
	if c.waiters == nil {
		c.waiters = make(map[string]chan *Response)
	}
	c.waiters[string(key)] = ch
	c.mu.Unlock()
}

func (c *Client) unregisterWaiter(reqID *ID, ch chan *Response) {
	key, err := json.Marshal(reqID)
	if err != nil {
		return
	}
	c.mu.Lock()
	if cur, ok := c.waiters[string(key)]; ok && cur == ch {
		delete(c.waiters, string(key))
	}
	c.mu.Unlock()
}

func (c *Client) deliverResponse(resp *Response) {
	if isNullID(resp.ID) {
		debug.Log("mcp-stdio", "server=%s dropping response with %s id (error: %s)",
			c.name, idStateDesc(resp.ID), responseErrorDesc(resp))
		return
	}
	key := string(bytes.TrimSpace(resp.ID))
	c.mu.Lock()
	ch, ok := c.waiters[key]
	c.mu.Unlock()
	if ok {
		select {
		case ch <- resp:
		default:
		}
		return
	}
	debug.Log("mcp-stdio", "server=%s dropping response with unknown ID %s", c.name, key)
}

func isNullID(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null"))
}

func idStateDesc(raw json.RawMessage) string {
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return "null"
	}
	return "empty"
}

func responseErrorDesc(resp *Response) string {
	if resp == nil || resp.Error == nil {
		return "<no error>"
	}
	msg := resp.Error.Message
	if len(msg) > 200 {
		msg = msg[:200]
	}
	return fmt.Sprintf("%d %s", resp.Error.Code, msg)
}

func (c *Client) readMessage(ctx context.Context) (interface{}, error) {
	reader := c.reader
	for {
		select {
		case <-ctx.Done():
			return nil, c.withStderr(ctx.Err())
		default:
		}

		peek, err := reader.Peek(1)
		if err != nil {
			return nil, c.withStderr(fmt.Errorf("reading message: %w", err))
		}
		switch peek[0] {
		case '\r', '\n', ' ', '\t':
			if _, err := reader.ReadByte(); err != nil {
				return nil, c.withStderr(fmt.Errorf("discarding whitespace: %w", err))
			}
			continue
		case '{':

			line, err := readBoundedLine(reader, maxNDJSONLineLength)
			if err != nil {
				return nil, c.withStderr(fmt.Errorf("reading message line: %w", err))
			}
			msg, err := ParseMessage(bytes.TrimSpace(line))
			if err != nil {
				return nil, c.withStderr(err)
			}
			return msg, nil
		default:
			return c.readHeaderFramedMessage(ctx)
		}
	}
}

func (c *Client) readHeaderFramedMessage(ctx context.Context) (interface{}, error) {
	reader := c.reader
	contentLength := -1

	for {
		select {
		case <-ctx.Done():
			return nil, c.withStderr(ctx.Err())
		default:
		}

		line, err := readBoundedLine(reader, maxNDJSONLineLength)
		if err != nil {
			return nil, c.withStderr(fmt.Errorf("reading header: %w", err))
		}
		trimmed := strings.TrimSpace(string(line))
		if trimmed == "" {
			if contentLength >= 0 {
				break
			}
			continue
		}
		parts := strings.SplitN(trimmed, ":", 2)
		if len(parts) != 2 {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(parts[0]), "Content-Length") {
			continue
		}
		if _, err := fmt.Sscanf(strings.TrimSpace(parts[1]), "%d", &contentLength); err != nil {
			return nil, c.withStderr(fmt.Errorf("parsing Content-Length: %w", err))
		}
	}

	if contentLength < 0 || contentLength > maxHeaderContentLength {
		return nil, c.withStderr(fmt.Errorf("invalid Content-Length %d (max %d)", contentLength, maxHeaderContentLength))
	}

	body := make([]byte, contentLength)
	if _, err := io.ReadFull(reader, body); err != nil {
		return nil, c.withStderr(fmt.Errorf("reading body: %w", err))
	}

	msg, err := ParseMessage(body)
	if err != nil {
		return nil, c.withStderr(err)
	}
	return msg, nil
}

func (c *Client) handleServerRequest(req *Request) error {
	if req == nil || req.ID == nil {
		return nil
	}
	switch req.Method {
	case "sampling/createMessage", "elicitation/create":

		return c.handleServerRequestAsync(req)
	case "roots/list":
		rootURI, err := currentRootURI()
		if err != nil {
			return c.writeErrorResponse(req.ID, -32603, err.Error())
		}
		return c.writeResultResponse(req.ID, map[string]any{
			"roots": []map[string]string{{"uri": rootURI}},
		})
	case "ping":
		return c.writeResultResponse(req.ID, map[string]any{})
	default:
		return c.writeErrorResponse(req.ID, -32601, fmt.Sprintf("method not found: %s", req.Method))
	}
}

func (c *Client) dispatchServerRequest(req *Request) error {
	return c.handleServerRequest(req)
}

func (c *Client) handleServerRequestAsync(req *Request) error {
	isSampling := req.Method == "sampling/createMessage"
	if isSampling && c.samplingHandlerLocked() == nil {
		return c.writeErrorResponse(req.ID, -32601, "sampling not supported")
	}
	if !isSampling && c.elicitationHandlerLocked() == nil {
		return c.writeErrorResponse(req.ID, -32601, "elicitation not supported")
	}

	c.serverReqOnce.Do(func() {
		c.serverReqSem = make(chan struct{}, maxInteractiveServerRequests)
	})
	select {
	case c.serverReqSem <- struct{}{}:
	default:
		return c.writeErrorResponse(req.ID, -32603, "too many concurrent interactive server requests")
	}
	name := c.name
	reqCopy := *req
	safego.Go("mcp.client.serverRequest", func() {
		defer func() { <-c.serverReqSem }()
		if err := c.handleInteractiveRequest(&reqCopy); err != nil {
			debug.Log("mcp-client", "server=%s interactive request %s failed: %v", name, reqCopy.Method, err)
		}
	})
	return nil
}

const maxInteractiveServerRequests = 8

func (c *Client) handleInteractiveRequest(req *Request) error {
	if req.Method == "sampling/createMessage" {
		return c.handleSampling(req)
	}
	return c.handleElicitation(req)
}

func (c *Client) handleSampling(req *Request) error {
	handler := c.samplingHandlerLocked()
	if handler == nil {
		return c.writeErrorResponse(req.ID, -32601, "sampling not supported")
	}

	params, err := ParseSamplingParams(req.Params)
	if err != nil {
		return c.writeErrorResponse(req.ID, -32602, fmt.Sprintf("invalid sampling params: %v", err))
	}

	if err := ValidateSamplingParams(params); err != nil {
		return c.writeErrorResponse(req.ID, -32602, fmt.Sprintf("invalid sampling params: %v", err))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	result, err := handler(ctx, params)
	if err != nil {
		return c.writeErrorResponse(req.ID, -32603, fmt.Sprintf("sampling failed: %v", err))
	}
	return c.writeResultResponse(req.ID, result)
}

func (c *Client) handleElicitation(req *Request) error {
	handler := c.elicitationHandlerLocked()
	if handler == nil {
		return c.writeErrorResponse(req.ID, -32601, "elicitation not supported")
	}

	params, err := ParseElicitationParams(req.Params)
	if err != nil {
		return c.writeErrorResponse(req.ID, -32602, fmt.Sprintf("invalid elicitation params: %v", err))
	}

	if err := validateElicitationParams(params); err != nil {
		return c.writeErrorResponse(req.ID, -32602, fmt.Sprintf("invalid elicitation params: %v", err))
	}
	if params.EffectiveMode() == ElicitationModeURL {

		c.trackURLElicitation(params.ElicitationID)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	result, err := handler(ctx, params)
	if err != nil {
		return c.writeErrorResponse(req.ID, -32603, fmt.Sprintf("elicitation failed: %v", err))
	}
	if params.EffectiveMode() == ElicitationModeURL && result != nil {

		result.Content = nil
	}
	return c.writeResultResponse(req.ID, result)
}

const pendingURLElicitationsCap = 128

func (c *Client) trackURLElicitation(id string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.pendingURLElicitations == nil {
		c.pendingURLElicitations = make(map[string]struct{})
	}
	if len(c.pendingURLElicitations) >= pendingURLElicitationsCap {

		c.pendingURLElicitations = make(map[string]struct{})
	}
	c.pendingURLElicitations[id] = struct{}{}
}

func (c *Client) handleElicitationComplete(params json.RawMessage) {
	var p struct {
		ElicitationID string `json:"elicitationId"`
	}
	if err := json.Unmarshal(params, &p); err != nil || p.ElicitationID == "" {
		debug.Log("mcp-client", "elicitation complete notification without elicitationId ignored")
		return
	}
	c.mu.Lock()
	_, known := c.pendingURLElicitations[p.ElicitationID]
	delete(c.pendingURLElicitations, p.ElicitationID)
	c.mu.Unlock()
	if !known {

		debug.Log("mcp-client", "elicitation complete for unknown id %q ignored", p.ElicitationID)
		return
	}
	debug.Log("mcp-client", "url elicitation %q completed out-of-band", p.ElicitationID)
}

func (c *Client) SetElicitationHandler(h ElicitationHandler) {

	c.mu.Lock()
	c.elicitationHandler = h
	c.mu.Unlock()
}

func (c *Client) SetSamplingHandler(h SamplingHandler) {

	c.mu.Lock()
	c.samplingHandler = h
	c.mu.Unlock()
}

func (c *Client) samplingHandlerLocked() SamplingHandler {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.samplingHandler
}

func (c *Client) elicitationHandlerLocked() ElicitationHandler {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.elicitationHandler
}

func (c *Client) writeResultResponse(id *ID, result interface{}) error {
	data, err := json.Marshal(result)
	if err != nil {
		return c.withStderr(err)
	}
	return c.writeMessage(Response{
		JSONRPC: "2.0",
		ID:      marshalRequestID(id),
		Result:  data,
	})
}

func (c *Client) writeErrorResponse(id *ID, code int, message string) error {
	return c.writeMessage(Response{
		JSONRPC: "2.0",
		ID:      marshalRequestID(id),
		Error: &Error{
			Code:    code,
			Message: message,
		},
	})
}

func marshalRequestID(id *ID) json.RawMessage {
	if id == nil {
		return nil
	}
	data, err := json.Marshal(id)
	if err != nil {
		return nil
	}
	return data
}

func currentRootURI() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	abs, err := filepath.Abs(cwd)
	if err != nil {
		return "", err
	}
	return (&url.URL{Scheme: "file", Path: filepath.ToSlash(abs)}).String(), nil
}

func (c *Client) captureStderr(r io.Reader) {
	buf := make([]byte, 4096)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			c.appendStderr(buf[:n])
		}
		if err != nil {
			return
		}
	}
}

func (c *Client) appendStderr(data []byte) {
	if len(data) == 0 {
		return
	}
	const maxStderrBytes = 64 * 1024
	c.stderrMu.Lock()
	defer c.stderrMu.Unlock()
	remaining := maxStderrBytes - c.stderrBuf.Len()
	if remaining <= 0 {
		return
	}
	if len(data) > remaining {
		data = data[:remaining]
	}
	c.stderrBuf.Write(data)
}

func (c *Client) stderrSummary() string {
	c.stderrMu.RLock()
	defer c.stderrMu.RUnlock()
	text := strings.TrimSpace(c.stderrBuf.String())
	if text == "" {
		return ""
	}
	const maxSummary = 512
	if len(text) > maxSummary {
		text = text[len(text)-maxSummary:]
	}
	return strings.TrimSpace(text)
}

func (c *Client) withStderr(err error) error {
	if err == nil {
		return nil
	}
	if stderr := c.stderrSummary(); stderr != "" && !strings.Contains(err.Error(), stderr) {
		return fmt.Errorf("%w: server stderr: %s", err, stderr)
	}
	return err
}

func (c *Client) SetNotificationHandler(h func(method string, params json.RawMessage)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.notificationHandler = h
}

const notificationChanSize = 64

func (c *Client) processNotification(notif *Notification) {
	if notif == nil {
		return
	}

	if notif.Method == "notifications/elicitation/complete" {
		c.handleElicitationComplete(notif.Params)
		return
	}

	if c.routeSubscriptionNotification(notif) {
		return
	}

	c.cacheInvalidateForNotification(notif.Method, notif.Params)

	if c.notificationHandler == nil {
		return
	}

	c.notificationOnce.Do(func() {
		if c.notificationCh == nil {
			c.notificationCh = make(chan *Notification, notificationChanSize)
		}
		if c.notificationDone == nil {
			c.notificationDone = make(chan struct{})
		}
		ch := c.notificationCh
		done := c.notificationDone
		safego.Go("mcp.client.notifications", func() {
			for {
				select {
				case n := <-ch:
					handler := c.notificationHandler
					if handler == nil {
						continue
					}
					debug.Log("mcp-notif", "server=%s method=%s", c.name, n.Method)

					handler(n.Method, n.Params)
				case <-done:
					return
				}
			}
		})
	})
	debug.Log("mcp-notif", "queued server=%s method=%s", c.name, notif.Method)
	select {
	case c.notificationCh <- notif:
	default:
		debug.Log("mcp-notif", "notification queue full, dropping server=%s method=%s",
			c.name, notif.Method)
	}
}

func (c *Client) SetLevel(ctx context.Context, level string) error {
	_, caps := c.negotiatedState()
	if caps.Logging == nil {
		return fmt.Errorf("mcp[%s]: server does not support logging", c.name)
	}
	params := struct {
		Level string `json:"level"`
	}{Level: level}
	if err := c.sendRequest(ctx, "logging/setLevel", params, nil); err != nil {
		return fmt.Errorf("mcp[%s]: logging/setLevel: %w", c.name, err)
	}
	return nil
}

func (c *Client) HasToolsListChanged() bool {
	_, caps := c.negotiatedState()
	return caps.Tools != nil && caps.Tools.ListChanged
}

func (c *Client) HasLogging() bool {
	_, caps := c.negotiatedState()
	return caps.Logging != nil
}

func (c *Client) HasResourceSubscribe() bool {
	_, caps := c.negotiatedState()
	return caps.Resources != nil && caps.Resources.Subscribe
}

type InitializeParams struct {
	ProtocolVersion string         `json:"protocolVersion"`
	Capabilities    ClientCaps     `json:"capabilities"`
	ClientInfo      Implementation `json:"clientInfo"`
}

type ClientCaps struct {
	Roots struct {
		ListChanged bool `json:"listChanged,omitempty"`
	} `json:"roots,omitempty"`
	Sampling    *SamplingCapability    `json:"sampling,omitempty"`
	Elicitation *ElicitationCapability `json:"elicitation,omitempty"`

	Tasks *struct{} `json:"tasks,omitempty"`
}

type ElicitationCapability struct {
	Form *struct{} `json:"form,omitempty"`
	URL  *struct{} `json:"url,omitempty"`
}

type SamplingCapability struct {
	Tools *struct{} `json:"tools,omitempty"`
}

type Implementation struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type InitializeResult struct {
	ProtocolVersion string         `json:"protocolVersion"`
	Capabilities    ServerCaps     `json:"capabilities"`
	ServerInfo      Implementation `json:"serverInfo"`

	Instructions string `json:"instructions,omitempty"`
}

type ServerCaps struct {
	Tools     *ToolsCapability     `json:"tools,omitempty"`
	Resources *ResourcesCapability `json:"resources,omitempty"`
	Prompts   *PromptsCapability   `json:"prompts,omitempty"`

	Tasks   *TasksCapability `json:"tasks,omitempty"`
	Logging *struct{}        `json:"logging,omitempty"`

	Completions *struct{} `json:"completions,omitempty"`
}

func (c *Client) HasCompletion() bool {
	_, caps := c.negotiatedState()
	return caps.Completions != nil
}

type ToolsCapability struct {
	ListChanged bool `json:"listChanged,omitempty"`
}

type ResourcesCapability struct {
	Subscribe   bool `json:"subscribe,omitempty"`
	ListChanged bool `json:"listChanged,omitempty"`
}

type PromptsCapability struct {
	ListChanged bool `json:"listChanged,omitempty"`
}

type ListToolsParams struct {
	Cursor string `json:"cursor,omitempty"`
}

type CacheableResult struct {
	TTLms      int64  `json:"ttlMs,omitempty"`
	CacheScope string `json:"cacheScope,omitempty"`
}

type ListToolsResult struct {
	Tools []ToolDefinition `json:"tools"`

	NextCursor string `json:"nextCursor,omitempty"`
	CacheableResult
}

type ListPromptsResult struct {
	Prompts []PromptDefinition `json:"prompts"`

	NextCursor string `json:"nextCursor,omitempty"`
	CacheableResult
}

type PromptDefinition struct {
	Name        string           `json:"name"`
	Description string           `json:"description,omitempty"`
	Arguments   []PromptArgument `json:"arguments,omitempty"`
}

type PromptArgument struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Required    bool   `json:"required,omitempty"`
}

type GetPromptParams struct {
	Name      string                 `json:"name"`
	Arguments map[string]interface{} `json:"arguments,omitempty"`

	InputResponses map[string]json.RawMessage `json:"inputResponses,omitempty"`
	RequestState   string                     `json:"requestState,omitempty"`
}

type GetPromptResult struct {
	Description string          `json:"description,omitempty"`
	Messages    []PromptMessage `json:"messages"`
}

type PromptMessage struct {
	Role    string          `json:"role,omitempty"`
	Content json.RawMessage `json:"content"`
}

type ListResourcesResult struct {
	Resources []ResourceDefinition `json:"resources"`

	NextCursor string `json:"nextCursor,omitempty"`
	CacheableResult
}

type ResourceDefinition struct {
	URI         string `json:"uri"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	MIMEType    string `json:"mimeType,omitempty"`
}

type ReadResourceParams struct {
	URI string `json:"uri"`

	InputResponses map[string]json.RawMessage `json:"inputResponses,omitempty"`
	RequestState   string                     `json:"requestState,omitempty"`
}

type ReadResourceResult struct {
	Contents []ResourceContent `json:"contents"`
	CacheableResult
}

type ResourceContent struct {
	URI      string `json:"uri,omitempty"`
	MIMEType string `json:"mimeType,omitempty"`
	Text     string `json:"text,omitempty"`
	Blob     string `json:"blob,omitempty"`
}

type ToolDefinition struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"inputSchema"`

	Annotations *ToolAnnotations `json:"annotations,omitempty"`
}

type ToolAnnotations struct {
	Title           string `json:"title,omitempty"`
	ReadOnlyHint    *bool  `json:"readOnlyHint,omitempty"`
	DestructiveHint *bool  `json:"destructiveHint,omitempty"`
	IdempotentHint  *bool  `json:"idempotentHint,omitempty"`
	OpenWorldHint   *bool  `json:"openWorldHint,omitempty"`
}

type CallToolParams struct {
	Name      string                 `json:"name"`
	Arguments map[string]interface{} `json:"arguments,omitempty"`

	InputResponses map[string]json.RawMessage `json:"inputResponses,omitempty"`
	RequestState   string                     `json:"requestState,omitempty"`

	Task TaskRequestOptions `json:"task,omitempty"`
}

type CallToolResult struct {
	Content []ToolContent `json:"content"`
	IsError bool          `json:"isError,omitempty"`

	StructuredContent json.RawMessage `json:"structuredContent,omitempty"`
}

type ToolContent struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}
