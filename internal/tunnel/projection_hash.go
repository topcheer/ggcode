package tunnel

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

type projectionHashEvent struct {
	SessionID string          `json:"session_id,omitempty"`
	EventID   string          `json:"event_id,omitempty"`
	StreamID  string          `json:"stream_id,omitempty"`
	Type      string          `json:"type"`
	Data      json.RawMessage `json:"data,omitempty"`
}

func ProjectionEventHash(msg GatewayMessage) string {
	data, err := json.Marshal(projectionHashEvent{
		SessionID: msg.SessionID,
		EventID:   msg.EventID,
		StreamID:  msg.StreamID,
		Type:      msg.Type,
		Data:      msg.Data,
	})
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func ProjectionHash(events []GatewayMessage) string {
	if len(events) == 0 {
		return ""
	}
	return ProjectionHashPrefix(events, len(events))
}

func ProjectionHashPrefix(events []GatewayMessage, limit int) string {
	if limit <= 0 || len(events) == 0 {
		return ""
	}
	ordered := append([]GatewayMessage(nil), events...)
	SortReplayEvents(ordered)
	if limit > len(ordered) {
		limit = len(ordered)
	}
	hasher := sha256.New()
	for i := 0; i < limit; i++ {
		hash := ProjectionEventHash(ordered[i])
		if hash == "" {
			continue
		}
		_, _ = hasher.Write([]byte(hash))
		_, _ = hasher.Write([]byte{'\n'})
	}
	sum := hasher.Sum(nil)
	if len(sum) == 0 {
		return ""
	}
	return hex.EncodeToString(sum)
}
