package lsp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type Position struct {
	Line      int
	Character int
}

type Range struct {
	Start Position
	End   Position
}

type Location struct {
	Path  string
	Range Range
}

type Diagnostic struct {
	Severity int
	Message  string
	Range    Range
	Source   string
}

type rawLocation struct {
	URI   string   `json:"uri"`
	Range rawRange `json:"range"`
}

type Symbol struct {
	Name string
	Kind int
	Range
}

type rawRange struct {
	Start struct {
		Line      int `json:"line"`
		Character int `json:"character"`
	} `json:"start"`
	End struct {
		Line      int `json:"line"`
		Character int `json:"character"`
	} `json:"end"`
}

type rawDocumentSymbol struct {
	Name     string              `json:"name"`
	Kind     int                 `json:"kind"`
	Range    rawRange            `json:"range"`
	Children []rawDocumentSymbol `json:"children"`
}

type rawSymbolInformation struct {
	Name     string      `json:"name"`
	Kind     int         `json:"kind"`
	Location rawLocation `json:"location"`
}

type rpcEnvelope struct {
	JSONRPC string          `json:"jsonrpc,omitempty"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type stdioClient struct {
	cmd      *exec.Cmd
	stdin    io.WriteCloser
	reader   *bufio.Reader
	waitErr  chan error
	resolved ResolvedServer

	nextID  int64
	pending map[string]chan rpcEnvelope
	mu      sync.Mutex

	stderr              bytes.Buffer
	notificationHandler func(method string, params json.RawMessage)
}

func Hover(ctx context.Context, workspace, path string, pos Position) (string, error) {
	result, err := withOpenDocument(ctx, workspace, path, func(ctx context.Context, session *sessionClient, docURI string) (string, error) {
		call := func() (string, error) {
			var raw json.RawMessage
			if err := session.client.call(ctx, "textDocument/hover", map[string]any{
				"textDocument": map[string]any{"uri": docURI},
				"position":     toLSPPosition(pos),
			}, &raw); err != nil {
				return "", err
			}
			return parseHover(raw), nil
		}
		return retryEmptyStringResult(ctx, session, call)
	})
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(result), nil
}

func Definition(ctx context.Context, workspace, path string, pos Position) ([]Location, error) {
	return withOpenDocument(ctx, workspace, path, func(ctx context.Context, session *sessionClient, docURI string) ([]Location, error) {
		call := func() ([]Location, error) {
			var raw json.RawMessage
			if err := session.client.call(ctx, "textDocument/definition", map[string]any{
				"textDocument": map[string]any{"uri": docURI},
				"position":     toLSPPosition(pos),
			}, &raw); err != nil {
				return nil, err
			}
			return parseLocations(raw), nil
		}
		return retryEmptySliceResult(ctx, session, call)
	})
}

func References(ctx context.Context, workspace, path string, pos Position) ([]Location, error) {
	return withOpenDocument(ctx, workspace, path, func(ctx context.Context, session *sessionClient, docURI string) ([]Location, error) {
		call := func() ([]Location, error) {
			var raw json.RawMessage
			if err := session.client.call(ctx, "textDocument/references", map[string]any{
				"textDocument": map[string]any{"uri": docURI},
				"position":     toLSPPosition(pos),
				"context":      map[string]any{"includeDeclaration": true},
			}, &raw); err != nil {
				return nil, err
			}
			return parseLocations(raw), nil
		}
		return retryEmptySliceResult(ctx, session, call)
	})
}

func DocumentSymbols(ctx context.Context, workspace, path string) ([]Symbol, error) {
	return withOpenDocument(ctx, workspace, path, func(ctx context.Context, session *sessionClient, docURI string) ([]Symbol, error) {
		call := func() ([]Symbol, error) {
			var raw json.RawMessage
			if err := session.client.call(ctx, "textDocument/documentSymbol", map[string]any{
				"textDocument": map[string]any{"uri": docURI},
			}, &raw); err != nil {
				return nil, err
			}
			return parseSymbols(raw), nil
		}
		return retryEmptySliceResult(ctx, session, call)
	})
}

func Diagnostics(ctx context.Context, workspace, path string) ([]Diagnostic, error) {
	return withOpenDocument(ctx, workspace, path, func(ctx context.Context, session *sessionClient, docURI string) ([]Diagnostic, error) {
		if session.supportsPullDiagnostics() {
			var raw json.RawMessage
			if err := session.client.call(ctx, "textDocument/diagnostic", map[string]any{
				"textDocument": map[string]any{"uri": docURI},
			}, &raw); err == nil {
				session.setPullDiagnosticsSupport(true)
				if parsed := parseDocumentDiagnostics(raw); len(parsed) > 0 {
					session.setPublishedDiagnostics(docURI, parsed)
					return parsed, nil
				}
			} else if isUnsupportedDiagnosticMethodError(err) {
				session.setPullDiagnosticsSupport(false)
			} else {
				return nil, err
			}
		}
		deadline := time.Now().Add(publishedDiagnosticsWait)
		for time.Now().Before(deadline) {
			if published, seen := session.publishedDiagnostics(docURI); seen {
				return published, nil
			}
			time.Sleep(40 * time.Millisecond)
		}
		return nil, nil
	})
}

func retryEmptyStringResult(ctx context.Context, session *sessionClient, call func() (string, error)) (string, error) {
	result, err := call()
	if err != nil || strings.TrimSpace(result) != "" || !session.shouldRetryEmptyResults() {
		return result, err
	}
	for i := 0; i < csharpWarmupRetryAttempts; i++ {
		if err := sleepWithContext(ctx, csharpWarmupRetryDelay); err != nil {
			return result, nil
		}
		next, err := call()
		if err != nil {
			return "", err
		}
		if strings.TrimSpace(next) != "" {
			return next, nil
		}
	}
	return result, nil
}

func retryEmptySliceResult[T any](ctx context.Context, session *sessionClient, call func() ([]T, error)) ([]T, error) {
	result, err := call()
	if err != nil || len(result) > 0 || !session.shouldRetryEmptyResults() {
		return result, err
	}
	for i := 0; i < csharpWarmupRetryAttempts; i++ {
		if err := sleepWithContext(ctx, csharpWarmupRetryDelay); err != nil {
			return result, nil
		}
		next, err := call()
		if err != nil {
			return nil, err
		}
		if len(next) > 0 {
			return next, nil
		}
	}
	return result, nil
}

func sleepWithContext(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func isUnsupportedDiagnosticMethodError(err error) bool {
	text := strings.ToLower(strings.TrimSpace(err.Error()))
	return strings.Contains(text, "unsupportedoperationexception") ||
		strings.Contains(text, "method not found") ||
		strings.Contains(text, "unsupported method") ||
		strings.Contains(text, "textdocument/diagnostic failed")
}

func startClient(ctx context.Context, workspace string, resolved ResolvedServer) (*stdioClient, error) {
	cmd := exec.Command(resolved.Binary, resolved.Args...)
	cmd.Dir = workspace
	cmd.Env = serverLaunchEnv(resolved.Binary)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	client := &stdioClient{
		cmd:      cmd,
		stdin:    stdin,
		reader:   bufio.NewReader(stdout),
		waitErr:  make(chan error, 1),
		resolved: resolved,
		pending:  make(map[string]chan rpcEnvelope),
	}
	cmd.Stderr = &client.stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	go func() { client.waitErr <- cmd.Wait() }()
	go client.readLoop()
	if err := client.call(ctx, "initialize", map[string]any{
		"processId": os.Getpid(),
		"rootPath":  workspace,
		"rootUri":   fileURI(workspace),
		"workspaceFolders": []map[string]string{{
			"uri":  fileURI(workspace),
			"name": filepath.Base(workspace),
		}},
		"capabilities": map[string]any{
			"workspace": map[string]any{
				"configuration":    true,
				"workspaceFolders": true,
			},
			"textDocument": map[string]any{
				"hover":      map[string]any{},
				"definition": map[string]any{},
				"references": map[string]any{},
				"codeAction": map[string]any{
					"dynamicRegistration": false,
					"dataSupport":         true,
					"resolveSupport": map[string]any{
						"properties": []string{"edit", "command"},
					},
					"codeActionLiteralSupport": map[string]any{
						"codeActionKind": map[string]any{
							"valueSet": []string{
								"",
								"quickfix",
								"refactor",
								"refactor.extract",
								"refactor.inline",
								"refactor.rewrite",
								"source",
								"source.organizeImports",
							},
						},
					},
				},
				"documentSymbol": map[string]any{
					"hierarchicalDocumentSymbolSupport": true,
				},
				"publishDiagnostics": map[string]any{
					"relatedInformation": true,
				},
			},
		},
	}, nil); err != nil {
		client.close()
		return nil, err
	}
	if err := client.notify(ctx, "initialized", map[string]any{}); err != nil {
		client.close()
		return nil, err
	}
	if err := client.notify(ctx, "workspace/didChangeConfiguration", map[string]any{
		"settings": clientWorkspaceSettings(resolved, workspace),
	}); err != nil {
		client.close()
		return nil, err
	}
	if strings.TrimSpace(workspace) != "" {
		if err := client.notify(ctx, "workspace/didChangeWorkspaceFolders", map[string]any{
			"event": map[string]any{
				"added": []map[string]string{{
					"uri":  fileURI(workspace),
					"name": filepath.Base(workspace),
				}},
				"removed": []map[string]string{},
			},
		}); err != nil {
			client.close()
			return nil, err
		}
	}
	return client, nil
}

func serverLaunchEnv(binary string) []string {
	env := os.Environ()
	if binaryBaseName(binary) != "csharp-ls" {
		return env
	}
	if hasEnvKey("DOTNET_ROOT") {
		return env
	}
	root := detectDotnetRoot()
	if strings.TrimSpace(root) == "" {
		return env
	}
	env = append(env, "DOTNET_ROOT="+root)
	if strings.TrimSpace(os.Getenv("DOTNET_ROOT_ARM64")) == "" {
		env = append(env, "DOTNET_ROOT_ARM64="+root)
	}
	return env
}

func hasEnvKey(key string) bool {
	value, ok := os.LookupEnv(key)
	return ok && strings.TrimSpace(value) != ""
}

func detectDotnetRoot() string {
	if root := strings.TrimSpace(os.Getenv("DOTNET_ROOT")); root != "" {
		return root
	}
	path, err := exec.LookPath("dotnet")
	if err != nil {
		return ""
	}
	resolvedPath, err := filepath.EvalSymlinks(path)
	if err == nil && strings.TrimSpace(resolvedPath) != "" {
		path = resolvedPath
	}
	dir := filepath.Dir(path)
	if filepath.Base(dir) == "libexec" {
		return dir
	}
	if filepath.Base(dir) == "bin" {
		candidate := filepath.Join(filepath.Dir(dir), "libexec")
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate
		}
	}
	return dir
}

func (c *stdioClient) close() {
	if c == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = c.call(ctx, "shutdown", map[string]any{}, nil)
	_ = c.notify(ctx, "exit", map[string]any{})
	_ = c.stdin.Close()
	select {
	case <-c.waitErr:
	case <-time.After(500 * time.Millisecond):
		if c.cmd != nil && c.cmd.Process != nil {
			_ = c.cmd.Process.Kill()
		}
	}
}

func (c *stdioClient) readLoop() {
	for {
		msg, err := readRPCMessage(c.reader)
		if err != nil {
			c.failPending(err)
			return
		}
		if hasRPCID(msg.ID) && strings.TrimSpace(msg.Method) != "" {
			c.handleServerRequest(msg)
			continue
		}
		if !hasRPCID(msg.ID) {
			if c.notificationHandler != nil && strings.TrimSpace(msg.Method) != "" {
				c.notificationHandler(msg.Method, msg.Params)
			}
			continue
		}
		idKey := rpcIDKey(msg.ID)
		c.mu.Lock()
		ch := c.pending[idKey]
		delete(c.pending, idKey)
		c.mu.Unlock()
		if ch != nil {
			ch <- msg
		}
	}
}

func (c *stdioClient) handleServerRequest(msg rpcEnvelope) {
	result, err := serverRequestResult(msg.Method, msg.Params, c.cmd.Dir, c.resolved)
	if err != nil {
		_ = c.write(rpcEnvelope{
			JSONRPC: "2.0",
			ID:      msg.ID,
			Error:   &rpcError{Code: -32603, Message: err.Error()},
		})
		return
	}
	_ = c.write(rpcEnvelope{
		JSONRPC: "2.0",
		ID:      msg.ID,
		Result:  result,
	})
}

func serverRequestResult(method string, params json.RawMessage, workspace string, resolved ResolvedServer) (json.RawMessage, error) {
	switch strings.TrimSpace(method) {
	case "client/registerCapability", "client/unregisterCapability", "window/workDoneProgress/create", "window/showMessageRequest":
		return json.RawMessage("null"), nil
	case "workspace/configuration":
		var payload struct {
			Items []struct {
				Section string `json:"section"`
			} `json:"items"`
		}
		if err := json.Unmarshal(params, &payload); err != nil {
			return nil, err
		}
		settings := clientWorkspaceSettings(resolved, workspace)
		results := make([]any, len(payload.Items))
		for i, item := range payload.Items {
			results[i] = workspaceSettingValue(settings, item.Section)
		}
		return json.Marshal(results)
	case "workspace/workspaceFolders":
		if strings.TrimSpace(workspace) == "" {
			return json.RawMessage("[]"), nil
		}
		return json.Marshal([]map[string]string{{
			"uri":  fileURI(workspace),
			"name": filepath.Base(workspace),
		}})
	default:
		return json.RawMessage("null"), nil
	}
}

func clientWorkspaceSettings(resolved ResolvedServer, workspace string) map[string]any {
	settings := map[string]any{}
	if binaryBaseName(resolved.Binary) == "csharp-ls" {
		settings["csharp"] = csharpWorkspaceSettings(resolved, workspace)
	}
	return settings
}

func csharpWorkspaceSettings(resolved ResolvedServer, workspace string) map[string]any {
	settings := map[string]any{
		"logLevel":               "info",
		"applyFormattingOptions": false,
		"useMetadataUris":        false,
		"razorSupport":           false,
		"solution":               nil,
		"solutionPathOverride":   nil,
	}
	if solution := csharpSolutionOverride(resolved, workspace); solution != "" {
		settings["solution"] = solution
		settings["solutionPathOverride"] = solution
	}
	return settings
}

func csharpSolutionOverride(resolved ResolvedServer, workspace string) string {
	for i := 0; i < len(resolved.Args)-1; i++ {
		if resolved.Args[i] != "--solution" {
			continue
		}
		solution := strings.TrimSpace(resolved.Args[i+1])
		if solution == "" {
			return ""
		}
		if filepath.IsAbs(solution) {
			return solution
		}
		if strings.TrimSpace(workspace) == "" {
			return solution
		}
		return filepath.Join(workspace, solution)
	}
	return ""
}

func workspaceSettingValue(settings map[string]any, section string) any {
	section = strings.TrimSpace(section)
	if section == "" {
		return settings
	}
	current := any(settings)
	for _, part := range strings.Split(section, ".") {
		m, ok := current.(map[string]any)
		if !ok {
			return map[string]any{}
		}
		next, ok := m[part]
		if !ok {
			return map[string]any{}
		}
		current = next
	}
	return current
}

func (c *stdioClient) failPending(err error) {
	if c != nil {
		if stderr := strings.TrimSpace(c.stderr.String()); stderr != "" {
			err = fmt.Errorf("%w (stderr: %s)", err, stderr)
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for id, ch := range c.pending {
		ch <- rpcEnvelope{ID: json.RawMessage(id), Error: &rpcError{Code: -1, Message: err.Error()}}
		delete(c.pending, id)
	}
}

func (c *stdioClient) call(ctx context.Context, method string, params any, out any) error {
	id := atomic.AddInt64(&c.nextID, 1)
	idRaw := rpcNumericID(id)
	rawParams, err := json.Marshal(params)
	if err != nil {
		return err
	}
	ch := make(chan rpcEnvelope, 1)
	c.mu.Lock()
	c.pending[rpcIDKey(idRaw)] = ch
	c.mu.Unlock()
	if err := c.write(rpcEnvelope{JSONRPC: "2.0", ID: idRaw, Method: method, Params: rawParams}); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case msg := <-ch:
		if msg.Error != nil {
			return fmt.Errorf("%s failed: %s", method, msg.Error.Message)
		}
		if out != nil && len(msg.Result) > 0 {
			if err := json.Unmarshal(msg.Result, out); err != nil {
				return err
			}
		}
		return nil
	}
}

func (c *stdioClient) notify(ctx context.Context, method string, params any) error {
	rawParams, err := json.Marshal(params)
	if err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return c.write(rpcEnvelope{JSONRPC: "2.0", Method: method, Params: rawParams})
	}
}

func (c *stdioClient) write(msg rpcEnvelope) error {
	body, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(c.stdin, "Content-Length: %d\r\n\r\n", len(body)); err != nil {
		return err
	}
	_, err = c.stdin.Write(body)
	return err
}

func readRPCMessage(r *bufio.Reader) (rpcEnvelope, error) {
	var msg rpcEnvelope
	contentLength := 0
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return msg, err
		}
		line = strings.TrimSpace(line)
		if line == "" {
			break
		}
		if strings.HasPrefix(strings.ToLower(line), "content-length:") {
			value := strings.TrimSpace(strings.TrimPrefix(strings.ToLower(line), "content-length:"))
			contentLength, err = strconv.Atoi(value)
			if err != nil {
				return msg, err
			}
		}
	}
	if contentLength <= 0 {
		return msg, fmt.Errorf("missing content length")
	}
	body := make([]byte, contentLength)
	if _, err := io.ReadFull(r, body); err != nil {
		return msg, err
	}
	if err := json.Unmarshal(body, &msg); err != nil {
		return msg, err
	}
	return msg, nil
}

func fileURI(path string) string {
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(path)}
	return u.String()
}

func hasRPCID(id json.RawMessage) bool {
	return len(bytes.TrimSpace(id)) > 0 && string(bytes.TrimSpace(id)) != "null"
}

func rpcIDKey(id json.RawMessage) string {
	return string(bytes.TrimSpace(id))
}

func rpcNumericID(id int64) json.RawMessage {
	return json.RawMessage(strconv.FormatInt(id, 10))
}

func toLSPPosition(pos Position) map[string]int {
	line := max(0, pos.Line-1)
	character := max(0, pos.Character-1)
	return map[string]int{"line": line, "character": character}
}

func parseHover(raw json.RawMessage) string {
	var response struct {
		Contents any `json:"contents"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		return ""
	}
	return stringifyHoverContents(response.Contents)
}

func stringifyHoverContents(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case map[string]any:
		if v, ok := typed["value"].(string); ok {
			return v
		}
	case []any:
		parts := make([]string, 0, len(typed))
		for _, item := range typed {
			if part := strings.TrimSpace(stringifyHoverContents(item)); part != "" {
				parts = append(parts, part)
			}
		}
		return strings.Join(parts, "\n\n")
	}
	return ""
}

func parseLocations(raw json.RawMessage) []Location {
	var list []rawLocation
	if err := json.Unmarshal(raw, &list); err == nil && len(list) > 0 {
		return toLocations(list)
	}
	var single rawLocation
	if err := json.Unmarshal(raw, &single); err == nil && single.URI != "" {
		return toLocations([]rawLocation{single})
	}
	return nil
}

func toLocations(list []rawLocation) []Location {
	out := make([]Location, 0, len(list))
	for _, item := range list {
		path := uriToPath(item.URI)
		out = append(out, Location{
			Path: path,
			Range: Range{
				Start: Position{Line: item.Range.Start.Line + 1, Character: item.Range.Start.Character + 1},
				End:   Position{Line: item.Range.End.Line + 1, Character: item.Range.End.Character + 1},
			},
		})
	}
	return out
}

func parseSymbols(raw json.RawMessage) []Symbol {
	var probe []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &probe); err != nil || len(probe) == 0 {
		return nil
	}
	if _, ok := probe[0]["location"]; ok {
		var infos []rawSymbolInformation
		if err := json.Unmarshal(raw, &infos); err != nil {
			return nil
		}
		out := make([]Symbol, 0, len(infos))
		for _, item := range infos {
			out = append(out, Symbol{
				Name:  item.Name,
				Kind:  item.Kind,
				Range: toRange(item.Location.Range),
			})
		}
		return out
	}

	var documents []rawDocumentSymbol
	if err := json.Unmarshal(raw, &documents); err == nil && len(documents) > 0 {
		out := make([]Symbol, 0, len(documents))
		var walk func(rawDocumentSymbol)
		walk = func(item rawDocumentSymbol) {
			out = append(out, Symbol{Name: item.Name, Kind: item.Kind, Range: toRange(item.Range)})
			for _, child := range item.Children {
				walk(child)
			}
		}
		for _, item := range documents {
			walk(item)
		}
		return out
	}
	return nil
}

func toRange(r rawRange) Range {
	return Range{
		Start: Position{Line: r.Start.Line + 1, Character: r.Start.Character + 1},
		End:   Position{Line: r.End.Line + 1, Character: r.End.Character + 1},
	}
}

func parseDocumentDiagnostics(raw json.RawMessage) []Diagnostic {
	var response struct {
		Items []struct {
			Severity int    `json:"severity"`
			Message  string `json:"message"`
			Source   string `json:"source"`
			Range    struct {
				Start struct {
					Line      int `json:"line"`
					Character int `json:"character"`
				} `json:"start"`
				End struct {
					Line      int `json:"line"`
					Character int `json:"character"`
				} `json:"end"`
			} `json:"range"`
		} `json:"items"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		return nil
	}
	out := make([]Diagnostic, 0, len(response.Items))
	for _, item := range response.Items {
		out = append(out, Diagnostic{
			Severity: item.Severity,
			Message:  item.Message,
			Source:   item.Source,
			Range: Range{
				Start: Position{Line: item.Range.Start.Line + 1, Character: item.Range.Start.Character + 1},
				End:   Position{Line: item.Range.End.Line + 1, Character: item.Range.End.Character + 1},
			},
		})
	}
	return out
}

func uriToPath(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "file" {
		return raw
	}
	return filepath.Clean(u.Path)
}
