//go:build goolm

package mcp

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestIssue597_M1_SSEResponseIDMatching(t *testing.T) {
	stream := "data: " + `{"jsonrpc":"2.0","id":111,"result":{"content":[{"type":"text","text":"foreign"}]}}` + "\n\n" +
		"data: " + `{"jsonrpc":"2.0","id":222,"result":{"content":[{"type":"text","text":"ours"}]}}` + "\n\n"

	ours := NewIntID(222)
	resp, err := extractSSEResponseForID([]byte(stream), &ours)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	var payload struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	_ = json.Unmarshal(resp.Result, &payload)
	if len(payload.Content) == 0 || payload.Content[0].Text != "ours" {
		t.Fatalf("cross-injection: got %v", string(resp.Result))
	}

	any, err := extractSSEResponse([]byte(stream))
	if err != nil {
		t.Fatalf("legacy extract: %v", err)
	}
	_ = any
}

func TestIssue597_M1_NoMatchingIDReportsError(t *testing.T) {
	stream := "data: " + `{"jsonrpc":"2.0","id":111,"result":{}}` + "\n\n"
	ours := NewIntID(999)
	_, err := extractSSEResponseForID([]byte(stream), &ours)
	if err == nil {
		t.Fatal("expected error when no response carries our id")
	}
}

func TestIssue597_M2_OversizedSSELineSurfacesError(t *testing.T) {
	huge := "data: " + strings.Repeat("x", 2*1024*1024) + "\n\n"
	events, scanErr := extractAllSSEDataChecked([]byte(huge))
	if scanErr == nil {
		t.Fatalf("expected scanner error for 2MB line, got none (%d events)", len(events))
	}
	if len(events) != 0 {
		t.Fatalf("oversized line must not silently truncate into events: %d", len(events))
	}
	if !strings.Contains(scanErr.Error(), "scanning SSE") {
		t.Fatalf("error should carry SSE context: %v", scanErr)
	}
}

func TestIssue605_G1_MidStreamErrTooLongNotSwallowed(t *testing.T) {
	body := "data: " + `{"jsonrpc":"2.0","method":"notifications/progress","params":{}}` + "\n\n" +
		"data: " + strings.Repeat("x", 2*1024*1024) + "\n\n"
	_, err := extractSSEResponse([]byte(body))
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "scanning SSE") {
		t.Fatalf("truncation cause must outrank count message, got: %v", err)
	}
}
