package tunnel

import (
	"encoding/json"
	"strings"
)

// #5633 family (mobile/relay parity with desktop PR #5633, ChatView.tsx
// stripGuidancePreamble): mid-run injected guidance is stored verbatim in the
// session (first text block = LLM-facing preamble), but it must never render
// as user-visible content on the tunnel display chain. Strip the known
// preamble families at the emit/record side; keep the payload that follows.
// Only the exact startsWith families below are stripped - normal user text is
// never touched, and the session/provider (LLM context) face is unaffected.
var userPreambleMarkers = [...]string{
	"New user guidance arrived while you were working",
	"The user RETRACTED the request",
}

// StripUserPreamble strips an LLM-facing mid-run guidance preamble from
// user-visible message text. Preamble and payload are separated by "\n\n".
// A preamble-only message (e.g. a retraction notice) becomes empty so
// clients hide it instead of rendering the raw LLM-facing instruction.
func StripUserPreamble(raw string) string {
	for _, marker := range userPreambleMarkers {
		if strings.HasPrefix(raw, marker) {
			if sep := strings.Index(raw, "\n\n"); sep >= 0 {
				return strings.TrimSpace(raw[sep+2:])
			}
			return ""
		}
	}
	return raw
}

// StripUserMessageData returns a copy of MessageData with LLM-facing
// preambles stripped from the display fields.
func StripUserMessageData(data MessageData) MessageData {
	data.Text = StripUserPreamble(data.Text)
	data.DisplayText = StripUserPreamble(data.DisplayText)
	return data
}

// StripUserMessageEvent strips LLM-facing preambles from a recorded
// user_message event payload. Events of other types and malformed payloads
// pass through untouched; when nothing changes, the input message is
// returned without re-encoding.
func StripUserMessageEvent(msg GatewayMessage) GatewayMessage {
	if msg.Type != EventUserMessage || len(msg.Data) == 0 {
		return msg
	}
	var data MessageData
	if err := json.Unmarshal(msg.Data, &data); err != nil {
		return msg
	}
	stripped := StripUserMessageData(data)
	if stripped.Text == data.Text && stripped.DisplayText == data.DisplayText {
		return msg
	}
	if dataBytes, err := json.Marshal(stripped); err == nil {
		msg.Data = dataBytes
	}
	return msg
}
