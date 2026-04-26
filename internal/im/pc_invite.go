package im

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const PCInviteURIPrefix = "privateclaw://connect?payload="

// PCEncodeInviteToURI encodes an invite into a privateclaw:// URI (exported for TUI).
func PCEncodeInviteToURI(invite PCInvite) (string, error) {
	data, err := json.Marshal(invite)
	if err != nil {
		return "", err
	}
	encoded := pcB64.EncodeToString(data)
	return PCInviteURIPrefix + encoded, nil
}

// pcDecodeInviteString decodes an invite from a URI, JSON string, or raw base64url.
func pcDecodeInviteString(input string) (*PCInvite, error) {
	trimmed := strings.TrimSpace(input)

	// Format 1: privateclaw://connect?payload=...
	if strings.HasPrefix(trimmed, "privateclaw://") {
		idx := strings.Index(trimmed, "payload=")
		if idx < 0 {
			return nil, fmt.Errorf("invalid invite URI: missing payload parameter")
		}
		b64 := trimmed[idx+len("payload="):]
		// Strip any trailing query parameters or fragments
		if ampIdx := strings.IndexByte(b64, '&'); ampIdx >= 0 {
			b64 = b64[:ampIdx]
		}
		if hashIdx := strings.IndexByte(b64, '#'); hashIdx >= 0 {
			b64 = b64[:hashIdx]
		}
		return pcDecodeInviteFromBase64(b64)
	}

	// Format 2: JSON object
	if strings.HasPrefix(trimmed, "{") {
		var invite PCInvite
		if err := json.Unmarshal([]byte(trimmed), &invite); err != nil {
			return nil, fmt.Errorf("invalid invite JSON: %w", err)
		}
		return &invite, nil
	}

	// Format 3: raw base64url
	return pcDecodeInviteFromBase64(trimmed)
}

func pcDecodeInviteFromBase64(b64 string) (*PCInvite, error) {
	data, err := pcB64.DecodeString(b64)
	if err != nil {
		return nil, fmt.Errorf("invalid invite base64: %w", err)
	}
	var invite PCInvite
	if err := json.Unmarshal(data, &invite); err != nil {
		return nil, fmt.Errorf("invalid invite payload: %w", err)
	}
	return &invite, nil
}

// pcBuildMessageID generates a unique message ID.
func pcBuildMessageID(prefix string) string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%s-%x", prefix, b)
}

// pcNowISO returns the current time in ISO 8601 format (UTC).
func pcNowISO() string {
	return time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
}

// pcGenerateRequestID generates a unique request ID.
func pcGenerateRequestID() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return fmt.Sprintf("req-%x", b)
}
