package mcp

import (
	"bytes"
	"encoding/json"
	"fmt"
)

type ID struct {
	value interface{}
}

func NewStringID(s string) ID { return ID{value: s} }
func NewIntID(n int64) ID     { return ID{value: n} }

func (id ID) MarshalJSON() ([]byte, error) { return json.Marshal(id.value) }

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
			id.value = n
		}
		return nil
	}
	id.value = v
	return nil
}

type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
	ID      *ID             `json:"id,omitempty"`
}

type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *Error          `json:"error,omitempty"`
	ID      json.RawMessage `json:"id,omitempty"`
}

type Error struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *Error) Error() string {
	return fmt.Sprintf("JSON-RPC error %d: %s", e.Code, e.Message)
}

type Notification struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

func (r *Response) IsError() bool { return r.Error != nil }

func ParseMessage(data []byte) (interface{}, error) {

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parsing JSON-RPC message: %w", err)
	}

	_, hasResult := raw["result"]
	_, hasError := raw["error"]
	if hasResult && hasError {
		return nil, fmt.Errorf("malformed JSON-RPC message: both result and error present")
	}
	if hasResult {
		var resp Response
		if err := json.Unmarshal(data, &resp); err != nil {
			return nil, err
		}
		return &resp, nil
	}
	if hasError {
		var resp Response
		if err := json.Unmarshal(data, &resp); err != nil {
			return nil, err
		}
		return &resp, nil
	}

	if v, ok := raw["id"]; ok && !isNullJSON(v) {
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

func isNullJSON(v json.RawMessage) bool {
	return string(bytes.TrimSpace(v)) == "null"
}
