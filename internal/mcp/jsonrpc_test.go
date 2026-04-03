package mcp

import (
	"encoding/json"
	"testing"
)

func TestParseRequest(t *testing.T) {
	data := `{"jsonrpc":"2.0","method":"test","params":{"x":1},"id":1}`
	msg, err := ParseMessage([]byte(data))
	if err != nil {
		t.Fatal(err)
	}
	req, ok := msg.(*Request)
	if !ok {
		t.Fatalf("expected *Request, got %T", msg)
	}
	if req.Method != "test" {
		t.Errorf("method = %q, want %q", req.Method, "test")
	}
	if req.ID == nil {
		t.Fatal("ID is nil")
	}
}

func TestParseResponse(t *testing.T) {
	data := `{"jsonrpc":"2.0","result":{"tools":[]},"id":1}`
	msg, err := ParseMessage([]byte(data))
	if err != nil {
		t.Fatal(err)
	}
	resp, ok := msg.(*Response)
	if !ok {
		t.Fatalf("expected *Response, got %T", msg)
	}
	if resp.IsError() {
		t.Error("expected non-error response")
	}
	var result map[string]interface{}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatal(err)
	}
}

func TestParseErrorResponse(t *testing.T) {
	data := `{"jsonrpc":"2.0","error":{"code":-32600,"message":"Invalid Request"},"id":1}`
	msg, err := ParseMessage([]byte(data))
	if err != nil {
		t.Fatal(err)
	}
	resp, ok := msg.(*Response)
	if !ok {
		t.Fatalf("expected *Response, got %T", msg)
	}
	if !resp.IsError() {
		t.Error("expected error response")
	}
	if resp.Error.Code != -32600 {
		t.Errorf("code = %d, want -32600", resp.Error.Code)
	}
}

func TestParseNotification(t *testing.T) {
	data := `{"jsonrpc":"2.0","method":"notifications/initialized"}`
	msg, err := ParseMessage([]byte(data))
	if err != nil {
		t.Fatal(err)
	}
	_, ok := msg.(*Notification)
	if !ok {
		t.Fatalf("expected *Notification, got %T", msg)
	}
}

func TestIDMarshalInt(t *testing.T) {
	id := NewIntID(42)
	b, err := json.Marshal(id)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "42" {
		t.Errorf("marshaled = %q, want %q", b, "42")
	}
}

func TestIDMarshalString(t *testing.T) {
	id := NewStringID("abc")
	b, err := json.Marshal(id)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != `"abc"` {
		t.Errorf("marshaled = %q, want %q", b, `"abc"`)
	}
}

func TestErrorString(t *testing.T) {
	e := &Error{Code: -32601, Message: "Method not found"}
	s := e.Error()
	if s != "JSON-RPC error -32601: Method not found" {
		t.Errorf("Error() = %q", s)
	}
}
