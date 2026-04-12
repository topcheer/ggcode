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
	ID      int64           `json:"id,omitempty"`
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
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	reader  *bufio.Reader
	waitErr chan error

	nextID  int64
	pending map[int64]chan rpcEnvelope
	mu      sync.Mutex

	stderr              bytes.Buffer
	notificationHandler func(method string, params json.RawMessage)
}

func Hover(ctx context.Context, workspace, path string, pos Position) (string, error) {
	result, err := withOpenDocument(ctx, workspace, path, func(ctx context.Context, session *sessionClient, docURI string) (string, error) {
		var raw json.RawMessage
		if err := session.client.call(ctx, "textDocument/hover", map[string]any{
			"textDocument": map[string]any{"uri": docURI},
			"position":     toLSPPosition(pos),
		}, &raw); err != nil {
			return "", err
		}
		return parseHover(raw), nil
	})
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(result), nil
}

func Definition(ctx context.Context, workspace, path string, pos Position) ([]Location, error) {
	return withOpenDocument(ctx, workspace, path, func(ctx context.Context, session *sessionClient, docURI string) ([]Location, error) {
		var raw json.RawMessage
		if err := session.client.call(ctx, "textDocument/definition", map[string]any{
			"textDocument": map[string]any{"uri": docURI},
			"position":     toLSPPosition(pos),
		}, &raw); err != nil {
			return nil, err
		}
		return parseLocations(raw), nil
	})
}

func References(ctx context.Context, workspace, path string, pos Position) ([]Location, error) {
	return withOpenDocument(ctx, workspace, path, func(ctx context.Context, session *sessionClient, docURI string) ([]Location, error) {
		var raw json.RawMessage
		if err := session.client.call(ctx, "textDocument/references", map[string]any{
			"textDocument": map[string]any{"uri": docURI},
			"position":     toLSPPosition(pos),
			"context":      map[string]any{"includeDeclaration": true},
		}, &raw); err != nil {
			return nil, err
		}
		return parseLocations(raw), nil
	})
}

func DocumentSymbols(ctx context.Context, workspace, path string) ([]Symbol, error) {
	return withOpenDocument(ctx, workspace, path, func(ctx context.Context, session *sessionClient, docURI string) ([]Symbol, error) {
		var raw json.RawMessage
		if err := session.client.call(ctx, "textDocument/documentSymbol", map[string]any{
			"textDocument": map[string]any{"uri": docURI},
		}, &raw); err != nil {
			return nil, err
		}
		return parseSymbols(raw), nil
	})
}

func Diagnostics(ctx context.Context, workspace, path string) ([]Diagnostic, error) {
	return withOpenDocument(ctx, workspace, path, func(ctx context.Context, session *sessionClient, docURI string) ([]Diagnostic, error) {
		var raw json.RawMessage
		if err := session.client.call(ctx, "textDocument/diagnostic", map[string]any{
			"textDocument": map[string]any{"uri": docURI},
		}, &raw); err == nil {
			if parsed := parseDocumentDiagnostics(raw); len(parsed) > 0 {
				session.setPublishedDiagnostics(docURI, parsed)
				return parsed, nil
			}
		}
		deadline := time.Now().Add(400 * time.Millisecond)
		for time.Now().Before(deadline) {
			if published, seen := session.publishedDiagnostics(docURI); seen {
				return published, nil
			}
			time.Sleep(40 * time.Millisecond)
		}
		return nil, nil
	})
}

func startClient(ctx context.Context, workspace string, resolved ResolvedServer) (*stdioClient, error) {
	cmd := exec.Command(resolved.Binary)
	cmd.Dir = workspace
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	client := &stdioClient{
		cmd:     cmd,
		stdin:   stdin,
		reader:  bufio.NewReader(stdout),
		waitErr: make(chan error, 1),
		pending: make(map[int64]chan rpcEnvelope),
	}
	cmd.Stderr = &client.stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	go func() { client.waitErr <- cmd.Wait() }()
	go client.readLoop()
	if err := client.call(ctx, "initialize", map[string]any{
		"processId": os.Getpid(),
		"rootUri":   fileURI(workspace),
		"capabilities": map[string]any{
			"textDocument": map[string]any{
				"hover":      map[string]any{},
				"definition": map[string]any{},
				"references": map[string]any{},
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
	return client, nil
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
		if msg.ID == 0 {
			if c.notificationHandler != nil && strings.TrimSpace(msg.Method) != "" {
				c.notificationHandler(msg.Method, msg.Params)
			}
			continue
		}
		c.mu.Lock()
		ch := c.pending[msg.ID]
		delete(c.pending, msg.ID)
		c.mu.Unlock()
		if ch != nil {
			ch <- msg
		}
	}
}

func (c *stdioClient) failPending(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for id, ch := range c.pending {
		ch <- rpcEnvelope{ID: id, Error: &rpcError{Code: -1, Message: err.Error()}}
		delete(c.pending, id)
	}
}

func (c *stdioClient) call(ctx context.Context, method string, params any, out any) error {
	id := atomic.AddInt64(&c.nextID, 1)
	rawParams, err := json.Marshal(params)
	if err != nil {
		return err
	}
	ch := make(chan rpcEnvelope, 1)
	c.mu.Lock()
	c.pending[id] = ch
	c.mu.Unlock()
	if err := c.write(rpcEnvelope{JSONRPC: "2.0", ID: id, Method: method, Params: rawParams}); err != nil {
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
