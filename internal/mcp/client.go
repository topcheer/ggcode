package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
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

// Client connects to an MCP server via stdio transport.
type Client struct {
	name              string
	transport         string
	command           string
	args              []string
	env               map[string]string
	url               string
	headers           map[string]string
	cmd               *exec.Cmd
	procCancel        context.CancelFunc
	stdin             io.WriteCloser
	stdout            io.Reader
	reader            *bufio.Reader // reused stdout reader
	httpClient        *http.Client
	wsMu              sync.Mutex                // serializes ReadMessage on wsConn (fix #138)
	readMu            sync.Mutex                // serializes reads on the shared stdio bufio.Reader and response matching (fix #156)
	waiters           map[string]chan *Response // stdio response waiters keyed by request ID JSON (guarded by mu)
	wsConn            *websocket.Conn
	sessionID         string
	negotiatedVersion string // protocol version agreed upon during initialize
	mu                sync.Mutex
	stderrMu          sync.RWMutex
	stderrBuf         strings.Builder
	abortOnce         sync.Once
	nextID            atomic.Int64
	closed            atomic.Bool
	// httpNotifDisabled permanently stops the standalone HTTP GET SSE
	// stream after the server answered 405 (spec-allowed: servers MAY not
	// offer the stream) or a non-SSE 200 body. Safe to flip from the stream
	// goroutine; read before each (re)start.
	httpNotifDisabled atomic.Bool
	// notifStreamCancel cancels the standalone GET SSE stream's request
	// context. Without it the stream's resp.Body read blocks until the
	// server speaks again, pinning the connection (and test servers' Close)
	// long after the client closed. Set by startHTTPNotificationStream,
	// cancelled by Close. Guarded by mu.
	notifStreamCancel context.CancelFunc
	// #1275: hang-watchdog state. lastReadProgress is bumped (unix nano)
	// after every successfully parsed stdio message; hangWatchdogArmed
	// dedupes the watchdog across concurrent request timeouts.
	lastReadProgress  atomic.Int64
	hangWatchdogArmed atomic.Bool
	// hangAbort marks a teardown initiated by the #1275 watchdog (as opposed
	// to user Close/Abort). procWatch still closes processExit for watchdog
	// teardowns so the plugin reconnect watcher can restore service - a user
	// abort stays silent, but a hung-server abort is exactly the situation
	// auto-reconnect exists for.
	hangAbort    atomic.Bool
	oauthHandler *OAuthHandler

	// processExit is closed when the stdio server process exits unexpectedly
	// (i.e., not via Close/Abort). Consumers can use this for auto-reconnect.
	// It is closed at most once; nil for non-stdio transports.
	processExit  chan struct{}
	procWaitDone chan struct{} // closed when the procWatch goroutine's cmd.Wait returns (#292 race fix)

	// notificationHandler is called for every server-initiated notification
	// (e.g., notifications/tools/list_changed, notifications/message).
	// If nil, notifications are silently dropped (legacy behavior).
	notificationHandler func(method string, params json.RawMessage)

	// Notification dispatch (fix #255): handlers are invoked asynchronously
	// from a dedicated goroutine so they can safely call back into the client
	// (e.g. refreshTools → ListTools → sendRequest) even while the stdio read
	// loop holds readMu. The channel is FIFO, preserving notification order.
	notificationCh   chan *Notification
	notificationOnce sync.Once
	notificationDone chan struct{}

	// samplingHandler processes sampling/createMessage requests from the
	// server. If nil, sampling requests are rejected with an error.
	samplingHandler SamplingHandler

	// elicitationHandler processes elicitation/create requests from the
	// server (MCP 2025-06-18+). If nil, elicitation requests are rejected
	// with an error.
	elicitationHandler ElicitationHandler

	// serverCaps holds the capabilities advertised by the server during
	// initialize. Used to gate feature-specific requests (e.g., logging).
	// Both serverCaps and negotiatedVersion are written by Initialize and read
	// by capability gates / reconnect paths from other goroutines — all access
	// must go through negotiatedState/setNegotiatedState (c.mu-guarded, #562 F).
	serverCaps ServerCaps

	// serverReqSem bounds goroutines spawned for interactive server requests
	// (sampling/elicitation) dispatched off the read loop (#562 C). Lazily
	// initialized under serverReqOnce so struct-literal clients are safe too.
	serverReqOnce sync.Once
	serverReqSem  chan struct{}
}

// negotiatedState returns the protocol version and server capabilities
// negotiated during Initialize, synchronized with c.mu (#562 Bug F:
// Initialize's write raced HasToolsListChanged's read — a precise TSan
// report — and reconnect re-entry could observe torn state).
func (c *Client) negotiatedState() (string, ServerCaps) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.negotiatedVersion, c.serverCaps
}

// setNegotiatedState records the negotiated version and server capabilities
// under c.mu (companion to negotiatedState, #562 Bug F).
func (c *Client) setNegotiatedState(version string, caps ServerCaps) {
	c.mu.Lock()
	c.negotiatedVersion = version
	c.serverCaps = caps
	c.mu.Unlock()
}

// NewClient creates a new MCP client for the given server config.
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
		transport = "stdio"
	}
	client := &Client{
		name:             cfg.Name,
		transport:        transport,
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
	return client
}

// Start launches the MCP server process.
func (c *Client) Start(ctx context.Context) error {
	switch c.transport {
	case "http":
		c.httpClient = newMCPHTTPClient(0) // no client-level timeout; per-request context used
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
		c.wsConn = conn
		return nil
	case "", "stdio":
	default:
		return fmt.Errorf("mcp[%s]: unsupported transport %q", c.name, c.transport)
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
	safego.Go("mcp.captureStderr", func() { c.captureStderr(stderr) })

	if err := cmd.Start(); err != nil {
		cancelProc()
		return fmt.Errorf("mcp[%s]: starting server: %w", c.name, err)
	}

	c.mu.Lock()
	c.cmd = cmd
	c.procCancel = cancelProc
	c.stdin = stdin
	c.stdout = stdout
	c.reader = bufio.NewReader(stdout)
	c.processExit = make(chan struct{})
	c.procWaitDone = make(chan struct{})
	c.mu.Unlock()

	// Monitor process exit. When the process dies on its own (not via
	// Close/Abort), close the processExit channel so watchers can react.
	safego.Go("mcp.procWatch", func() {
		_ = cmd.Wait()
		// Always signal that the single legal Wait() has returned so Close can
		// observe process teardown without calling Wait() again (concurrent
		// exec.Cmd.Wait is a data race — #292).
		close(c.procWaitDone)
		// Signal teardown to watchers when the exit was NOT user-initiated
		// (#1275 review): user Close/Abort stays silent, but a hang-watchdog
		// abort sets hangAbort first - a torn-down hung server is exactly
		// what auto-reconnect exists to recover from.
		if !c.closed.Load() || c.hangAbort.Load() {
			c.closed.Store(true)
			close(c.processExit)
			debug.Log("mcp-procwatch", "server=%s process exited (watchdog=%v)", c.name, c.hangAbort.Load())
		}
	})

	return nil
}

// latestMCPProtocolVersion is the latest MCP protocol version this client
// supports. The client sends this during initialize; the server may negotiate
// down to an older version.
const latestMCPProtocolVersion = "2025-11-25"

// knownMCPProtocolVersions lists all protocol versions this client accepts.
// If the server negotiates to a version not in this set, initialization fails.
var knownMCPProtocolVersions = map[string]bool{
	"2024-11-05": true,
	"2025-03-26": true,
	"2025-06-18": true,
	"2025-11-25": true,
}

// NegotiatedVersion returns the MCP protocol version agreed upon during
// initialize, or empty string if not yet initialized.
func (c *Client) NegotiatedVersion() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.negotiatedVersion
}

// Initialize sends the initialize request and returns server capabilities.
func (c *Client) Initialize(ctx context.Context) (*InitializeResult, error) {
	caps := ClientCaps{
		Roots: struct {
			ListChanged bool `json:"listChanged,omitempty"`
		}{ListChanged: true},
	}
	if c.samplingHandlerLocked() != nil {
		caps.Sampling = &struct{}{}
	}
	if c.elicitationHandlerLocked() != nil {
		caps.Elicitation = &struct{}{}
	}
	params := InitializeParams{
		ProtocolVersion: latestMCPProtocolVersion,
		Capabilities:    caps,
		ClientInfo:      Implementation{Name: "ggcode", Version: "0.1.0"},
	}
	var result InitializeResult
	if err := c.sendRequest(ctx, "initialize", params, &result); err != nil {
		return nil, fmt.Errorf("mcp[%s]: initialize: %w", c.name, err)
	}

	// Version negotiation: the server may respond with the same version
	// (if it supports ours) or a different one (its latest supported).
	// We accept any known version; reject unknown versions.
	serverVersion := result.ProtocolVersion
	if serverVersion == "" {
		return nil, fmt.Errorf("mcp[%s]: server returned empty protocolVersion", c.name)
	}
	if !knownMCPProtocolVersions[serverVersion] {
		return nil, fmt.Errorf("mcp[%s]: unsupported protocol version %q (client supports %s)",
			c.name, serverVersion, latestMCPProtocolVersion)
	}
	// Cache server capabilities for feature gating — under c.mu, together
	// with the negotiated version, so capability readers never observe a
	// torn half-updated state (#562 Bug F).
	c.setNegotiatedState(serverVersion, result.Capabilities)

	// Send initialized notification
	notif := Notification{
		JSONRPC: "2.0",
		Method:  "notifications/initialized",
	}
	if err := c.sendNotification(ctx, notif); err != nil {
		// #562 Bug G: the initialize handshake itself has already succeeded —
		// the version was negotiated and capabilities cached. A failed
		// initialized notification is a transport-level symptom (e.g. EPIPE
		// because the connection dropped right after the initialize response),
		// not an initialization failure; reporting it as such misleads
		// diagnosis. Downgrade to a warning — the next request will surface
		// the dead transport with its own context.
		debug.Log("mcp-client", "server=%s initialized notification send failed (handshake complete): %v", c.name, err)
	}

	// streamable HTTP only: open the standalone GET SSE channel so
	// server-initiated notifications (tools/list_changed etc.) reach us
	// while idle, not only while a POST response is streaming.
	c.startHTTPNotificationStream()

	return &result, nil
}

// maxPaginationPages bounds cursor-following in List* calls (#562 Bug A). A
// broken or hostile server that echoes back the same cursor forever must not
// turn pagination into an infinite request loop.
const maxPaginationPages = 100

// ListTools returns the tools provided by the MCP server.
//
// #562 Bug A: follows nextCursor pagination — servers were previously cut off
// after the first page, silently losing every tool on later pages.
// #562 Bug E: a server that did not advertise the tools capability now yields
// an empty list instead of an error, mirroring how ListPrompts/ListResources
// tolerate absent capabilities. Production callers gate all three behind one
// discoverCapabilities call, so a resources-only server previously failed the
// entire discovery.
func (c *Client) ListTools(ctx context.Context) ([]ToolDefinition, error) {
	// Preserve the closed-client error contract (see
	// TestClientErrorContext_ListToolsOnClosed) even when the capability gate
	// below would otherwise return early with an empty list.
	if c.closed.Load() {
		return nil, fmt.Errorf("mcp[%s]: connection closed", c.name)
	}
	_, caps := c.negotiatedState()
	if caps.Tools == nil {
		debug.Log("mcp-client", "server=%s tools capability not advertised; returning empty tool list", c.name)
		return []ToolDefinition{}, nil
	}
	var all []ToolDefinition
	cursor := ""
	for page := 0; page < maxPaginationPages; page++ {
		var result ListToolsResult
		if err := c.sendRequest(ctx, "tools/list", ListToolsParams{Cursor: cursor}, &result); err != nil {
			if len(all) > 0 {
				// Pages already collected are still valid; report them and note
				// the truncation rather than failing everything.
				debug.Log("mcp-client", "server=%s tools/list page %d failed after %d tools: %v", c.name, page+1, len(all), err)
				return all, nil
			}
			return nil, fmt.Errorf("mcp[%s]: tools/list: %w", c.name, err)
		}
		all = append(all, result.Tools...)
		if result.NextCursor == "" {
			return all, nil
		}
		cursor = result.NextCursor
	}
	debug.Log("mcp-client", "server=%s tools/list exceeded %d pages; stopping pagination", c.name, maxPaginationPages)
	return all, nil
}

func (c *Client) ListPrompts(ctx context.Context) ([]PromptDefinition, error) {
	var all []PromptDefinition
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
		if result.NextCursor == "" {
			return all, nil
		}
		cursor = result.NextCursor
	}
	debug.Log("mcp-client", "server=%s prompts/list exceeded %d pages; stopping pagination", c.name, maxPaginationPages)
	return all, nil
}

func (c *Client) ListResources(ctx context.Context) ([]ResourceDefinition, error) {
	var all []ResourceDefinition
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
		if result.NextCursor == "" {
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
	if err := c.sendRequest(ctx, "prompts/get", params, &result); err != nil {
		return nil, fmt.Errorf("mcp[%s]: prompts/get: %w", c.name, err)
	}
	return &result, nil
}

func (c *Client) ReadResource(ctx context.Context, uri string) (*ReadResourceResult, error) {
	params := ReadResourceParams{URI: uri}
	var result ReadResourceResult
	if err := c.sendRequest(ctx, "resources/read", params, &result); err != nil {
		return nil, fmt.Errorf("mcp[%s]: resources/read: %w", c.name, err)
	}
	return &result, nil
}

// CallTool invokes a tool on the MCP server.
func (c *Client) CallTool(ctx context.Context, name string, args map[string]interface{}) (*CallToolResult, error) {
	params := CallToolParams{
		Name:      name,
		Arguments: args,
	}
	var result CallToolResult
	if err := c.sendRequest(ctx, "tools/call", params, &result); err != nil {
		return nil, fmt.Errorf("mcp[%s]: tools/call %s: %w", c.name, name, err)
	}
	return &result, nil
}

// Close terminates the server process.
func (c *Client) Close() error {
	// #717: a stdio writer stuck on a full pipe (child stopped reading
	// stdin) holds c.mu with no deadline — the old lock-first order blocked
	// here forever, making the Abort() below unreachable, so Close()
	// deadlocked permanently whenever the child stopped draining stdin.
	// Abort is idempotent and lock-free (it is documented as callable with
	// c.mu held), so tear the transport down FIRST: closing stdin and
	// killing the process group unblocks any stuck writer, which then
	// releases c.mu and lets the bookkeeping below proceed. Abort also runs
	// the #643 cleanup (notification worker) on every path, and the #39
	// "capture before Abort" concern disappears because the closed flag no
	// longer gates any cleanup here.
	c.Abort()

	c.mu.Lock()
	cmd := c.cmd
	transport := c.transport
	c.sessionID = ""
	c.httpClient = nil
	c.procCancel = nil
	notifCancel := c.notifStreamCancel
	waitDone := c.procWaitDone
	oauthHandler := c.oauthHandler
	c.oauthHandler = nil
	c.mu.Unlock()

	// Cancel the standalone GET SSE stream first so its body read unblocks
	// immediately instead of pinning the connection until the server speaks.
	if notifCancel != nil {
		notifCancel()
	}

	if oauthHandler != nil {
		oauthHandler.Close()
	}
	if (transport == "stdio" || transport == "") && cmd != nil {
		// Wait for the process to exit WITHOUT calling cmd.Wait() again — the
		// procWatch goroutine owns the single legal Wait() call, and concurrent
		// exec.Cmd.Wait invocations race on the Cmd's internal state (#292).
		if waitDone != nil {
			select {
			case <-waitDone:
			case <-time.After(3 * time.Second):
			}
		}
	}
	return nil
}

// ForceReauth deletes the server-name-specific OAuth credential (if any),
// leaving the canonical (shared) credential untouched. The next request will
// get a 401 and trigger a fresh OAuth flow bound to this server name.
func (c *Client) ForceReauth() error {
	c.mu.Lock()
	handler := c.oauthHandler
	c.mu.Unlock()
	if handler == nil {
		return nil
	}
	return handler.DeleteServerToken()
}

func (c *Client) Abort() {
	c.abortOnce.Do(func() {
		// Mark as closed atomically — Abort may be called from
		// sendRequest's cancel path which already holds c.mu.
		// atomic.Bool avoids both the data race and the deadlock.
		c.closed.Store(true)

		wsConn := c.wsConn
		stdin := c.stdin
		cmd := c.cmd
		procCancel := c.procCancel

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
			// Kill the entire process group to prevent orphaned child processes.
			killProcessGroup(cmd)
		}

		// Stop the notification dispatch worker (fix #255). The read loop is
		// gone once the transport/process is torn down, so no new
		// notifications can be queued after this point. notificationDone is
		// initialized in the constructors, so this read is race-free (#292).
		if c.notificationDone != nil {
			close(c.notificationDone)
		}
	})
}

// Name returns the MCP server name.
func (c *Client) Name() string { return c.name }

// ProcessExit returns a channel that is closed when the stdio server process
// exits unexpectedly. Returns nil for non-stdio transports or if the process
// hasn't started yet. Used by the auto-reconnect watcher.
func (c *Client) ProcessExit() <-chan struct{} {
	return c.processExit
}

// IsClosed returns whether the client has been closed (either explicitly or
// because the underlying process exited).
func (c *Client) IsClosed() bool {
	return c.closed.Load()
}

func (c *Client) nextRequestID() *ID {
	id := c.nextID.Add(1)
	i := NewIntID(id)
	return &i
}

// mcpRequestTimeout is the per-request deadline for all MCP requests
// (HTTP, WebSocket, and stdio). Prevents indefinite hangs when a server
// accepts the connection but never responds.
const mcpRequestTimeout = 120 * time.Second

// maxHeaderContentLength bounds the body allocation in
// readHeaderFramedMessage. A crashed or malicious stdio server can emit an
// arbitrary Content-Length header; without a cap, make() either OOMs the
// process or panics (makeslice), leaving sendRequest dead-locked (#182).
const maxHeaderContentLength = 16 << 20 // 16MB

// maxNDJSONLineLength bounds a single newline-delimited JSON message read by
// readMessage's NDJSON branch (#643, sister gap of #182). A crashed or
// malicious stdio server can emit `{` followed by an unbounded stream with no
// newline; an unbounded ReadBytes would grow the buffer without limit and OOM
// the client. Aligned with maxHeaderContentLength — far above any legitimate
// MCP message, and NDJSON is the default stdio framing so this is the main
// read path.
const maxNDJSONLineLength = 16 << 20 // 16MB

// readBoundedLine reads one '\n'-terminated line from r, failing once the
// accumulated line exceeds max bytes (#643). bufio.Reader.ReadBytes has no
// length limit, so we accumulate ReadSlice chunks and abort as soon as the
// cap is crossed — the unbounded alternative lets a newline-less attacker
// stream drive the buffer to OOM.
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

	// Apply per-request timeout so a slow/hung MCP server can't block forever.
	// If the caller's ctx already has a shorter deadline, that takes priority.
	ctx, cancel := context.WithTimeout(ctx, mcpRequestTimeout)
	defer cancel()

	paramsJSON, err := json.Marshal(params)
	if err != nil {
		return fmt.Errorf("mcp[%s]: marshal params for %s: %w", c.name, method, err)
	}

	req := Request{
		JSONRPC: "2.0",
		Method:  method,
		Params:  paramsJSON,
		ID:      c.nextRequestID(),
	}

	// For WS/stdio transports, the send path includes a read loop that may
	// dispatch notifications. Those notification handlers can call back into
	// sendRequest (e.g. ListTools after tools/list_changed). If we hold c.mu
	// during the read loop, this reentrant call deadlocks. So we split:
	// 1) write under c.mu, 2) read without c.mu, using wsMu to serialize reads.
	resp, err := c.sendRequestUnlocked(req, ctx)
	if err != nil {
		return fmt.Errorf("mcp[%s]: send %s: %w", c.name, method, err)
	}

	if resp.IsError() {
		return resp.Error
	}

	if result != nil && resp.Result != nil {
		if err := json.Unmarshal(resp.Result, result); err != nil {
			return fmt.Errorf("mcp[%s]: unmarshal result: %w", c.name, err)
		}
	}

	return nil
}

// sendRequestUnlocked sends a request and reads the response. c.mu is held
// only during the write phase, not the read phase (fix #138). The stdio read
// path is serialized by readMu in readResponseWithCancel (fix #156) so that
// concurrent sendRequest calls cannot interleave reads on the shared
// bufio.Reader.
func (c *Client) sendRequestUnlocked(req Request, ctx context.Context) (*Response, error) {
	switch c.transport {
	case "ws", "websocket":
		return c.sendWSUnlocked(ctx, req)
	case "http":
		// Bug B (#523): the HTTP roundtrip must NOT hold c.mu — Close()
		// takes c.mu first and would block until the request (or its OAuth
		// 401 retry, which can take minutes of interactive time) finishes.
		// sendHTTP now locks c.mu only for short state snapshot/write-backs.
		return c.sendHTTP(ctx, req)
	default: // stdio
		// #994: register the waiter BEFORE the request hits the wire, mirroring
		// the WS path (Bug A, #523). The old ordering (write under c.mu, then
		// register inside readResponseWithCancel) left an unlocked gap between
		// write completion and registration: a concurrent caller's read loop
		// could consume our response and deliverResponse would find no waiter
		// ("dropping response with unknown ID"), leaving this request to
		// idle-wait the full mcpRequestTimeout and — as the sole waiter — Abort
		// the shared connection other callers were still using.
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

func (c *Client) sendNotification(ctx context.Context, notif Notification) error {
	// Bug B (#523): for HTTP the roundtrip must run WITHOUT c.mu, otherwise
	// Close() blocks behind it exactly like the request path did.
	if c.transport == "http" {
		if c.closed.Load() {
			return fmt.Errorf("mcp[%s]: connection closed", c.name)
		}
		_, err := c.sendHTTP(ctx, notif)
		return err
	}
	// #562 Bug B: the WS path now serializes its write under c.mu inside
	// sendWSNotification, so this dispatcher must NOT already hold c.mu when
	// handing off — the old generic path locked here and delegated to c.send,
	// which would now reentrantly deadlock.
	if c.transport == "ws" || c.transport == "websocket" {
		_, err := c.sendWSNotification(ctx, notif)
		return err
	}
	// stdio: frame serialization requires c.mu (#480).
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
	// #994: the real request path now registers the waiter in
	// sendRequestUnlocked BEFORE the write; this self-registering legacy
	// entry remains for direct callers (tests) that already own a request.
	var waiter chan *Response
	if reqID != nil {
		waiter = make(chan *Response, 1)
		c.registerWaiter(reqID, waiter)
		defer c.unregisterWaiter(reqID, waiter)
	}
	return c.readResponseWithWaiter(ctx, reqID, waiter)
}

// readResponseWithWaiter is the stdio read phase for a waiter the caller
// registered BEFORE writing the request (#994, same ordering rule as the WS
// path in sendWSUnlocked). Unregistration is idempotent (unregisterWaiter
// matches on the channel) and happens on three paths: caller-side defer in
// sendRequestUnlocked, the ctx.Done early-unregister below (#652), and the
// read goroutine's defer.
func (c *Client) readResponseWithWaiter(ctx context.Context, reqID *ID, waiter chan *Response) (*Response, error) {
	type result struct {
		resp *Response
		err  error
	}
	// The waiter is registered SYNCHRONOUSLY by the caller before the write
	// (fix #156, tightened by #994): another caller holding readMu may read
	// our response as soon as our request hits the wire, so the waiter must
	// already be in c.waiters by the time the write completes.
	done := make(chan result, 1)
	safego.Go("mcp.client.readResponse", func() {
		if waiter != nil {
			defer c.unregisterWaiter(reqID, waiter)
		}
		// Serialize the whole read loop (fix #156): concurrent sendRequest
		// calls on the same stdio client must not interleave Peek/ReadBytes/
		// ReadString calls on the shared bufio.Reader (not concurrency-safe).
		c.readMu.Lock()
		defer c.readMu.Unlock()
		resp, err := c.readResponse(ctx, reqID, waiter)
		done <- result{resp: resp, err: err}
	})
	select {
	case res := <-done:
		if err := ctx.Err(); err != nil {
			return nil, c.withStderr(err)
		}
		return res.resp, res.err
	case <-ctx.Done():
		// #652: the waiter must be unregistered as soon as this request gives
		// up, NOT when the read goroutine exits. The goroutine is parked behind
		// readMu in Peek(1) when the server hangs, and readMessage's ctx check
		// is unreachable there — deferring the unregistration to goroutine exit
		// leaves a ghost waiter forever, which keeps hasOtherWaiters true for
		// every later request and permanently blocks the only Abort path.
		if waiter != nil {
			c.unregisterWaiter(reqID, waiter)
		}
		// #644: a single request's ctx timeout must not tear down the whole
		// stdio connection while other requests are still in flight — that
		// kills their healthy in-flight calls and permanently closes the
		// client. Abort only when we are the last (or only) waiter; otherwise
		// fail just this request and leave the shared transport alone.
		if !c.hasOtherWaiters(waiter) {
			c.Abort()
			// The read goroutine normally delivers on done right after Abort,
			// but if it panicked before reaching done <- result (recovered by
			// safego.Go), a bare <-done would block forever (#182). Bound the
			// wait so the caller gets the ctx error instead of hanging.
			select {
			case res := <-done:
				if err := ctx.Err(); err != nil {
					return nil, c.withStderr(err)
				}
				return res.resp, res.err
			case <-time.After(5 * time.Second):
				return nil, c.withStderr(fmt.Errorf("mcp[%s]: read goroutine did not return after abort: %w", c.name, ctx.Err()))
			}
		}
		// Other waiters are active: return this request's ctx error now. The
		// read goroutine still owns readMu and unwinds on its own once the
		// server responds or the shared connection is torn down later.
		//
		// #1275: "later" used to mean never under sustained traffic — every
		// subsequent request kept a waiter registered, so no timeout ever saw
		// itself as the last waiter and the Abort path stayed permanently
		// blocked: parked read goroutines accumulated and the hung connection
		// never healed. Arm the hang watchdog: if the connection reads NO
		// message at all during the grace window while requests are still
		// waiting, it is hung (not merely slow — a live server reads messages
		// constantly), and Abort heals it for everyone.
		c.armHangWatchdog()
		return nil, c.withStderr(fmt.Errorf("mcp[%s]: request cancelled while %d other request(s) in flight, connection kept: %w",
			c.name, c.waiterCountExcluding(waiter), ctx.Err()))
	}
}

// hungServerAbortGrace is how long the #1275 watchdog waits for ANY read
// progress on a stdio connection that still has waiters before declaring it
// hung and aborting. A live-but-slow server keeps reading messages (progress
// bumps disarm the watchdog); 10s without a single byte parsed with requests
// pending is a hang.
const hungServerAbortGrace = 10 * time.Second

// armHangWatchdog starts (once) the #1275 hang watchdog. It snapshots the
// read-progress counter, waits the grace period, and aborts the connection
// iff NO progress was made AND requests are still waiting. Abort is
// idempotent (abortOnce) and safe when the connection is already gone.
func (c *Client) armHangWatchdog() {
	if !c.hangWatchdogArmed.CompareAndSwap(false, true) {
		return // one watchdog at a time
	}
	progressAtArm := c.lastReadProgress.Load()
	safego.Go("mcp.client.hangWatchdog", func() {
		defer c.hangWatchdogArmed.Store(false)
		time.Sleep(hungServerAbortGrace)
		if c.closed.Load() {
			return // already torn down normally; nothing to heal
		}
		if c.lastReadProgress.Load() == progressAtArm && c.waiterCountExcluding(nil) > 0 {
			// Hung: zero messages parsed during the entire grace window while
			// requests were still waiting. Abort unwinds every goroutine parked on
			// readMu or blocked in a raw body read (the only way to interrupt a
			// pipe read is closing it). hangAbort makes procWatch close
			// processExit, so the plugin reconnect watcher restores service
			// (#1275 review: a plain Abort is treated as user-initiated and
			// stays silent).
			c.hangAbort.Store(true)
			c.Abort()
		}
	})
}

// hasOtherWaiters reports whether any request other than the one owning the
// self channel still has a registered response waiter (#644). self may be
// nil (caller has no waiter, e.g. a nil reqID) — then any registered waiter
// counts as other.
func (c *Client) hasOtherWaiters(self chan *Response) bool {
	return c.waiterCountExcluding(self) > 0
}

// waiterCountExcluding returns the number of registered waiters whose channel
// differs from self (#644).
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

// writeMessage serializes the write under c.mu (#480) — all stdin
// writers (request sends AND server-request responses from the read
// loop) must go through this, because POSIX write atomicity stops at
// PIPE_BUF (512B macOS) and interleaved large frames corrupt NDJSON.
func (c *Client) writeMessage(msg interface{}) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.writeMessageUnlocked(msg)
}

// writeMessageUnlocked writes WITHOUT taking c.mu — callers MUST already
// hold it (sendRequestUnlocked, sendNotification).
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

// mcpStdioWriteTimeout (#717) bounds a single stdin write. A child that
// stopped reading stdin leaves the pipe full; a plain c.stdin.Write then
// blocks forever while the caller holds c.mu, so Close() — which needs
// c.mu — deadlocked permanently and Abort() was unreachable. A var (not a
// const) so tests can shorten it.
var mcpStdioWriteTimeout = 15 * time.Second

// writeStdinWithDeadline writes data to the child's stdin with a deadline.
// The caller holds c.mu; serialization is unchanged because the write
// still happens one-at-a-time under the lock. On timeout it calls Abort()
// (lock-free and idempotent — documented as callable with c.mu held):
// closing stdin and killing the process group closes the pipe's read end,
// which makes the kernel fail the stuck Write (EPIPE) so the writer
// goroutine exits instead of leaking.
func (c *Client) writeStdinWithDeadline(data []byte) error {
	stdin := c.stdin
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
		// Deadline exceeded — the child is not draining stdin. Tear the
		// transport down so this writer (and Close()) can make progress.
		c.Abort()
		select {
		case err := <-writeDone:
			if err == nil {
				// Write landed exactly as the deadline fired; report failure
				// anyway — the transport is now aborted.
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
	// Snapshot shared state under c.mu, then run the whole network roundtrip
	// WITHOUT the lock (Bug B, #523). Previously the caller held c.mu across
	// httpClient.Do + Handle401 (interactive OAuth can take minutes) + the 401
	// retry, so Close() — whose first action is c.mu.Lock() — blocked until the
	// request finished, contradicting its own contract that transports are
	// aborted "without holding c.mu".
	c.mu.Lock()
	httpClient := c.httpClient
	sessionID := c.sessionID
	headers := c.headers
	oauthHandler := c.oauthHandler
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
	if sessionID != "" {
		req.Header.Set("Mcp-Session-Id", sessionID)
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
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("mcp[%s]: http request: %w", c.name, err)
	}
	defer resp.Body.Close()
	if newSession := strings.TrimSpace(resp.Header.Get("Mcp-Session-Id")); newSession != "" {
		c.mu.Lock()
		c.sessionID = newSession
		c.mu.Unlock()
	}
	// #597 M1 + #716: for success-status SSE responses, stream-parse at
	// event boundaries instead of draining to EOF — spec-compliant servers
	// may keep the stream open after the Response event.
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
		// #1602: a session-bearing streamable-http server that restarted
		// 404s every POST (the session died with the old process) and the
		// http transport has NO reconnect watcher (exitCh==nil in the
		// loader's startReconnectWatcher) - the client stayed Connected
		// and every call failed until a manual reload. Drop the stale
		// session and re-initialize once, then replay the request.
		debug.Log("mcp-http", "server=%s 404 with session - dropping stale session and re-initializing", c.name)
		c.mu.Lock()
		c.sessionID = ""
		c.mu.Unlock()
		if _, err := c.Initialize(ctx); err != nil {
			return nil, fmt.Errorf("mcp[%s]: re-init after 404: %w", c.name, err)
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
	// #597 M1: the request's JSON-RPC id makes the HTTP/SSE/NDJSON parsers
	// only accept OUR response — concurrent streamable-HTTP requests share
	// response streams, and the first parseable Response previously won
	// (cross-request tool-output injection).
	return parseHTTPResponseForID(body, contentType, requestIDOf(msg))
}

// requestIDOf extracts the JSON-RPC id from a request message (nil for
// notifications / unknown shapes).
func requestIDOf(msg interface{}) *ID {
	switch typed := msg.(type) {
	case *Request:
		return typed.ID // Request.ID is already *ID
	case Request:
		return typed.ID
	}
	return nil
}

// tryStreamSSEResponse (#716) handles success-status SSE responses by
// streaming: it parses events off the live response stream and returns as
// soon as the reqID-matching Response arrives, instead of draining the body
// to EOF first. Spec-compliant servers may keep the SSE stream open after
// the Response event (notification-push gateways) — the old drain-to-EOF
// blocked every request for the full mcpRequestTimeout on such servers.
// Notifications received before the Response are routed through
// processNotification instead of being discarded. handled=false means the
// response was not a success-status SSE stream and the caller must fall
// through to the buffered body path.
func (c *Client) tryStreamSSEResponse(resp *http.Response, msg interface{}) (streamed *Response, handled bool, err error) {
	contentType := resp.Header.Get("Content-Type")
	if resp.StatusCode >= 400 || !strings.Contains(strings.ToLower(contentType), "text/event-stream") {
		return nil, false, nil
	}
	if _, isNotif := msg.(Notification); isNotif {
		// Fire-and-forget: never wait for the server's stream.
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

// handleHTTPAuthChallenge processes a 401/403 response when an OAuth handler
// is configured. 401 = no auth; 403 = auth present but insufficient
// permissions. Both trigger OAuth DCR discovery as a fallback — the user's
// configured API key may have limited scope while OAuth grants broader
// access. When handled is true the caller must return (retried, err)
// verbatim; when false (no handler, or challenge not actionable) the caller
// proceeds with normal status handling.
func (c *Client) handleHTTPAuthChallenge(ctx context.Context, msg interface{}, resp *http.Response, authHeader string, allowRetry bool, oauthHandler *OAuthHandler) (retried *Response, handled bool, err error) {
	if oauthHandler == nil {
		return nil, false, nil
	}
	needsOAuth, _ := oauthHandler.Handle401(resp)
	if allowRetry {
		if token, _ := oauthHandler.GetAccessToken(ctx); token != "" && "Bearer "+token != authHeader {
			// OAuth succeeded — permanently switch auth mode by removing the
			// user-configured API key header so future requests use OAuth token
			// only. Clone-then-swap under c.mu (Bug B, #523): concurrent senders
			// hold a snapshot of the old map and range over it outside the lock.
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

// sendWSUnlocked writes a request under c.mu, then reads the response
// without holding c.mu (fix #138). This prevents reentrant deadlock when
// a notification handler calls back into sendRequest. wsMu serializes
// ReadMessage calls to avoid concurrent reads on the non-thread-safe
// websocket connection.
func (c *Client) sendWSUnlocked(ctx context.Context, req Request) (*Response, error) {
	data, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("mcp[%s]: marshal ws message: %w", c.name, err)
	}
	// Register the waiter BEFORE the write hits the wire (Bug A, #523 — same
	// ordering rule as the stdio path in fix #156): the moment WriteMessage
	// returns, another caller's read loop may consume and route our response,
	// so the waiter must already be in place.
	var waiter chan *Response
	if req.ID != nil {
		waiter = make(chan *Response, 1)
		c.registerWaiter(req.ID, waiter)
		defer c.unregisterWaiter(req.ID, waiter)
	}
	// Write under c.mu to serialize WS writes.
	c.mu.Lock()
	if c.closed.Load() {
		c.mu.Unlock()
		return nil, fmt.Errorf("mcp[%s]: connection closed", c.name)
	}
	// #994: wsConn may legitimately be nil here (sendWSNotification checks the
	// same state under c.mu); dereferencing it panicked with no safego recovery.
	if c.wsConn == nil {
		c.mu.Unlock()
		return nil, fmt.Errorf("mcp[%s]: websocket connection not established", c.name)
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = c.wsConn.SetWriteDeadline(deadline)
	}
	if err := c.wsConn.WriteMessage(websocket.TextMessage, data); err != nil {
		c.mu.Unlock()
		return nil, fmt.Errorf("mcp[%s]: websocket write: %w", c.name, err)
	}
	c.mu.Unlock()
	// Read loop — NOT holding c.mu so notification handlers can re-enter
	// sendRequest without deadlock (fix #138).
	return c.readWSResponse(ctx, req.ID, waiter)
}

// readWSResponse is the WebSocket counterpart of readResponseWithCancel
// (Bug A, #523): responses belonging to other concurrent callers are routed
// to their registered waiter instead of being dropped, and a cancelled or
// timed-out caller aborts the connection so its read goroutine cannot pin
// wsMu until the read deadline expires.
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
		// #644: same gate as the stdio path — Abort closes wsConn, unblocking
		// the goroutine parked in ReadMessage inside wsMu, but only when no
		// other request is still waiting on this shared connection. A single
		// timed-out request must not kill concurrent healthy ones. Bounded
		// wait in case the goroutine never reaches done (safego-recovered
		// panic, cf. #182).
		if !c.hasOtherWaiters(waiter) {
			c.Abort()
			select {
			case res := <-done:
				return res.resp, res.err
			case <-time.After(5 * time.Second):
				return nil, fmt.Errorf("mcp[%s]: ws read goroutine did not return after abort: %w", c.name, ctx.Err())
			}
		}
		// Other waiters are active: fail only this request, keep the WS
		// connection; the read goroutine unwinds when the server responds or
		// the connection is legitimately torn down.
		return nil, fmt.Errorf("mcp[%s]: ws request cancelled while %d other request(s) in flight, connection kept: %w",
			c.name, c.waiterCountExcluding(waiter), ctx.Err())
	}
}

// readWSLoop reads messages until the response for reqID arrives. Foreign
// responses are handed to their waiter via deliverResponse (Bug A, #523);
// the old code logged "dropping mismatched response ID" and consumed the
// message, which guaranteed the other concurrent caller a false
// mcpRequestTimeout of 120s. The ENTIRE loop — waiter poll, ReadMessage,
// and delivery — runs under wsMu, mirroring how the stdio path holds readMu
// across its whole loop (fix #156). This is race-free by construction:
// deliveries happen only while the reader holds wsMu, so a waiter entry is
// either visible to the owner's pre-read poll (delivered before it acquired
// the lock) or cannot appear until it releases the lock — there is no gap
// where an owner parks in ReadMessage with an already-delivered response
// sitting unconsumed. Lock ordering is one-directional (wsMu→c.mu via
// deliverResponse/respondToServerRequestWS; the write path takes only c.mu),
// so this cannot deadlock. wsMu is therefore per-read-loop, not
// per-ReadMessage, as of #523.
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
			_ = c.wsConn.SetReadDeadline(deadline)
		}
		_, payload, err := c.wsConn.ReadMessage()
		if err != nil {
			return nil, fmt.Errorf("mcp[%s]: websocket read: %w", c.name, err)
		}
		parsed, err := ParseMessage(payload)
		if err != nil {
			return nil, fmt.Errorf("mcp[%s]: parse ws message: %w", c.name, err)
		}
		switch typed := parsed.(type) {
		case *Response:
			// #562 Bug D: same unattributable-response rule as the stdio loop —
			// responseIDMatches treats an empty raw ID as a match for ANY reqID,
			// so without this pre-check an id:null error would be misattributed
			// to whichever caller's read loop consumed it.
			if isNullID(typed.ID) {
				c.deliverResponse(typed)
				continue
			}
			// Under concurrent requests a response may belong to a different
			// caller. Route foreign responses to their waiter instead of
			// dropping them (Bug A, #523 — mirrors the stdio fix #156).
			if reqID != nil && !responseIDMatches(typed.ID, reqID) {
				c.deliverResponse(typed)
				continue
			}
			return typed, nil
		case *Notification:
			// processNotification only queues into a buffered channel (fix
			// #255) and never blocks, so it is safe to call while holding wsMu.
			c.processNotification(typed)
			continue
		case *Request:
			_ = c.respondToServerRequestWS(typed)
			continue
		}
	}
}

// sendWSNotification writes a notification over WebSocket without entering
// a read loop (notifications are fire-and-forget).
//
// #562 Bug B: the write is serialized under c.mu with a closed check, exactly
// like sendWSUnlocked and respondToServerRequestWS (the "#480 gorilla
// single-writer" rule). The previous unlocked WriteMessage raced concurrent
// request writes on the same connection — gorilla/websocket panics with
// "concurrent write to websocket connection", which is a process-level crash,
// not just a race-detector artifact (reproduced in an isolated process).
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
	if c.wsConn == nil {
		return nil, fmt.Errorf("mcp[%s]: websocket connection not established", c.name)
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = c.wsConn.SetWriteDeadline(deadline)
	}
	if err := c.wsConn.WriteMessage(websocket.TextMessage, data); err != nil {
		return nil, fmt.Errorf("mcp[%s]: websocket write: %w", c.name, err)
	}
	return &Response{JSONRPC: "2.0"}, nil
}

func parseHTTPResponse(body []byte, contentType string) (*Response, error) {
	return parseHTTPResponseForID(body, contentType, nil)
}

// parseHTTPResponseForID is the ID-aware form (#597 M1). reqID nil keeps
// the legacy any-match behavior.
func parseHTTPResponseForID(body []byte, contentType string, reqID *ID) (*Response, error) {
	payload := body
	if strings.Contains(strings.ToLower(contentType), "text/event-stream") {
		// Some MCP servers send Notification messages (e.g., logging/progress)
		// before the actual Response in the SSE stream. Parse all events and
		// return the first one that is a valid JSON-RPC Response.
		resp, err := extractSSEResponse(body)
		if err != nil {
			return nil, err
		}
		return resp, nil
	}
	// For non-SSE responses, also try the SSE extraction path as a fallback.
	// Some servers return content-type: application/json but actually send
	// newline-delimited JSON messages (Notification then Response) in a single
	// response body. If the first ParseMessage returns a Notification, try
	// extracting all SSE-style events to find the Response.
	msg, err := ParseMessage(payload)
	if err != nil {
		// Multi-message body (NDJSON) fails whole-body parsing — try fallbacks
		// before giving up, since the first line may parse fine.
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
	// First message was a Notification — try SSE extraction as fallback.
	debug.Log("mcp-http", "parseHTTPResponse: first message was %T, trying SSE fallback", msg)
	if r, err := extractSSEResponseForID(body, reqID); err == nil {
		return r, nil
	}
	// NDJSON fallback: some servers (or gateways in front of them) return
	// newline-delimited JSON messages — Notification first, then the Response.
	// SSE extraction finds nothing because there is no "data:" prefix.
	if r := extractNDJSONResponseForID(body, reqID); r != nil {
		debug.Log("mcp-http", "parseHTTPResponse: recovered Response via NDJSON fallback")
		return r, nil
	}
	// Non-JSON-RPC body (e.g. API gateway errors like {"code":1000,"msg":"..."})
	// parses as a Notification because it has no id/result fields. Surface the
	// original body instead of a misleading type error so auth failures are
	// immediately visible.
	if !isJSONRPCMessage(body) {
		return nil, fmt.Errorf("non-JSON-RPC response (content-type %s): %s", contentType, previewBody(body))
	}
	return nil, fmt.Errorf("expected response, got %T", msg)
}

// isJSONRPCMessage reports whether the body carries any JSON-RPC structure
// (jsonrpc, id, result, or error fields). Gateway error bodies have none.
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

// previewBody returns a trimmed, length-capped preview of a response body for
// inclusion in error messages.
func previewBody(body []byte) string {
	s := strings.TrimSpace(string(body))
	if len(s) > 300 {
		s = s[:300] + "..."
	}
	return s
}

// extractSSEResponse parses ALL SSE events from the body and returns the first
// one that parses as a JSON-RPC Response. This handles servers that send
// Notification messages (logging, progress) before the actual Response.
func extractSSEResponse(body []byte) (*Response, error) {
	return extractSSEResponseForID(body, nil)
}

// extractSSEResponseForID is the ID-matching form (#597 M1): skip Responses
// whose JSON-RPC id does not match reqID (mirrors stdio responseIDMatches
// #156 and the WS waiter routing #523 — the HTTP path was the only one
// without id matching; concurrent streamable-HTTP requests cross-injected
// tool output, probe: the waiter for id=222 received id=111's result).
// id:null responses match any caller (legacy routing).
func extractSSEResponseForID(body []byte, reqID *ID) (*Response, error) {
	events, scanErr := extractAllSSEDataChecked(body)
	if len(events) == 0 {
		if scanErr != nil {
			// #597 M2: a >1MB SSE line exceeds bufio's buffer cap — report the
			// real cause instead of misdiagnosing "no data event found".
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
				// Foreign response in a shared stream — keep looking for ours.
				debug.Log("mcp-http", "extractSSEResponse: skipping response with foreign id (concurrent stream)")
				continue
			}
			return resp, nil
		}
		// Skip notifications — keep looking for the Response
		debug.Log("mcp-http", "parseHTTPResponse: skipping non-response SSE event %T", msg)
	}
	if lastParseErr != nil {
		return nil, fmt.Errorf("parsing SSE response: no valid JSON-RPC message found: %w", lastParseErr)
	}
	// #605 G1: no Response matched among the parsed events AND the scanner
	// hit its buffer cap mid-stream (e.g. a notification first, then a >1MB
	// result line) — the truncation cause outranks the count-based message;
	// otherwise we repeat #597 M2's misdiagnosis one step later in the stream.
	if scanErr != nil {
		return nil, fmt.Errorf("parsing SSE response: %w", scanErr)
	}
	return nil, fmt.Errorf("parsing SSE response: no Response found in %d event(s)", len(events))
}

func extractAllSSEData(body []byte) [][]byte {
	events, _ := extractAllSSEDataChecked(body)
	return events
}

// extractAllSSEDataChecked also surfaces scanner errors (#597 M2): a line
// longer than the 1MB scanner buffer made Scan() stop silently and the old
// code reported "no data event found" — misdiagnosing an oversized (>1MB)
// tool result as a protocol error.
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

// streamHTTPSSEResponse (#716) is the streaming counterpart of
// extractSSEResponseForID: instead of buffering the whole body first
// (drain-to-EOF), it parses events off the live response stream as they
// arrive and returns as soon as the Response matching reqID is seen — so
// spec-compliant servers that keep the SSE stream open after the Response
// event (notification-push gateways) no longer block every request for the
// full mcpRequestTimeout. Notification events received along the way are
// routed through processNotification (handler + queued channel) instead of
// being discarded. Foreign-id responses in a shared stream are skipped
// (#597 M1 semantics preserved). reqID == nil accepts the first Response
// (legacy routing, mirrors extractSSEResponse).
func (c *Client) streamHTTPSSEResponse(r io.Reader, reqID *ID) (*Response, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 256*1024), 1024*1024)
	var dataLines []string
	var found *Response
	var lastParseErr error
	events := 0
	// flush assembles the pending data: lines into one SSE event payload and
	// parses it (#597 M1 id matching; #716 notification routing).
	flush := func() {
		if len(dataLines) == 0 {
			return
		}
		payload := []byte(strings.Join(dataLines, "\n"))
		dataLines = nil
		events++
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
			// #716: server notifications (e.g. tools/list_changed) riding the
			// POST response stream must reach the handler, not vanish with
			// the discarded body bytes.
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
		case strings.TrimSpace(line) == "":
			flush()
			if found != nil {
				return found, nil
			}
		}
	}
	scanErr := scanner.Err()
	if found == nil {
		flush() // trailing event not terminated by a blank line
	}
	if found != nil {
		return found, nil
	}
	// Error mirrors extractSSEResponseForID so diagnostics stay uniform
	// (#597 M2: truncation cause outranks the count-based message).
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

// httpNotifResult classifies how one standalone GET SSE attempt ended.
type httpNotifResult int

const (
	// httpNotifDrop: stream ended or network error - retry with backoff.
	httpNotifDrop httpNotifResult = iota
	// httpNotifUnsupported: server answered 405 or a non-SSE body. The MCP
	// streamable-HTTP spec allows servers to not offer the standalone
	// stream; stop permanently instead of hammering.
	httpNotifUnsupported
	// httpNotifSessionGone: 404 - the server expired our session. The
	// plugin-level reconnect cycle builds a fresh client (whose Initialize
	// starts a fresh stream), so this one stops.
	httpNotifSessionGone
	// httpNotifClosing: the client is closing; exit without retry.
	httpNotifClosing
)

// startHTTPNotificationStream opens the MCP streamable-HTTP standalone GET
// SSE channel (spec: "the client MAY issue a GET request ... to enable
// server-to-client messages"). Without it, server-initiated notifications
// such as tools/list_changed could only arrive while a POST response was
// actively streaming - a server that adds a tool while the client is idle
// had its notification silently lost. Idempotent: a no-op for non-HTTP
// transports, after Close, or once the server proved it does not offer
// the stream.
func (c *Client) startHTTPNotificationStream() {
	if c.transport != "http" || c.closed.Load() || c.httpNotifDisabled.Load() {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	c.mu.Lock()
	if c.notifStreamCancel != nil {
		// A previous stream is already running (e.g. Initialize retried);
		// keep the oldest ctx alive and skip spawning a second loop.
		c.mu.Unlock()
		cancel()
		return
	}
	c.notifStreamCancel = cancel
	c.mu.Unlock()
	safego.Go("mcp.client.httpNotifStream", func() {
		defer cancel()
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
			// httpNotifDrop: retry with backoff, capped at 60s. Wait in
			// slices so Close() is noticed within a second, not a full tick.
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

// readHTTPNotifStreamOnce performs one GET / SSE cycle: dial, stream events
// until the server closes or the client does, routing every Notification
// through the shared async dispatch (processNotification).
func (c *Client) readHTTPNotifStreamOnce(ctx context.Context) httpNotifResult {
	c.mu.Lock()
	httpClient := c.httpClient
	sessionID := c.sessionID
	headers := c.headers
	oauthHandler := c.oauthHandler
	url := c.url
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
	if sessionID != "" {
		req.Header.Set("Mcp-Session-Id", sessionID)
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
		// Other errors (401 pending token refresh, 5xx, ...) are treated as
		// transient: the POST path owns interactive auth and error surfacing.
		return httpNotifDrop
	}
	contentType := resp.Header.Get("Content-Type")
	if !strings.Contains(strings.ToLower(contentType), "text/event-stream") {
		// 200 with a non-SSE body means the server does not stream on GET.
		return httpNotifUnsupported
	}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	var dataLines []string
	flush := func() {
		if len(dataLines) == 0 {
			return
		}
		payload := []byte(strings.Join(dataLines, "\n"))
		dataLines = nil
		msg, err := ParseMessage(payload)
		if err != nil {
			debug.Log("mcp-http", "notif-stream server=%s unparsable event: %v", c.name, err)
			return
		}
		switch typed := msg.(type) {
		case *Notification:
			c.processNotification(typed)
		default:
			// Server-initiated Requests (sampling, elicitation) arriving on
			// the standalone stream are not wired through this reader yet;
			// log for diagnosis instead of silently dropping bytes.
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
		case strings.TrimSpace(line) == "":
			flush()
		}
	}
	// #1597-B: SSE spec (WHATWG 9.2.6) - on stream close a pending
	// non-empty buffer must be processed and dispatched. A server that
	// writes its last notification without a trailing blank line and
	// closes (or flushes an under-filled buffer) dropped that event
	// forever - the reconnect lands on a NEW connection. Flush the tail.
	flush()
	if err := scanner.Err(); err != nil {
		debug.Log("mcp-client", "http notif stream read error: %v", err)
	}
	if c.closed.Load() {
		return httpNotifClosing
	}
	return httpNotifDrop
}

// sleepUntilClosed waits for d, but wakes at most every second to check the
// client's closed flag so shutdown is not delayed by a long backoff tick.
// Returns false when the client closed during the wait.
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

// extractNDJSONResponse parses newline-delimited JSON bodies and returns the
// first message that parses as a JSON-RPC Response. Handles servers that send
// a Notification (e.g. logging) before the actual Response, without SSE framing.
func extractNDJSONResponse(body []byte) *Response {
	return extractNDJSONResponseForID(body, nil)
}

// extractNDJSONResponseForID is the ID-matching NDJSON form (#597 M1/M2).
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
		// #597 M2: surface the truncation cause instead of a silent nil.
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

// respondToServerRequestWS handles server-initiated requests received over
// WebSocket transport. Sends an error response since the agent doesn't support
// sampling/elicitation over WS (keeps the server from blocking indefinitely).
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
	// #480: gorilla/websocket requires a single writer — sendWSUnlocked
	// already serializes under c.mu ("Write under c.mu to serialize WS
	// writes"); this server-request response path must not violate it.
	c.mu.Lock()
	defer c.mu.Unlock()
	// #994: align with sendWSNotification's nil guard — respondToServerRequestWS
	// runs off the WS read loop where wsConn can already be nil (torn down), and
	// a nil WriteMessage panics inside a goroutine safego cannot make safe.
	if c.wsConn == nil {
		return fmt.Errorf("mcp[%s]: websocket connection not established", c.name)
	}
	return c.wsConn.WriteMessage(websocket.TextMessage, data)
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
		// #1275: any successfully parsed message proves the connection is
		// alive; the hang watchdog compares this counter to detect hangs.
		c.lastReadProgress.Store(time.Now().UnixNano())
		switch typed := msg.(type) {
		case *Response:
			// #562 Bug D: null/absent-ID responses are unattributable (they are
			// spec-mandated error replies to malformed requests). Route them to
			// deliverResponse (which logs the drop) instead of returning them as
			// the answer to whichever caller happens to hold readMu — the old
			// path misattributed server-level errors to an innocent request.
			if isNullID(typed.ID) {
				c.deliverResponse(typed)
				continue
			}
			// Under concurrent requests a response may belong to a different
			// caller. Forward foreign responses to their waiter instead of
			// misattributing them (fix #156).
			if reqID != nil && !responseIDMatches(typed.ID, reqID) {
				c.deliverResponse(typed)
				continue
			}
			return typed, nil
		case *Notification:
			c.processNotification(typed)
			continue
		case *Request:
			// #562 Bug C: dispatching inside the read loop runs while readMu is
			// held. Quick handlers (roots/list, ping, unknown method) answer
			// immediately and stay synchronous — but sampling/elicitation wait
			// for the USER for up to 5 minutes, during which every concurrent
			// request times out and Aborts the whole connection. Interactive
			// handlers are therefore deferred to a bounded goroutine and the
			// loop keeps reading; response routing is unaffected because every
			// response write goes through writeMessage (c.mu-serialized stdin
			// writes), regardless of which goroutine issues it.
			if err := c.dispatchServerRequest(typed); err != nil {
				return nil, fmt.Errorf("mcp[%s]: handle server request: %w", c.name, err)
			}
		default:
			return nil, c.withStderr(fmt.Errorf("unexpected MCP message type %T", msg))
		}
	}
}

// responseIDMatches reports whether a raw JSON-RPC response ID equals the
// given request ID (fix #156).
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

// deliverResponse hands a response belonging to another concurrent caller to
// that caller's waiter channel (fix #156).
//
// #562 Bug D: a response with a null/absent ID is legal JSON-RPC (the spec
// mandates "id": null on error responses to malformed requests). Such a
// response cannot be attributed to any request this client issued (our
// requests always carry integer IDs), so it is neither delivered to a waiter
// (misattribution) nor silently dropped — it is logged with its error detail
// so the loss is diagnosable.
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

// isNullID reports whether a raw JSON-RPC response ID is absent or the JSON
// literal null (#562 Bug D). Such responses are unattributable.
func isNullID(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null"))
}

// idStateDesc describes a null/empty response ID for drop logging (#562 D).
func idStateDesc(raw json.RawMessage) string {
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return "null"
	}
	return "empty"
}

// responseErrorDesc extracts a compact error description for drop logging
// (#562 D); returns "<no error>" for success payloads.
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
			// #643: bound the line — ReadBytes grows the buffer without limit and
			// a newline-less server stream OOMs the client (sister of #182).
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

		// #771: the raw ReadString had no size cap -- a runaway stdio server
		// (or even '\r'-flushed spinner output) grew the buffer unboundedly. All
		// sibling framing paths already cap lines; legal headers are tiny.
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

	// Sanity bound before allocating (#182): a malicious or crashed server
	// can declare an absurd Content-Length. make() with a huge value either
	// OOMs the process (multi-GB allocation) or panics with makeslice — and
	// the panic leaves sendRequest blocked on a bare <-done forever. 16MB
	// is far above any legitimate MCP message.
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
		// Interactive handlers (#562 Bug C): never run them on the read-loop
		// goroutine — they block on user input (up to 5 minutes for
		// elicitation), stall the whole read loop, and cause every concurrent
		// request to time out and Abort the connection. handleServerRequestAsync
		// validates cheaply and defers the actual handler to a bounded worker.
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

// dispatchServerRequest is the read-loop entry point for server-initiated
// requests on the stdio path (#562 Bug C). It is a thin alias today but makes
// the read-loop policy ("quick handlers inline, interactive ones deferred")
// explicit at the call site.
func (c *Client) dispatchServerRequest(req *Request) error {
	return c.handleServerRequest(req)
}

// handleServerRequestAsync validates an interactive server request (sampling/
// elicitation) and runs it on a bounded worker goroutine. Handlers without a
// registered handler are rejected synchronously (cheap). Errors from the
// handler itself are converted to JSON-RPC error responses inside the
// goroutine, so this function returns only setup errors.
func (c *Client) handleServerRequestAsync(req *Request) error {
	isSampling := req.Method == "sampling/createMessage"
	if isSampling && c.samplingHandlerLocked() == nil {
		return c.writeErrorResponse(req.ID, -32601, "sampling not supported")
	}
	if !isSampling && c.elicitationHandlerLocked() == nil {
		return c.writeErrorResponse(req.ID, -32601, "elicitation not supported")
	}
	// Bounded dispatch: a server flooding us with interactive requests must
	// not spawn unbounded goroutines. Acquire a semaphore slot without
	// blocking the read loop — if all workers are busy, reject with
	// "server busy" instead of stalling reads (the exact failure mode of
	// Bug C, just bounded).
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

// maxInteractiveServerRequests bounds concurrent goroutines answering
// interactive server requests (sampling/elicitation) dispatched off the read
// loop (#562 Bug C).
const maxInteractiveServerRequests = 8

// handleInteractiveRequest runs on a worker goroutine (not the read loop) and
// answers the request via the registered handler. Assumes the corresponding
// handler is non-nil (checked by the caller).
func (c *Client) handleInteractiveRequest(req *Request) error {
	if req.Method == "sampling/createMessage" {
		return c.handleSampling(req)
	}
	return c.handleElicitation(req)
}

// handleSampling processes a sampling/createMessage request from the MCP server.
// Servers use this to ask the client to generate an LLM completion on their behalf.
func (c *Client) handleSampling(req *Request) error {
	handler := c.samplingHandlerLocked()
	if handler == nil {
		return c.writeErrorResponse(req.ID, -32601, "sampling not supported")
	}

	params, err := ParseSamplingParams(req.Params)
	if err != nil {
		return c.writeErrorResponse(req.ID, -32602, fmt.Sprintf("invalid sampling params: %v", err))
	}

	// Use a bounded timeout to prevent runaway sampling from blocking the
	// MCP read loop. The handler itself may use a shorter context.
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	result, err := handler(ctx, params)
	if err != nil {
		return c.writeErrorResponse(req.ID, -32603, fmt.Sprintf("sampling failed: %v", err))
	}
	return c.writeResultResponse(req.ID, result)
}

// handleElicitation processes an elicitation/create request from the MCP server.
// Servers use this to ask the client to collect structured input from the user.
func (c *Client) handleElicitation(req *Request) error {
	handler := c.elicitationHandlerLocked()
	if handler == nil {
		return c.writeErrorResponse(req.ID, -32601, "elicitation not supported")
	}

	params, err := ParseElicitationParams(req.Params)
	if err != nil {
		return c.writeErrorResponse(req.ID, -32602, fmt.Sprintf("invalid elicitation params: %v", err))
	}

	// Validate the server-provided schema before presenting it to the user.
	if err := ValidateElicitationSchema(params.Schema); err != nil {
		return c.writeErrorResponse(req.ID, -32602, fmt.Sprintf("invalid elicitation schema: %v", err))
	}

	// Bounded timeout to prevent blocking the MCP read loop indefinitely.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	result, err := handler(ctx, params)
	if err != nil {
		return c.writeErrorResponse(req.ID, -32603, fmt.Sprintf("elicitation failed: %v", err))
	}
	return c.writeResultResponse(req.ID, result)
}

// SetElicitationHandler registers a handler for elicitation/create requests.
// When set, the client advertises elicitation capability during initialize.
// Pass nil to disable elicitation support.
func (c *Client) SetElicitationHandler(h ElicitationHandler) {
	// #645: guard with c.mu — the field is read from Initialize and from the
	// read-loop goroutine (handleServerRequestAsync); an unlocked setter is a
	// latent race for any caller that does not finish before Start (the
	// SetNotificationHandler pattern).
	c.mu.Lock()
	c.elicitationHandler = h
	c.mu.Unlock()
}

// SetSamplingHandler registers a handler for sampling/createMessage requests.
// When set, the client advertises sampling capability during initialize.
// Pass nil to disable sampling support.
func (c *Client) SetSamplingHandler(h SamplingHandler) {
	// #645: same c.mu guard as SetElicitationHandler.
	c.mu.Lock()
	c.samplingHandler = h
	c.mu.Unlock()
}

// samplingHandlerLocked snapshots the registered sampling handler under c.mu
// (#645). Callers that cannot hold c.mu (Initialize builds caps before the
// request; the read-loop goroutine dispatches server requests) must read the
// field through this accessor, not directly.
func (c *Client) samplingHandlerLocked() SamplingHandler {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.samplingHandler
}

// elicitationHandlerLocked snapshots the registered elicitation handler under
// c.mu (#645 — companion to samplingHandlerLocked).
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

// SetNotificationHandler registers a callback for server-initiated notifications.
// The handler receives the notification method (e.g., "notifications/tools/list_changed")
// and raw params. Handlers are dispatched asynchronously from a dedicated
// goroutine (fix #255), so a handler MAY call back into the client (e.g.
// ListTools for a hot tool refresh) without deadlocking the read loop;
// long-running work should still be handed off to its own goroutine.
// Pass nil to disable notification processing (notifications are silently dropped).
func (c *Client) SetNotificationHandler(h func(method string, params json.RawMessage)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.notificationHandler = h
}

// notificationChanSize bounds the pending notification queue. If a handler is
// slower than the server emits notifications, excess ones are dropped rather
// than blocking the read loop (which would stall every in-flight request).
const notificationChanSize = 64

// processNotification queues a server notification for asynchronous handler
// dispatch (fix #255). It is called from inside the stdio read loop while
// readMu is held; it must never call the handler synchronously — a handler
// that re-enters sendRequest would block on the readMu held by this very
// call stack, deadlocking until the request timeout aborts the connection.
func (c *Client) processNotification(notif *Notification) {
	if notif == nil {
		return
	}
	// Read the handler pointer without acquiring c.mu. sendRequest holds c.mu
	// during the write phase, and processNotification is called from within
	// the read loop. A function-pointer read is atomic on 64-bit platforms;
	// the worst case is reading a stale pointer during handler replacement,
	// which is harmless (one notification delivered to the old handler).
	if c.notificationHandler == nil {
		return
	}
	// notificationCh/notificationDone are created in the constructors (NewClient/
	// NewClientFromConfig) before the Client is shared across goroutines, so
	// this unsynchronized read is race-free (fix #292: previously they were
	// lazily assigned inside this Once closure, which Abort() — never a Once
	// participant — could read concurrently without any happens-before edge).
	// Safety net: clients built via struct literals (tests, internal use) skip
	// the constructors; create their channels here under the Once so the
	// dispatch worker does not block on nil channels forever. Production code
	// only uses the constructors, so this fallback is never taken there and
	// the #292 race fix is preserved.
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
					// Handler runs on this worker goroutine, outside both readMu
					// and c.mu, so it may call sendRequest/ListTools freely.
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

// SetLevel requests the server to set its minimum logging level.
// The server must advertise the logging capability during initialize.
// Valid levels: "debug", "info", "notice", "warning", "error", "critical",
// "alert", "emergency".
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

// HasToolsListChanged returns true if the server advertised tools with listChanged.
func (c *Client) HasToolsListChanged() bool {
	_, caps := c.negotiatedState()
	return caps.Tools != nil && caps.Tools.ListChanged
}

// HasLogging returns true if the server supports the logging capability.
func (c *Client) HasLogging() bool {
	_, caps := c.negotiatedState()
	return caps.Logging != nil
}

// HasResourceSubscribe returns true if the server supports resource subscriptions.
func (c *Client) HasResourceSubscribe() bool {
	_, caps := c.negotiatedState()
	return caps.Resources != nil && caps.Resources.Subscribe
}

// --- MCP Protocol Types ---

type InitializeParams struct {
	ProtocolVersion string         `json:"protocolVersion"`
	Capabilities    ClientCaps     `json:"capabilities"`
	ClientInfo      Implementation `json:"clientInfo"`
}

type ClientCaps struct {
	Roots struct {
		ListChanged bool `json:"listChanged,omitempty"`
	} `json:"roots,omitempty"`
	Sampling    *struct{} `json:"sampling,omitempty"`
	Elicitation *struct{} `json:"elicitation,omitempty"`
}

type Implementation struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type InitializeResult struct {
	ProtocolVersion string         `json:"protocolVersion"`
	Capabilities    ServerCaps     `json:"capabilities"`
	ServerInfo      Implementation `json:"serverInfo"`
}

type ServerCaps struct {
	Tools      *ToolsCapability     `json:"tools,omitempty"`
	Resources  *ResourcesCapability `json:"resources,omitempty"`
	Prompts    *PromptsCapability   `json:"prompts,omitempty"`
	Logging    *struct{}            `json:"logging,omitempty"`
	Completion *struct{}            `json:"completion,omitempty"`
}

// ToolsCapability describes the server's tool capabilities.
type ToolsCapability struct {
	ListChanged bool `json:"listChanged,omitempty"`
}

// ResourcesCapability describes the server's resource capabilities.
type ResourcesCapability struct {
	Subscribe   bool `json:"subscribe,omitempty"`
	ListChanged bool `json:"listChanged,omitempty"`
}

// PromptsCapability describes the server's prompt capabilities.
type PromptsCapability struct {
	ListChanged bool `json:"listChanged,omitempty"`
}

type ListToolsParams struct {
	Cursor string `json:"cursor,omitempty"`
}

type ListToolsResult struct {
	Tools []ToolDefinition `json:"tools"`
	// NextCursor is the pagination cursor for the next tools/list page (#562 A).
	NextCursor string `json:"nextCursor,omitempty"`
}

type ListPromptsResult struct {
	Prompts []PromptDefinition `json:"prompts"`
	// NextCursor is the pagination cursor for the next prompts/list page (#562 A).
	NextCursor string `json:"nextCursor,omitempty"`
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
	// NextCursor is the pagination cursor for the next resources/list page (#562 A).
	NextCursor string `json:"nextCursor,omitempty"`
}

type ResourceDefinition struct {
	URI         string `json:"uri"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	MIMEType    string `json:"mimeType,omitempty"`
}

type ReadResourceParams struct {
	URI string `json:"uri"`
}

type ReadResourceResult struct {
	Contents []ResourceContent `json:"contents"`
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
}

type CallToolParams struct {
	Name      string                 `json:"name"`
	Arguments map[string]interface{} `json:"arguments,omitempty"`
}

type CallToolResult struct {
	Content []ToolContent `json:"content"`
	IsError bool          `json:"isError,omitempty"`
}

type ToolContent struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}
