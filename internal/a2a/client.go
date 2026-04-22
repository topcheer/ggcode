package a2a

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/safego"
)

// Client is an A2A protocol client that sends tasks to remote agents.
type Client struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
	card       *AgentCard // cached agent card (nil until discovered)
}

// NewClient creates a new A2A client targeting the given server URL.
func NewClient(baseURL, apiKey string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		httpClient: &http.Client{
			Timeout: 15 * time.Minute, // must exceed coordinator handler timeout
		},
	}
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
	if c.apiKey != "" {
		req.Header.Set("X-API-Key", c.apiKey)
	}

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

// CancelTask requests cancellation of a running task.
func (c *Client) CancelTask(ctx context.Context, taskID string) (*Task, error) {
	params := CancelTaskParams{ID: taskID}
	var result Task
	if err := c.rpc(ctx, "tasks/cancel", params, &result); err != nil {
		return nil, err
	}
	return &result, nil
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
	if c.apiKey != "" {
		req.Header.Set("X-API-Key", c.apiKey)
	}

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
	if c.apiKey != "" {
		req.Header.Set("X-API-Key", c.apiKey)
	}

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
