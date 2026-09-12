package provider

// #2129: outputTokens had no zero-guard while inputTokens/cache tokens
// did (#722/#1168) - the SSE protocol allows multiple message_delta
// events, and a gateway replaying a trailing delta with an all-zero
// Usage zeroed the accumulated output count (probe: deltas 42-then-zero
// reported output=0 while input=100 stayed guarded). Symmetric guard.

import "testing"

func TestZZIssue2129_TrailingZeroMessageDeltaKeepsOutputTokens(t *testing.T) {
	sse := "" +
		"event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"m\",\"content\":[],\"stop_reason\":null,\"usage\":{\"input_tokens\":100,\"output_tokens\":1,\"cache_creation_input_tokens\":0,\"cache_read_input_tokens\":0}}}\n\n" +
		"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n" +
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"hello\"}}\n\n" +
		"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n" +
		// First delta carries the real cumulative output count.
		"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":42}}\n\n" +
		// A replayed/synthesized trailing delta with zeroed Usage must NOT
		// zero the accumulated count.
		"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{},\"usage\":{\"output_tokens\":0}}\n\n" +
		"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"

	_, output, _, _ := zz1168RunStream(t, sse)
	if output != 42 {
		t.Fatalf("OutputTokens = %d, want 42 (trailing zero-usage message_delta must not clobber the accumulated count)", output)
	}
}

func TestZZIssue2129_LegitimateFinalDeltaStillWins(t *testing.T) {
	sse := "" +
		"event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"m\",\"content\":[],\"stop_reason\":null,\"usage\":{\"input_tokens\":10,\"output_tokens\":1,\"cache_creation_input_tokens\":0,\"cache_read_input_tokens\":0}}}\n\n" +
		"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n" +
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"hi\"}}\n\n" +
		"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n" +
		"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":7}}\n\n" +
		"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"

	_, output, _, _ := zz1168RunStream(t, sse)
	if output != 7 {
		t.Fatalf("OutputTokens = %d, want 7 (a genuine non-zero final delta is the protocol's cumulative count)", output)
	}
}
