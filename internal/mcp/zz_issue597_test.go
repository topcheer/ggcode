//go:build goolm

package mcp

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestIssue597_M1_SSEResponseIDMatching: the waiter for id=222 must never
// receive id=111's response — concurrent streamable-HTTP requests share
// response streams, and the parser previously returned the FIRST parseable
// Response (cross-request tool-output injection, probe verbatim).
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

	// Legacy nil-reqID form still returns the first response.
	any, err := extractSSEResponse([]byte(stream))
	if err != nil {
		t.Fatalf("legacy extract: %v", err)
	}
	_ = any
}

// TestIssue597_M1_NoMatchingIDReportsError: a stream with only foreign ids
// must NOT silently succeed with someone else's result.
func TestIssue597_M1_NoMatchingIDReportsError(t *testing.T) {
	stream := "data: " + `{"jsonrpc":"2.0","id":111,"result":{}}` + "\n\n"
	ours := NewIntID(999)
	_, err := extractSSEResponseForID([]byte(stream), &ours)
	if err == nil {
		t.Fatal("expected error when no response carries our id")
	}
}

// TestIssue597_M2_OversizedSSELineSurfacesError: a >1MB single SSE data
// line exceeds bufio's cap; the old code swallowed scanner.Err and
// misdiagnosed it as "no data event found".
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

// TestIssue605_G1_MidStreamErrTooLongNotSwallowed: a notification followed
// by a >1MB line previously reported "no Response found in 1 event(s)"
// — #597 M2's misdiagnosis preserved one step later (#605 G1 probe).
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
