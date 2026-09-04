package mcp

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// JSON-RPC 2.0 types per https://www.jsonrpc.org/specification

// ID can be a string or number.
type ID struct {
	value interface{}
}

func NewStringID(s string) ID { return ID{value: s} }
func NewIntID(n int64) ID     { return ID{value: n} }

func (id ID) MarshalJSON() ([]byte, error) { return json.Marshal(id.value) }

// UnmarshalJSON keeps numeric precision (#1591-C): decoding into
// interface{} turns every number into float64, so snowflake-scale request
// IDs (>2^53) from server-initiated requests (sampling/elicitation) lost
// precision and the echoed response ID no longer matched, hanging the
// server side. Small ints still become int64; anything that does not fit
// is preserved as json.Number, which marshals back to the exact original
// text.
func (id *ID) UnmarshalJSON(data []byte) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var v interface{}
	if err := dec.Decode(&v); err != nil {
		return err
	}
	if n, ok := v.(json.Number); ok {
		if i, err := n.Int64(); err == nil {
			id.value = i
		} else {
			id.value = n // json.Number marshals as the raw literal
		}
		return nil
	}
	id.value = v
	return nil
}

// Request is a JSON-RPC request.
type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
	ID      *ID             `json:"id,omitempty"`
}

// Response is a JSON-RPC response.
type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *Error          `json:"error,omitempty"`
	ID      json.RawMessage `json:"id,omitempty"`
}

// Error is a JSON-RPC error object.
type Error struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *Error) Error() string {
	return fmt.Sprintf("JSON-RPC error %d: %s", e.Code, e.Message)
}

// Notification is a JSON-RPC notification (no ID).
type Notification struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// IsResponseError returns true if the response contains an error.
func (r *Response) IsError() bool { return r.Error != nil }

// ParseMessage parses a JSON-RPC message from raw bytes.
// It can be a Request, Response, or Notification.
func ParseMessage(data []byte) (interface{}, error) {
	// Check for "result" or "error" field to identify responses
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parsing JSON-RPC message: %w", err)
	}

	if _, ok := raw["result"]; ok {
		var resp Response
		if err := json.Unmarshal(data, &resp); err != nil {
			return nil, err
		}
		return &resp, nil
	}
	if _, ok := raw["error"]; ok {
		var resp Response
		if err := json.Unmarshal(data, &resp); err != nil {
			return nil, err
		}
		return &resp, nil
	}

	// Has ID -> Request, no ID -> Notification
	if _, ok := raw["id"]; ok {
		var req Request
		if err := json.Unmarshal(data, &req); err != nil {
			return nil, err
		}
		return &req, nil
	}

	var notif Notification
	if err := json.Unmarshal(data, &notif); err != nil {
		return nil, err
	}
	return &notif, nil
}
